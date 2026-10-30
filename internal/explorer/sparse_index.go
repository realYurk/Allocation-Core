package explorer

// Bounded chunk dictionary for generic sparse CSVs. No dimension is privileged.
// Only whole CSV records are indexed; exact filters skip provably irrelevant chunks.
// A saturated dictionary is marked wildcard: it can never cause a false exclusion.
import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"sync"
	"time"
)

const chunkRows = 4096
const maxChunkMembers = 96
const maxIndexedMemberEntries = 300000

type sliceChunk struct {
	start, end int64
	rows       int
	members    map[string]map[string]bool
}
type sparseIndex struct {
	size       int64
	modified   time.Time
	dimensions []string
	chunks     []sliceChunk
	rows       int64
	partial    bool
}
type indexStore struct {
	mu sync.Mutex
	m  map[string]*sparseIndex
}

func (a *App) cacheIndex(path string, dims []string) (*sparseIndex, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	a.indexes.mu.Lock()
	defer a.indexes.mu.Unlock()
	if x := a.indexes.m[path]; x != nil && x.size == fi.Size() && x.modified.Equal(fi.ModTime()) {
		return x, nil
	}
	idx, err := buildIndex(path, dims)
	if err != nil {
		return nil, err
	}
	a.indexes.m[path] = idx
	return idx, nil
}
func buildIndex(path string, dims []string) (*sparseIndex, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	stat, e := f.Stat()
	if e != nil {
		return nil, e
	}
	rd := csv.NewReader(bufio.NewReaderSize(f, 256*1024))
	rd.FieldsPerRecord = -1
	header, e := rd.Read()
	if e != nil {
		return nil, e
	}
	pos := map[string]int{}
	for i, c := range header {
		pos[c] = i
	}
	for _, d := range dims {
		if _, ok := pos[d]; !ok {
			return nil, fmt.Errorf("missing dimension %q", d)
		}
	}
	result := &sparseIndex{size: stat.Size(), modified: stat.ModTime(), dimensions: append([]string(nil), dims...)}
	start := rd.InputOffset()
	entryCount := 0
	c := sliceChunk{start: start, members: map[string]map[string]bool{}}
	for _, d := range dims {
		c.members[d] = map[string]bool{}
	}
	for {
		rec, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(rec) != len(header) {
			return nil, fmt.Errorf("index row %d: field count mismatch", result.rows+2)
		}
		result.rows++
		c.rows++
		for _, d := range dims {
			set := c.members[d]
			if set == nil {
				continue
			}
			v := rec[pos[d]]
			if set[v] {
				continue
			}
			if len(set) >= maxChunkMembers || entryCount >= maxIndexedMemberEntries {
				c.members[d] = nil
				result.partial = true
				continue
			}
			set[v] = true
			entryCount++
		}
		if c.rows >= chunkRows {
			c.end = rd.InputOffset()
			result.chunks = append(result.chunks, c)
			c = sliceChunk{start: c.end, members: map[string]map[string]bool{}}
			for _, d := range dims {
				c.members[d] = map[string]bool{}
			}
		}
	}
	if c.rows > 0 {
		c.end = rd.InputOffset()
		result.chunks = append(result.chunks, c)
	}
	stat2, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	if stat2.Size() != stat.Size() || !stat2.ModTime().Equal(stat.ModTime()) {
		return nil, errors.New("CSV changed while building index")
	}
	return result, nil
}
func (a *App) scanFiltered(path string, dims []string, filters map[string]string, pick func(map[string]string, *big.Rat) error) (scanned int64, skipped int64, err error) {
	if len(filters) == 0 {
		return 0, 0, scanRecords(path, dims, func(coord map[string]string, v *big.Rat) error { scanned++; return pick(coord, v) })
	}
	index, err := a.cacheIndex(path, dims)
	if err != nil {
		return 0, 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	hd, positions, err := headerAndIdx(path)
	if err != nil {
		return 0, 0, err
	}
	for _, chunk := range index.chunks {
		omit := false
		for d, v := range filters {
			if v == "" {
				continue
			}
			if values := chunk.members[d]; values != nil && !values[v] {
				omit = true
				break
			}
		}
		if omit {
			skipped += int64(chunk.rows)
			continue
		}
		if _, err = f.Seek(chunk.start, io.SeekStart); err != nil {
			return scanned, skipped, err
		}
		lr := io.LimitReader(f, chunk.end-chunk.start)
		rd := csv.NewReader(bufio.NewReaderSize(lr, 64*1024))
		rd.FieldsPerRecord = -1
		for n := 0; n < chunk.rows; n++ {
			rec, e := rd.Read()
			if e != nil {
				return scanned, skipped, fmt.Errorf("chunk read: %w", e)
			}
			if len(rec) != len(hd) {
				return scanned, skipped, errors.New("indexed CSV row count changed")
			}
			scanned++
			matches := true
			for d, v := range filters {
				if v != "" && rec[positions[d]] != v {
					matches = false
					break
				}
			}
			if !matches {
				continue
			}
			value, e := parseNumber(rec[positions["value"]])
			if e != nil {
				return scanned, skipped, e
			}
			coord := map[string]string{}
			for _, d := range dims {
				coord[d] = rec[positions[d]]
			}
			if e = pick(coord, value); e != nil {
				return scanned, skipped, e
			}
		}
	}
	return scanned, skipped, nil
}
