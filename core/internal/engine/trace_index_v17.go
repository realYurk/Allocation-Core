package engine

// Optional, disposable trace acceleration. The index stores only byte offsets
// into the original CSV evidence; it never modifies money or evidence.
// CSV InputOffset keeps quoted multiline records safe for random access.

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
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

const traceIndexFormat = "allocation-core/trace-index-v17/1"
const traceShards = 64

type traceFileReceipt struct {
	Size    int64  `json:"size"`
	MTimeNS int64  `json:"mtime_ns"`
	SHA256  string `json:"sha256"`
	Rows    int64  `json:"rows"`
}
type traceIndexReceipt struct {
	Format     string                      `json:"format"`
	CreatedUTC string                      `json:"created_utc"`
	Files      map[string]traceFileReceipt `json:"files"`
	Shards     map[string]string           `json:"shard_sha256"`
}
type traceIndex struct {
	base    string
	files   map[string]traceFileReceipt
	shards  map[string][]byte
	digests map[string]string
}

var evidenceIndexCols = map[string][]string{
	"ledger.csv":         {"target_row_id"},
	"lineage_links.csv":  {"child_row_id"},
	"source_to_pool.csv": {"stage", "pool"},
}

func evidenceKey(name string, r []string, positions map[string]int) string {
	if name == "source_to_pool.csv" {
		return r[positions["stage"]] + "\x1f" + r[positions["pool"]]
	}
	return r[positions[evidenceIndexCols[name][0]]]
}

// BuildTraceIndex builds an atomic on-disk accelerator. It retains the CSVs and
// records their hashes in its manifest. A failed build never publishes an index.
func BuildTraceIndex(dir string) (traceIndexReceipt, error) {
	receipt := traceIndexReceipt{Format: traceIndexFormat, CreatedUTC: time.Now().UTC().Format(time.RFC3339), Files: map[string]traceFileReceipt{}, Shards: map[string]string{}}
	if _, err := os.Stat(filepath.Join(dir, "trace_index")); err == nil {
		return receipt, fmt.Errorf("trace index already exists; remove trace_index directory before rebuilding")
	} else if !os.IsNotExist(err) {
		return receipt, err
	}
	tmp, err := os.MkdirTemp(dir, ".trace-index-building-")
	if err != nil {
		return receipt, err
	}
	defer os.RemoveAll(tmp)
	names := []string{"ledger.csv", "lineage_links.csv", "source_to_pool.csv"}
	for _, name := range names {
		path := filepath.Join(dir, name)
		f, e := os.Open(path)
		if e != nil {
			return receipt, e
		}
		stat, e := f.Stat()
		if e != nil {
			f.Close()
			return receipt, e
		}
		r := csv.NewReader(bufio.NewReaderSize(f, 512*1024))
		r.FieldsPerRecord = -1
		header, e := r.Read()
		if e != nil {
			f.Close()
			return receipt, e
		}
		pos, e := HeaderPositions(header)
		if e != nil {
			f.Close()
			return receipt, e
		}
		for _, k := range evidenceIndexCols[name] {
			if _, ok := pos[k]; !ok {
				f.Close()
				return receipt, fmt.Errorf("%s missing field %s", name, k)
			}
		}
		sub := filepath.Join(tmp, name)
		if e = os.MkdirAll(sub, 0755); e != nil {
			f.Close()
			return receipt, e
		}
		outs := make([]*os.File, traceShards)
		writers := make([]*bufio.Writer, traceShards)
		cleanup := func() error {
			var first error
			for i, x := range outs {
				if x != nil {
					if e := writers[i].Flush(); e != nil && first == nil {
						first = e
					}
					if e := x.Close(); e != nil && first == nil {
						first = e
					}
				}
			}
			return first
		}
		for i := 0; i < traceShards; i++ {
			fp := filepath.Join(sub, fmt.Sprintf("%02d.bin", i))
			outs[i], e = os.Create(fp)
			if e != nil {
				_ = cleanup()
				f.Close()
				return receipt, e
			}
			writers[i] = bufio.NewWriterSize(outs[i], 64*1024)
		}
		var n int64
		for {
			offset := r.InputOffset()
			rec, er := r.Read()
			if er == io.EOF {
				break
			}
			if er != nil {
				e = er
				break
			}
			if len(rec) < len(header) {
				e = fmt.Errorf("%s short CSV record", name)
				break
			}
			k := evidenceKey(name, rec, pos)
			sum := sha256.Sum256([]byte(k))
			var raw [24]byte
			copy(raw[:16], sum[:16])
			binary.LittleEndian.PutUint64(raw[16:], uint64(offset))
			if _, er = writers[int(sum[0])%traceShards].Write(raw[:]); er != nil {
				e = er
				break
			}
			n++
		}
		if closeErr := cleanup(); e == nil {
			e = closeErr
		}
		_ = f.Close()
		if e != nil {
			return receipt, fmt.Errorf("index %s: %w", name, e)
		}
		sha, e := fileSHA(path)
		if e != nil {
			return receipt, e
		}
		receipt.Files[name] = traceFileReceipt{Size: stat.Size(), MTimeNS: stat.ModTime().UnixNano(), SHA256: sha, Rows: n}
		for shard := 0; shard < traceShards; shard++ {
			key := name + "/" + fmt.Sprintf("%02d.bin", shard)
			digest, err := fileSHA(filepath.Join(tmp, key))
			if err != nil {
				return receipt, err
			}
			receipt.Shards[key] = digest
		}
	}
	data, e := json.MarshalIndent(receipt, "", "  ")
	if e != nil {
		return receipt, e
	}
	if e = os.WriteFile(filepath.Join(tmp, "manifest.json"), data, 0644); e != nil {
		return receipt, e
	}
	if e = os.Rename(tmp, filepath.Join(dir, "trace_index")); e != nil {
		return receipt, e
	}
	return receipt, nil
}

