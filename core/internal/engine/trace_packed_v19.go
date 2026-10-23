package engine

// V19 opt-in compressed trace accelerator. It never modifies the original
// gzip CSVs or financial results. A bounded-memory builder creates independent
// gzip chunks and small hashed-key index shards, permitting a trace without
// expanding multi-GB audit files. Chunks are independently checksummed by
// gzip and the index/packed data are protected with SHA-256 manifests.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const packedTraceFormat = "allocation-core/packed-trace-v19/1"
const packedTraceDir = "trace_packed_v19"
const packedTraceRowsPerChunk = 512
const packedTraceMaxChunkBytes = 1024 * 1024
const packedEntryBytes = 28

type PackedTraceFile struct {
	Source       EvidenceStreamFile `json:"source"`
	SourceMTime  int64              `json:"source_mtime_ns"`
	Header       []string           `json:"header"`
	Rows         int64              `json:"rows"`
	Blocks       int64              `json:"blocks"`
	PackedSHA256 string             `json:"packed_sha256"`
	BlocksSHA256 string             `json:"blocks_sha256"`
	Shards       map[string]string  `json:"shards_sha256"`
	PackedSize   int64              `json:"packed_bytes"`
}
type PackedTraceReceipt struct {
	Format     string                     `json:"format"`
	CreatedUTC string                     `json:"created_utc"`
	Files      map[string]PackedTraceFile `json:"files"`
}

type packedTraceIndex struct {
	base  string
	files map[string]PackedTraceFile
}

func packedTracePath(dir, name string) string { return filepath.Join(dir, packedTraceDir, name) }
func packedShard(key string) int              { sum := sha256.Sum256([]byte(key)); return int(sum[0]) % traceShards }

