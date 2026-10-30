package engine

// External sorted driver index. The CSV is read once per allocation/stage;
// sorting uses bounded chunks, and only key -> file-range metadata stays in RAM.
// Both index and sorted runs live in the current unpublished working directory.

import (
	"bufio"
	"container/heap"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const driverSortChunk = 12000

type driverRange struct {
	Start  int64
	Length int64
}
type diskDriver struct {
	path       string
	file       *os.File
	ranges     map[string]driverRange
	count      int64
	persistent bool
}
type sortedDriver struct {
	Key string    `json:"k"`
	Row DriverRow `json:"r"`
}
type driverRun struct {
	f       *os.File
	r       *bufio.Reader
	d       *json.Decoder
	current sortedDriver
}
type driverEntry struct {
	row sortedDriver
	run int
}
type driverHeap []driverEntry

func (h driverHeap) Len() int { return len(h) }
func (h driverHeap) Less(i, j int) bool {
	if h[i].row.Key != h[j].row.Key {
		return h[i].row.Key < h[j].row.Key
	}
	return h[i].run < h[j].run
}
func (h driverHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *driverHeap) Push(v any)   { *h = append(*h, v.(driverEntry)) }
func (h *driverHeap) Pop() any     { a := *h; v := a[len(a)-1]; *h = a[:len(a)-1]; return v }

// mergeDriverRuns bounds concurrently open sort-run descriptors at 32.
// It preserves key ordering and uses only a single record per input run.
func mergeDriverRuns(paths []string, destination string) error {
	f, err := os.Create(destination)
	if err != nil {
		return err
	}
	okay := false
	defer func() {
		_ = f.Close()
		if !okay {
			_ = os.Remove(destination)
		}
	}()
	bw := bufio.NewWriterSize(f, 256*1024)
	enc := json.NewEncoder(bw)
	runs := make([]*driverRun, len(paths))
	defer func() {
		for _, r := range runs {
			if r != nil {
				_ = r.f.Close()
			}
		}
	}()
	hp := &driverHeap{}
	heap.Init(hp)
	for i, path := range paths {
		in, e := os.Open(path)
		if e != nil {
			return e
		}
		r := &driverRun{f: in, r: bufio.NewReaderSize(in, 256*1024)}
		r.d = json.NewDecoder(r.r)
		runs[i] = r
		var item sortedDriver
		if e = r.d.Decode(&item); e != nil {
			return e
		}
		heap.Push(hp, driverEntry{row: item, run: i})
	}
	for hp.Len() > 0 {
		item := heap.Pop(hp).(driverEntry)
		if e := enc.Encode(item.row); e != nil {
			return e
		}
		var next sortedDriver
		e := runs[item.run].d.Decode(&next)
		if e == nil {
			heap.Push(hp, driverEntry{row: next, run: item.run})
		} else if e != io.EOF {
			return e
		}
	}
	if err = bw.Flush(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	okay = true
	return nil
}

func driverReference(a Allocation, st Stage, dimensions []string, hier map[string]map[string][]string) (driverRef, map[string]func(string) bool, error) {
	ref := driverRef{alloc: a, ix: driverIndex{buckets: map[string][]DriverRow{}}, targetChecks: map[string]func(string) bool{}}
	filters, err := makeChecks(a.DriverFilters, hier)
	if err != nil {
		return ref, nil, err
	}
	for dim, t := range st.Target {
		if t.Mode == "INCLUDE" || t.Mode == "INCLUDE_EXCLUDE" {
			fn, e := compileFilter(dim, Filter{Include: t.Include, Exclude: t.Exclude}, hier)
			if e != nil {
				return ref, nil, e
			}
			ref.targetChecks[dim] = fn
		}
	}
	return ref, filters, nil
}

// buildDiskDriverIndex preserves the CSV validation, filtering and sparse
// SOURCE-dimension semantics of buildDriverIndex without ReadDrivers-all.
func buildDiskDriverIndex(path, field, prefix string, a Allocation, st Stage, dims []string, h map[string]map[string][]string) (driverRef, error) {
	ref, filters, err := driverReference(a, st, dims, h)
	if err != nil {
		return ref, err
	}
	input, err := os.Open(path)
	if err != nil {
		return ref, err
	}
	defer input.Close()
	reader := csv.NewReader(bufio.NewReaderSize(input, 256*1024))
	reader.FieldsPerRecord = -1
	headers, err := reader.Read()
	if err != nil {
		return ref, err
	}
	pos, err := HeaderPositions(headers)
	if err != nil {
		return ref, fmt.Errorf("%s: %w", path, err)
	}
	ix, ok := pos[field]
	if !ok {
		return ref, fail("%s missing weight column %s", path, field)
	}
	found := 0
	for _, d := range dims {
		if _, ok := pos[d]; ok {
			found++
			if st.Target[d].Mode == "SOURCE" {
				ref.ix.bound = append(ref.ix.bound, d)
			}
		}
	}
	if found == 0 {
		return ref, fail("%s has no matching dimensions", path)
	}
	for dim := range filters {
		if _, ok := pos[dim]; !ok {
			return ref, fail("driver filter requires missing column %s", dim)
		}
	}
	for dim := range ref.targetChecks {
		if _, ok := pos[dim]; !ok {
			return ref, fail("target mode INCLUDE requires missing driver dimension %s", dim)
		}
	}
	var runPaths []string
	defer func() {
		for _, p := range runPaths {
			_ = os.Remove(p)
		}
	}()
	chunk := make([]sortedDriver, 0, driverSortChunk)
	writeRun := func() error {
		if len(chunk) == 0 {
			return nil
		}
		sort.SliceStable(chunk, func(i, j int) bool { return chunk[i].Key < chunk[j].Key })
		name := fmt.Sprintf("%s_run_%04d.jsonl", prefix, len(runPaths))
		f, e := os.Create(name)
		if e != nil {
			return e
		}
		bw := bufio.NewWriterSize(f, 256*1024)
		enc := json.NewEncoder(bw)
		for _, it := range chunk {
			if e = enc.Encode(it); e != nil {
				break
			}
		}
		if e == nil {
			e = bw.Flush()
		}
		if closeErr := f.Close(); e == nil {
			e = closeErr
		}
		if e != nil {
			os.Remove(name)
			return e
		}
		runPaths = append(runPaths, name)
		chunk = chunk[:0]
		return nil
	}
	line := 1
	for {
		rec, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return ref, fmt.Errorf("%s:%d %w", path, line+1, e)
		}
		line++
		coord := map[string]string{}
		for _, d := range dims {
			if p, ok := pos[d]; ok {
				if p >= len(rec) || rec[p] == "" {
					return ref, fail("%s:%d missing coordinate %s", path, line, d)
				}
				coord[d] = rec[p]
			}
		}
		if ix >= len(rec) {
			return ref, fail("%s:%d missing weight", path, line)
		}
		weight, e := ParseScaled(rec[ix], 6)
		if e != nil {
			return ref, fail("%s:%d invalid weight: %v", path, line, e)
		}
		good := true
		for dim, fn := range filters {
			if !fn(coord[dim]) {
				good = false
				break
			}
		}
		if good {
			for dim, fn := range ref.targetChecks {
				if !fn(coord[dim]) {
					good = false
					break
				}
			}
		}
		if !good {
			continue
		}
		chunk = append(chunk, sortedDriver{Key: key(coord, ref.ix.bound), Row: DriverRow{Coord: coord, Weights: map[string]int64{field: weight}}})
		if len(chunk) == driverSortChunk {
			if e := writeRun(); e != nil {
				return ref, e
			}
		}
	}
	if err := writeRun(); err != nil {
		return ref, err
	}
	// Multi-pass 32-way merges prevent exhausting OS file descriptors on
	// tens of millions of driver rows / thousands of sorted chunks.
	for pass := 0; len(runPaths) > 32; pass++ {
		next := []string{}
		for first := 0; first < len(runPaths); first += 32 {
			last := first + 32
			if last > len(runPaths) {
				last = len(runPaths)
			}
			group := runPaths[first:last]
			if len(group) == 1 {
				next = append(next, group[0])
				continue
			}
			dest := fmt.Sprintf("%s_merge_%02d_%04d.jsonl", prefix, pass, len(next))
			if err := mergeDriverRuns(group, dest); err != nil {
				return ref, err
			}
			for _, p := range group {
				_ = os.Remove(p)
			}
			next = append(next, dest)
		}
		runPaths = next
	}

	finalPath := prefix + "_sorted.jsonl"
	out, e := os.Create(finalPath)
	if e != nil {
		return ref, e
	}
	okWrite := false
	defer func() {
		if !okWrite {
			out.Close()
			os.Remove(finalPath)
		}
	}()
	bw := bufio.NewWriterSize(out, 256*1024)
	runs := make([]*driverRun, len(runPaths))
	defer func() {
		for _, r := range runs {
			if r != nil {
				r.f.Close()
			}
		}
	}()
	hp := &driverHeap{}
	heap.Init(hp)
	for i, p := range runPaths {
		f, e := os.Open(p)
		if e != nil {
			return ref, e
		}
		r := &driverRun{f: f, r: bufio.NewReaderSize(f, 256*1024)}
		r.d = json.NewDecoder(r.r)
		runs[i] = r
		var item sortedDriver
		if e = r.d.Decode(&item); e != nil {
			return ref, e
		}
		heap.Push(hp, driverEntry{row: item, run: i})
	}
	ranges := map[string]driverRange{}
	var written int64
	for hp.Len() > 0 {
		item := heap.Pop(hp).(driverEntry)
		raw, e := json.Marshal(item.row.Row)
		if e != nil {
			return ref, e
		}
		span := int64(len(raw) + 1)
		rr, exists := ranges[item.row.Key]
		if !exists {
			rr = driverRange{Start: written}
		}
		rr.Length += span
		ranges[item.row.Key] = rr
		if _, e = bw.Write(raw); e != nil {
			return ref, e
		}
		if e = bw.WriteByte('\n'); e != nil {
			return ref, e
		}
		written += span
		var next sortedDriver
		e = runs[item.run].d.Decode(&next)
		if e == nil {
			heap.Push(hp, driverEntry{row: next, run: item.run})
		} else if e != io.EOF {
			return ref, e
		}
	}
	if e = bw.Flush(); e != nil {
		return ref, e
	}
	if e = out.Close(); e != nil {
		return ref, e
	}
	file, e := os.Open(finalPath)
	if e != nil {
		return ref, e
	}
	okWrite = true
	ref.ix.disk = &diskDriver{path: finalPath, file: file, ranges: ranges, count: written}
	return ref, nil
}

// forEach streams a single sparse SOURCE-key range without materializing all
// raw driver rows. The caller aggregates only the distinct target coordinates.
func (d *diskDriver) forEach(k string, emit func(DriverRow) error) error {
	r, ok := d.ranges[k]
	if !ok {
		return nil
	}
	dec := json.NewDecoder(io.NewSectionReader(d.file, r.Start, r.Length))
	for {
		var row DriverRow
		e := dec.Decode(&row)
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		if e = emit(row); e != nil {
			return e
		}
	}
}
func (d *diskDriver) load(k string) ([]DriverRow, error) {
	r, ok := d.ranges[k]
	if !ok {
		return nil, nil
	}
	dec := json.NewDecoder(io.NewSectionReader(d.file, r.Start, r.Length))
	result := []DriverRow{}
	for {
		var row DriverRow
		e := dec.Decode(&row)
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		result = append(result, row)
	}
	return result, nil
}
func (d *diskDriver) close() {
	if d != nil {
		if d.file != nil {
			d.file.Close()
		}
		if !d.persistent {
			_ = os.Remove(filepath.Clean(d.path))
		}
	}
}

// Driver indexes are content-addressed by source file bytes AND full semantic
// configuration. A manifest is published last; partial builds are ignored.
// This is a local, trusted filesystem cache. Use separate store roots per
// trust boundary; never share this writable directory across untrusted users.
type cachedDriverMeta struct {
	Version      int                    `json:"version"`
	Key          string                 `json:"key"`
	SourceSHA256 string                 `json:"source_sha256"`
	DataSHA256   string                 `json:"data_sha256"`
	DataBytes    int64                  `json:"data_bytes"`
	Ranges       map[string]driverRange `json:"ranges"`
	Bound        []string               `json:"bound"`
}

func cacheKey(path, field string, a Allocation, st Stage, dims []string, h map[string]map[string][]string) (string, string, error) {
	sourceHash, e := fileSHA(path)
	if e != nil {
		return "", "", e
	}
	semantic := struct {
		Version       int                            `json:"version"`
		SHA           string                         `json:"source_sha256"`
		Field         string                         `json:"field"`
		DriverFilters map[string]Filter              `json:"driver_filters"`
		Target        map[string]Target              `json:"target"`
		Dimensions    []string                       `json:"dimensions"`
		Hierarchies   map[string]map[string][]string `json:"hierarchies"`
	}{2, sourceHash, field, a.DriverFilters, st.Target, dims, h}
	b, e := json.Marshal(semantic)
	if e != nil {
		return "", "", e
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), sourceHash, nil
}

// buildCachedDiskDriverIndex can be called concurrently by jobs or processes.
// Atomic O_EXCL lock avoids parallel builds for the same source+configuration.
// Lock lease timeout fails closed rather than reusing a partially built index.
func buildCachedDiskDriverIndex(path, field, prefix, cacheDir string, a Allocation, st Stage, dims []string, h map[string]map[string][]string) (driverRef, error) {
	ref, _, e := driverReference(a, st, dims, h)
	if e != nil {
		return ref, e
	}
	if cacheDir == "" {
		return buildDiskDriverIndex(path, field, prefix, a, st, dims, h)
	}
	if e = os.MkdirAll(cacheDir, 0750); e != nil {
		return ref, e
	}
	id, sha, e := cacheKey(path, field, a, st, dims, h)
	if e != nil {
		return ref, e
	}
	dataPath := filepath.Join(cacheDir, id+".jsonl")
	metaPath := filepath.Join(cacheDir, id+".meta.json")
	tryLoad := func() (driverRef, bool) {
		blob, er := os.ReadFile(metaPath)
		if er != nil {
			return ref, false
		}
		var m cachedDriverMeta
		if json.Unmarshal(blob, &m) != nil || m.Version != 1 || m.Key != id || m.SourceSHA256 != sha {
			return ref, false
		}
		f, er := os.Open(dataPath)
		if er != nil {
			return ref, false
		}
		info, er := f.Stat()
		if er != nil || info.Size() != m.DataBytes {
			f.Close()
			return ref, false
		}
		digest, er := fileSHA(dataPath)
		if er != nil || digest != m.DataSHA256 {
			f.Close()
			return ref, false
		}
		for _, r := range m.Ranges {
			if r.Start < 0 || r.Length < 0 || r.Start > m.DataBytes || r.Length > m.DataBytes-r.Start {
				f.Close()
				return ref, false
			}
		}
		ref.ix.bound = m.Bound
		ref.ix.disk = &diskDriver{path: dataPath, file: f, ranges: m.Ranges, count: m.DataBytes, persistent: true}
		return ref, true
	}
	if loaded, ok := tryLoad(); ok {
		loaded.cacheHit = true
		return loaded, nil
	}
	lockPath := filepath.Join(cacheDir, id+".lock")
	var lock *os.File
	deadline := time.Now().Add(30 * time.Second)
	for {
		if loaded, ok := tryLoad(); ok {
			loaded.cacheHit = true
			return loaded, nil
		}
		lock, e = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e == nil {
			break
		}
		if !os.IsExist(e) {
			return ref, e
		}
		if time.Now().After(deadline) {
			return ref, fmt.Errorf("driver index lock timed out (%s); remove stale .lock after verifying no builder is active", id[:12])
		}
		time.Sleep(100 * time.Millisecond)
	}
	lock.Close()
	defer os.Remove(lockPath)
	// A previous failed build may have left an orphan data file; metadata is
	// the commit marker. The fresh build uses a unique scratch prefix.
	if loaded, ok := tryLoad(); ok {
		loaded.cacheHit = true
		return loaded, nil
	}
	scratch := prefix + "_cachebuild_" + id[:12]
	built, e := buildDiskDriverIndex(path, field, scratch, a, st, dims, h)
	if e != nil {
		return ref, e
	}
	defer built.ix.disk.close()
	contentsPath := built.ix.disk.path
	size := built.ix.disk.count
	digest, e := fileSHA(contentsPath)
	if e != nil {
		return ref, e
	}
	meta := cachedDriverMeta{Version: 1, Key: id, SourceSHA256: sha, DataSHA256: digest, DataBytes: size, Ranges: built.ix.disk.ranges, Bound: built.ix.bound}
	b, e := json.Marshal(meta)
	if e != nil {
		return ref, e
	}
	// Windows cannot rename an open file; close the builder handle first.
	if e = built.ix.disk.file.Close(); e != nil {
		return ref, e
	}
	built.ix.disk.file = nil
	_ = os.Remove(dataPath) // orphan from uncommitted build (under exclusive lock)
	_ = os.Remove(metaPath)
	if e = os.Rename(contentsPath, dataPath); e != nil {
		return ref, e
	}
	// Remove should not destroy a shared committed data file now that it has moved.
	built.ix.disk.persistent = true
	tmpMeta := metaPath + ".pending"
	if e = os.WriteFile(tmpMeta, b, 0600); e != nil {
		return ref, e
	}
	if e = os.Rename(tmpMeta, metaPath); e != nil {
		return ref, e
	}
	fresh, err := tryCachedOrError(tryLoad)
	fresh.cacheHit = false
	return fresh, err
}
func tryCachedOrError(f func() (driverRef, bool)) (driverRef, error) {
	ref, ok := f()
	if !ok {
		return ref, fmt.Errorf("cached index verification failed after publish")
	}
	return ref, nil
}