// VerifyTraceIndex performs the expensive SHA-256 check, not only mtime/size.
func VerifyTraceIndex(dir string) error {
	idx, e := openTraceIndex(dir)
	if e != nil {
		return e
	}
	if idx == nil {
		return fmt.Errorf("trace index not found")
	}
	for name, receipt := range idx.files {
		sha, e := fileSHA(filepath.Join(dir, name))
		if e != nil {
			return e
		}
		if sha != receipt.SHA256 {
			return fmt.Errorf("trace index invalid: %s SHA-256 differs", name)
		}
	}
	for key, want := range idx.digests {
		sha, err := fileSHA(filepath.Join(dir, "trace_index", filepath.FromSlash(key)))
		if err != nil {
			return err
		}
		if sha != want {
			return fmt.Errorf("trace index shard checksum differs: %s", key)
		}
	}
	return nil
}
func openTraceIndex(dir string) (*traceIndex, error) {
	raw, e := os.ReadFile(filepath.Join(dir, "trace_index", "manifest.json"))
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var receipt traceIndexReceipt
	if e = json.Unmarshal(raw, &receipt); e != nil {
		return nil, e
	}
	if receipt.Format != traceIndexFormat {
		return nil, fmt.Errorf("unsupported trace index format")
	}
	for name := range evidenceIndexCols {
		meta, ok := receipt.Files[name]
		if !ok {
			return nil, fmt.Errorf("trace index lacks %s", name)
		}
		stat, e := os.Stat(filepath.Join(dir, name))
		if e != nil {
			return nil, e
		}
		if stat.Size() != meta.Size || stat.ModTime().UnixNano() != meta.MTimeNS {
			return nil, fmt.Errorf("trace index stale for %s: rebuild it", name)
		}
	}
	if len(receipt.Shards) != traceShards*len(evidenceIndexCols) {
		return nil, fmt.Errorf("incomplete trace shard manifest")
	}
	return &traceIndex{base: dir, files: receipt.Files, shards: map[string][]byte{}, digests: receipt.Shards}, nil
}

// lookup verifies original record keys after digest match (including collision
// protection). Only the selected shard is loaded, instead of multi-GB scans.
func (idx *traceIndex) lookup(file, key string, fields []string, fn func([]string) error) error {
	sum := sha256.Sum256([]byte(key))
	shard := fmt.Sprintf("%02d.bin", int(sum[0])%traceShards)
	cacheKey := file + "/" + shard
	data, ok := idx.shards[cacheKey]
	if !ok {
		var e error
		data, e = os.ReadFile(filepath.Join(idx.base, "trace_index", file, shard))
		if e != nil {
			return e
		}
		if len(data)%24 != 0 {
			return fmt.Errorf("broken trace index shard: %s", cacheKey)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != idx.digests[cacheKey] {
			return fmt.Errorf("trace index shard corrupted: %s", cacheKey)
		}
		idx.shards[cacheKey] = data
	}
	f, e := os.Open(filepath.Join(idx.base, file))
	if e != nil {
		return e
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	header, e := r.Read()
	if e != nil {
		return e
	}
	positions, e := HeaderPositions(header)
	if e != nil {
		return e
	}
	indexes := make([]int, len(fields))
	for i, k := range fields {
		p, ok := positions[k]
		if !ok {
			return fmt.Errorf("%s missing %s", file, k)
		}
		indexes[i] = p
	}
	for i := 0; i < len(data); i += 24 {
		if string(data[i:i+16]) != string(sum[:16]) {
			continue
		}
		off := int64(binary.LittleEndian.Uint64(data[i+16 : i+24]))
		if off < 0 || off >= idx.files[file].Size {
			return fmt.Errorf("invalid trace offset")
		}
		sr := csv.NewReader(io.NewSectionReader(f, off, idx.files[file].Size-off))
		sr.FieldsPerRecord = -1
		rec, e := sr.Read()
		if e != nil {
			return e
		}
		if len(rec) < len(header) {
			return fmt.Errorf("short indexed record")
		}
		if evidenceKey(file, rec, positions) != key {
			return fmt.Errorf("trace index key mismatch; rebuild index")
		}
		selected := make([]string, len(fields))
		for j, p := range indexes {
			selected[j] = rec[p]
		}
		if e = fn(selected); e != nil {
			return e
		}
	}
	return nil
}

// ScanIndexedEvidence lets TraceRow use its existing streaming algorithm as a
// fallback when no index has been constructed.
func scanIndexedEvidence(idx *traceIndex, dir, file string, keys map[string]bool, fields []string, keyOf func([]string) string, fn func([]string) error) error {
	if idx != nil {
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			if e := idx.lookup(file, k, fields, fn); e != nil {
				return e
			}
		}
		return nil
	}
	return scanEvidence(filepath.Join(dir, file), fields, func(r []string) error {
		if !keys[keyOf(r)] {
			return nil
		}
		return fn(r)
	})
}

// Digest is for user-visible index diagnostics without reproducing large file paths.
func (i *traceIndex) Digests() map[string]string {
	out := map[string]string{}
	for k, v := range i.files {
		out[k] = v.SHA256
	}
	return out
}