// BuildPackedTraceIndex only accepts a complete, verified V18 stream manifest.
// Stale/damaged sources are refused. Publishing is atomic at directory level.
func BuildPackedTraceIndex(dir string) (PackedTraceReceipt, error) {
	receipt := PackedTraceReceipt{Format: packedTraceFormat, CreatedUTC: time.Now().UTC().Format(time.RFC3339), Files: map[string]PackedTraceFile{}}
	if _, e := os.Stat(filepath.Join(dir, packedTraceDir)); e == nil {
		return receipt, errors.New("packed trace index already exists: remove trace_packed_v19 to rebuild")
	} else if !os.IsNotExist(e) {
		return receipt, e
	}
	sourceManifest, e := VerifyEvidenceStream(dir)
	if e != nil {
		return receipt, fmt.Errorf("source evidence verification: %w", e)
	}
	tmp, e := os.MkdirTemp(dir, ".trace-packed-building-")
	if e != nil {
		return receipt, e
	}
	defer os.RemoveAll(tmp)
	for _, name := range []string{"ledger.csv", "lineage_links.csv", "source_to_pool.csv"} {
		source := sourceManifest.Files[name]
		sourcePath := filepath.Join(dir, source.StoredName)
		stat, e := os.Stat(sourcePath)
		if e != nil {
			return receipt, e
		}
		dstDir := filepath.Join(tmp, name)
		if e = os.MkdirAll(dstDir, 0755); e != nil {
			return receipt, e
		}
		dst, e := os.Create(filepath.Join(dstDir, "chunks.bgz"))
		if e != nil {
			return receipt, e
		}
		blocks, e := os.Create(filepath.Join(dstDir, "blocks.bin"))
		if e != nil {
			dst.Close()
			return receipt, e
		}
		shardFiles := make([]*os.File, traceShards)
		shards := make([]*bufio.Writer, traceShards)
		closeAll := func() error {
			var first error
			for i, f := range shardFiles {
				if f != nil {
					if er := shards[i].Flush(); er != nil && first == nil {
						first = er
					}
					if er := f.Close(); er != nil && first == nil {
						first = er
					}
				}
			}
			if er := blocks.Close(); er != nil && first == nil {
				first = er
			}
			if er := dst.Close(); er != nil && first == nil {
				first = er
			}
			return first
		}
		for i := 0; i < traceShards; i++ {
			f, er := os.Create(filepath.Join(dstDir, fmt.Sprintf("%02d.bin", i)))
			if er != nil {
				closeAll()
				return receipt, er
			}
			shardFiles[i] = f
			shards[i] = bufio.NewWriterSize(f, 64*1024)
		}
		input, e := evidenceReader(sourcePath, true)
		if e != nil {
			closeAll()
			return receipt, e
		}
		reader := csv.NewReader(bufio.NewReaderSize(input, 256*1024))
		reader.FieldsPerRecord = -1
		header, e := reader.Read()
		if e != nil {
			input.Close()
			closeAll()
			return receipt, e
		}
		positions, e := HeaderPositions(header)
		if e != nil {
			input.Close()
			closeAll()
			return receipt, e
		}
		for _, key := range evidenceIndexCols[name] {
			if _, ok := positions[key]; !ok {
				input.Close()
				closeAll()
				return receipt, fmt.Errorf("%s missing %s", name, key)
			}
		}
		var buf bytes.Buffer
		var blockRows int
		var blockID, rows int64
		var writerOffset int64
		flushBlock := func() error {
			if blockRows == 0 {
				return nil
			}
			var zipped bytes.Buffer
			gz, er := gzip.NewWriterLevel(&zipped, gzip.BestSpeed)
			if er != nil {
				return er
			}
			gz.Name = ""
			gz.Comment = ""
			gz.ModTime = time.Unix(0, 0)
			if _, er = gz.Write(buf.Bytes()); er != nil {
				return er
			}
			if er = gz.Close(); er != nil {
				return er
			}
			var blockmeta [24]byte
			binary.LittleEndian.PutUint64(blockmeta[0:8], uint64(writerOffset))
			binary.LittleEndian.PutUint64(blockmeta[8:16], uint64(zipped.Len()))
			binary.LittleEndian.PutUint64(blockmeta[16:24], uint64(buf.Len()))
			if _, er = blocks.Write(blockmeta[:]); er != nil {
				return er
			}
			if _, er = dst.Write(zipped.Bytes()); er != nil {
				return er
			}
			writerOffset += int64(zipped.Len())
			blockID++
			blockRows = 0
			buf.Reset()
			return nil
		}
		for {
			row, er := reader.Read()
			if er == io.EOF {
				break
			}
			if er != nil {
				e = er
				break
			}
			if len(row) != len(header) {
				e = fmt.Errorf("%s invalid number of columns at row %d", name, rows+1)
				break
			}
			if blockRows >= packedTraceRowsPerChunk || buf.Len() >= packedTraceMaxChunkBytes {
				if e = flushBlock(); e != nil {
					break
				}
			}
			if buf.Len() > int(^uint32(0)) {
				e = errors.New("oversized trace chunk")
				break
			}
			offset := uint32(buf.Len())
			cw := csv.NewWriter(&buf)
			if er = cw.Write(row); er != nil {
				e = er
				break
			}
			cw.Flush()
			if er = cw.Error(); er != nil {
				e = er
				break
			}
			key := evidenceKey(name, row, positions)
			sum := sha256.Sum256([]byte(key))
			var entry [packedEntryBytes]byte
			copy(entry[:16], sum[:16])
			binary.LittleEndian.PutUint64(entry[16:24], uint64(blockID))
			binary.LittleEndian.PutUint32(entry[24:28], offset)
			if _, er = shards[packedShard(key)].Write(entry[:]); er != nil {
				e = er
				break
			}
			blockRows++
			rows++
			if buf.Len() > packedTraceMaxChunkBytes {
				if e = flushBlock(); e != nil {
					break
				}
			}
		}
		if e == nil {
			e = flushBlock()
		}
		if err := input.Close(); e == nil {
			e = err
		}
		if err := closeAll(); e == nil {
			e = err
		}
		if e != nil {
			return receipt, fmt.Errorf("build packed trace for %s: %w", name, e)
		}
		chunkSha, chunkSize, e := shaFileAndSize(filepath.Join(dstDir, "chunks.bgz"))
		if e != nil {
			return receipt, e
		}
		blockSha, _, e := shaFileAndSize(filepath.Join(dstDir, "blocks.bin"))
		if e != nil {
			return receipt, e
		}
		info := PackedTraceFile{Source: source, SourceMTime: stat.ModTime().UnixNano(), Header: header, Rows: rows, Blocks: blockID, PackedSHA256: chunkSha, BlocksSHA256: blockSha, PackedSize: chunkSize, Shards: map[string]string{}}
		for i := 0; i < traceShards; i++ {
			shard := fmt.Sprintf("%02d.bin", i)
			sha, _, er := shaFileAndSize(filepath.Join(dstDir, shard))
			if er != nil {
				return receipt, er
			}
			info.Shards[shard] = sha
		}
		receipt.Files[name] = info
	}
	raw, e := json.MarshalIndent(receipt, "", "  ")
	if e != nil {
		return receipt, e
	}
	if e = os.WriteFile(filepath.Join(tmp, "manifest.json"), raw, 0644); e != nil {
		return receipt, e
	}
	if e = os.Rename(tmp, filepath.Join(dir, packedTraceDir)); e != nil {
		return receipt, e
	}
	return receipt, nil
}

func openPackedTraceIndex(dir string) (*packedTraceIndex, error) {
	raw, e := os.ReadFile(filepath.Join(dir, packedTraceDir, "manifest.json"))
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var receipt PackedTraceReceipt
	if e = json.Unmarshal(raw, &receipt); e != nil {
		return nil, e
	}
	if receipt.Format != packedTraceFormat {
		return nil, errors.New("unsupported packed trace version")
	}
	if len(receipt.Files) != 3 {
		return nil, errors.New("incomplete packed trace files")
	}
	for _, name := range []string{"ledger.csv", "lineage_links.csv", "source_to_pool.csv"} {
		meta, ok := receipt.Files[name]
		if !ok || len(meta.Shards) != traceShards || meta.Blocks < 0 || meta.Rows < 0 || meta.Source.StoredName != name+".gz" || !meta.Source.Compressed {
			return nil, fmt.Errorf("invalid packed trace manifest: %s", name)
		}
		st, er := os.Stat(filepath.Join(dir, meta.Source.StoredName))
		if er != nil {
			return nil, er
		}
		if st.Size() != meta.Source.StoredBytes || st.ModTime().UnixNano() != meta.SourceMTime {
			return nil, fmt.Errorf("source evidence modified: %s; rebuild packed index", name)
		}
		st, er = os.Stat(filepath.Join(packedTracePath(dir, name), "chunks.bgz"))
		if er != nil {
			return nil, er
		}
		if st.Size() != meta.PackedSize {
			return nil, fmt.Errorf("packed chunks changed: %s", name)
		}
		blockStat, er := os.Stat(filepath.Join(packedTracePath(dir, name), "blocks.bin"))
		if er != nil {
			return nil, er
		}
		if blockStat.Size() != meta.Blocks*24 {
			return nil, fmt.Errorf("packed block directory changed: %s", name)
		}
	}
	return &packedTraceIndex{base: dir, files: receipt.Files}, nil
}

// VerifyPackedTraceIndex does a complete SHA-256 check of source gzip streams,
// independent gzip chunks, binary block maps and hashed-key shards.
func VerifyPackedTraceIndex(dir string) error {
	idx, e := openPackedTraceIndex(dir)
	if e != nil {
		return e
	}
	if idx == nil {
		return errors.New("packed trace index not found")
	}
	if _, e = VerifyEvidenceStream(dir); e != nil {
		return e
	}
	for name, meta := range idx.files {
		part := packedTracePath(dir, name)
		for file, want := range map[string]string{"chunks.bgz": meta.PackedSHA256, "blocks.bin": meta.BlocksSHA256} {
			got, _, er := shaFileAndSize(filepath.Join(part, file))
			if er != nil {
				return er
			}
			if got != want {
				return fmt.Errorf("packed %s integrity mismatch: %s", name, file)
			}
		}
		for shard, want := range meta.Shards {
			got, _, er := shaFileAndSize(filepath.Join(part, shard))
			if er != nil {
				return er
			}
			if got != want {
				return fmt.Errorf("packed index shard mismatch: %s/%s", name, shard)
			}
		}
	}
	return nil
}

// scanPackedEvidence looks up only selected keys. Every matching candidate is
// verified against its full CSV key; truncated, corrupt or wrong chunks fail.
func (idx *packedTraceIndex) scan(file string, keys map[string]bool, fields []string, _ func([]string) string, fn func([]string) error) error {
	meta, ok := idx.files[file]
	if !ok {
		return fmt.Errorf("packed source missing: %s", file)
	}
	if len(keys) == 0 {
		return nil
	}
	pos, e := HeaderPositions(meta.Header)
	if e != nil {
		return e
	}
	col := make([]int, len(fields))
	for i, f := range fields {
		p, ok := pos[f]
		if !ok {
			return fmt.Errorf("missing trace field %s in %s", f, file)
		}
		col[i] = p
	}
	base := packedTracePath(idx.base, file)
	blocks, e := os.Open(filepath.Join(base, "blocks.bin"))
	if e != nil {
		return e
	}
	defer blocks.Close()
	chunks, e := os.Open(filepath.Join(base, "chunks.bgz"))
	if e != nil {
		return e
	}
	defer chunks.Close()
	shardKeys := map[int]map[string]bool{}
	for k := range keys {
		n := packedShard(k)
		if shardKeys[n] == nil {
			shardKeys[n] = map[string]bool{}
		}
		shardKeys[n][k] = true
	}
	shardNums := make([]int, 0, len(shardKeys))
	for n := range shardKeys {
		shardNums = append(shardNums, n)
	}
	sort.Ints(shardNums)
	// One decompressed chunk cached per query shard, bounded by 1 MiB + one row.
	for _, n := range shardNums {
		shardName := fmt.Sprintf("%02d.bin", n)
		raw, er := os.ReadFile(filepath.Join(base, shardName))
		if er != nil {
			return er
		}
		if len(raw)%packedEntryBytes != 0 {
			return fmt.Errorf("damaged trace index shard: %s", shardName)
		}
		sha := sha256.Sum256(raw)
		if hex.EncodeToString(sha[:]) != meta.Shards[shardName] {
			return fmt.Errorf("trace shard checksum mismatch: %s", shardName)
		}
		hashes := map[[16]byte][]string{}
		for key := range shardKeys[n] {
			sum := sha256.Sum256([]byte(key))
			var dig [16]byte
			copy(dig[:], sum[:16])
			hashes[dig] = append(hashes[dig], key)
		}
		lastBlock := uint64(^uint64(0))
		var plain []byte
		for p := 0; p < len(raw); p += packedEntryBytes {
			var dig [16]byte
			copy(dig[:], raw[p:p+16])
			matching, ok := hashes[dig]
			if !ok {
				continue
			}
			bid := binary.LittleEndian.Uint64(raw[p+16 : p+24])
			off := binary.LittleEndian.Uint32(raw[p+24 : p+28])
			if bid >= uint64(meta.Blocks) {
				return errors.New("invalid trace block id")
			}
			if bid != lastBlock {
				var entry [24]byte
				if _, er = blocks.ReadAt(entry[:], int64(bid)*24); er != nil {
					return er
				}
				start := binary.LittleEndian.Uint64(entry[0:8])
				compressedSize := binary.LittleEndian.Uint64(entry[8:16])
				plainSize := binary.LittleEndian.Uint64(entry[16:24])
				if compressedSize == 0 || compressedSize > 64*1024*1024 || plainSize > packedTraceMaxChunkBytes+4*1024*1024 || start > uint64(meta.PackedSize) || compressedSize > uint64(meta.PackedSize)-start {
					return errors.New("invalid trace block bounds")
				}
				gz, er := gzip.NewReader(io.NewSectionReader(chunks, int64(start), int64(compressedSize)))
				if er != nil {
					return er
				}
				plain, er = io.ReadAll(io.LimitReader(gz, int64(plainSize)+1))
				if er == nil && int64(len(plain)) != int64(plainSize) {
					er = errors.New("uncompressed chunk length mismatch")
				}
				if cerr := gz.Close(); er == nil {
					er = cerr
				}
				if er != nil {
					return er
				}
				lastBlock = bid
			}
			if int(off) >= len(plain) {
				return errors.New("invalid row offset in compressed chunk")
			}
			cr := csv.NewReader(bytes.NewReader(plain[int(off):]))
			cr.FieldsPerRecord = -1
			rec, er := cr.Read()
			if er != nil {
				return er
			}
			if len(rec) != len(meta.Header) {
				return errors.New("packed record field mismatch")
			}
			trueKey := evidenceKey(file, rec, pos)
			valid := false
			for _, k := range matching {
				if k == trueKey {
					valid = true
					break
				}
			}
			if !valid {
				return errors.New("packed index key mismatch")
			}
			selected := make([]string, len(fields))
			for j, p := range col {
				selected[j] = rec[p]
			}
			if er = fn(selected); er != nil {
				return er
			}
		}
	}
	return nil
}
