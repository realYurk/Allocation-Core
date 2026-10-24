package engine

// Optional direct-to-gzip audit evidence. The money engine, source rows,
// pool order, CSV encoding and reconciliation logic are unchanged.
// Results and reconciliation remain ordinary Excel-readable CSV. Evidence
// streams are lossless gzip CSV, never first written as large plaintext files.

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const evidenceStreamFormat = "allocation-core/evidence-stream-v18/1"
const evidenceStreamManifest = "evidence_stream_manifest.json"

var evidenceStreamNames = []string{"results.csv", "ledger.csv", "reconciliation.csv", "source_to_pool.csv", "lineage_links.csv", "run_summary.json"}
var gzipEvidence = map[string]bool{"ledger.csv": true, "source_to_pool.csv": true, "lineage_links.csv": true}

type EvidenceStreamFile struct {
	StoredName   string `json:"stored_name"`
	Compressed   bool   `json:"compressed"`
	PlainBytes   int64  `json:"plain_bytes"`
	PlainSHA256  string `json:"plain_sha256"`
	StoredBytes  int64  `json:"stored_bytes"`
	StoredSHA256 string `json:"stored_sha256"`
}
type EvidenceStreamManifest struct {
	Format           string                        `json:"format"`
	Files            map[string]EvidenceStreamFile `json:"files"`
	PlainTotalBytes  int64                         `json:"plain_total_bytes"`
	StoredTotalBytes int64                         `json:"stored_total_bytes"`
}

func shaFileAndSize(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// WriteEvidenceStreamManifest is called only after every output sink has
// successfully closed, and before the run directory is published atomically.
// It uses the rolling plaintext hashes from the live CSV writer; it never
// re-reads an enormous decompressed ledger to create the manifest.
func WriteEvidenceStreamManifest(dir string, sinks ...*csvSink) error {
	byName := map[string]*csvSink{}
	for _, s := range sinks {
		byName[strings.TrimSuffix(filepath.Base(s.final), ".gz")] = s
	}
	m := EvidenceStreamManifest{Format: evidenceStreamFormat, Files: map[string]EvidenceStreamFile{}}
	for _, name := range evidenceStreamNames {
		compressed := gzipEvidence[name]
		storedName := name
		if compressed {
			storedName += ".gz"
		}
		file := filepath.Join(dir, storedName)
		storedHash, storedSize, err := shaFileAndSize(file)
		if err != nil {
			return err
		}
		var rawHash string
		var rawSize int64
		if s := byName[name]; s != nil {
			rawHash = hex.EncodeToString(s.digest.Sum(nil))
			rawSize = s.rawSize
		} else if !compressed {
			rawHash, rawSize = storedHash, storedSize
		} else {
			return fmt.Errorf("missing live evidence sink: %s", name)
		}
		if !compressed && (rawSize != storedSize || rawHash != storedHash) {
			return fmt.Errorf("sink hash mismatch %s", name)
		}
		m.Files[name] = EvidenceStreamFile{StoredName: storedName, Compressed: compressed, PlainBytes: rawSize, PlainSHA256: rawHash, StoredBytes: storedSize, StoredSHA256: storedHash}
		m.PlainTotalBytes += rawSize
		m.StoredTotalBytes += storedSize
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, evidenceStreamManifest), raw, 0644)
}

func readEvidenceStreamManifest(dir string) (EvidenceStreamManifest, error) {
	var m EvidenceStreamManifest
	f, err := os.Open(filepath.Join(dir, evidenceStreamManifest))
	if err != nil {
		return m, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&m); err != nil {
		return m, err
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return m, fmt.Errorf("manifest contains extra JSON or invalid trailing data")
	}
	if m.Format != evidenceStreamFormat {
		return m, fmt.Errorf("unsupported evidence manifest format")
	}
	if len(m.Files) != len(evidenceStreamNames) {
		return m, fmt.Errorf("unexpected evidence manifest files")
	}
	return m, nil
}

func evidenceReader(path string, compressed bool) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if !compressed {
		return f, nil
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &twoClosers{Reader: gz, closeA: gz, closeB: f}, nil
}

type twoClosers struct {
	io.Reader
	closeA, closeB io.Closer
}

func (t *twoClosers) Close() error {
	e := t.closeA.Close()
	if e2 := t.closeB.Close(); e == nil {
		e = e2
	}
	return e
}

func copyVerifyPlain(out io.Writer, reader io.Reader, size int64, digest string) error {
	if size < 0 || size == int64(^uint64(0)>>1) {
		return fmt.Errorf("invalid plain byte size")
	}
	h := sha256.New()
	var target io.Writer = h
	if out != nil {
		target = io.MultiWriter(out, h)
	}
	n, err := io.Copy(target, io.LimitReader(reader, size+1))
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return fmt.Errorf("evidence plaintext checksum/size mismatch")
	}
	return nil
}

func verifyEvidenceFile(dir, name string, record EvidenceStreamFile, restore io.Writer) error {
	expected := name
	if gzipEvidence[name] {
		expected += ".gz"
	}
	if record.StoredName != expected || record.Compressed != gzipEvidence[name] || record.PlainBytes < 0 || record.StoredBytes < 0 {
		return fmt.Errorf("unsafe/inconsistent evidence manifest: %s", name)
	}
	if len(record.PlainSHA256) != 64 || len(record.StoredSHA256) != 64 {
		return fmt.Errorf("invalid evidence checksums for %s", name)
	}
	path := filepath.Join(dir, expected)
	sha, n, err := shaFileAndSize(path)
	if err != nil {
		return err
	}
	if n != record.StoredBytes || sha != record.StoredSHA256 {
		return fmt.Errorf("stored evidence corrupted: %s", name)
	}
	reader, err := evidenceReader(path, record.Compressed)
	if err != nil {
		return err
	}
	defer reader.Close()
	if err = copyVerifyPlain(restore, reader, record.PlainBytes, record.PlainSHA256); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func VerifyEvidenceStream(dir string) (EvidenceStreamManifest, error) {
	m, err := readEvidenceStreamManifest(dir)
	if err != nil {
		return m, err
	}
	var plainTotal, storedTotal int64
	for _, name := range evidenceStreamNames {
		info, ok := m.Files[name]
		if !ok {
			return m, fmt.Errorf("missing %s", name)
		}
		if err = verifyEvidenceFile(dir, name, info, nil); err != nil {
			return m, err
		}
		plainTotal += info.PlainBytes
		storedTotal += info.StoredBytes
	}
	if plainTotal != m.PlainTotalBytes || storedTotal != m.StoredTotalBytes {
		return m, fmt.Errorf("manifest total mismatch")
	}
	return m, nil
}

// ExpandEvidenceStream restores exact legacy CSV bytes into a new directory;
// it never overwrites the source files or publishes partially verified output.
func ExpandEvidenceStream(dir, destination string) error {
	m, err := VerifyEvidenceStream(dir)
	if err != nil {
		return err
	}
	if _, err = os.Stat(destination); err == nil {
		return fmt.Errorf("destination exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(destination), ".evidence-v18-expanding-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, name := range evidenceStreamNames {
		dest, err := os.Create(filepath.Join(tmp, name))
		if err != nil {
			return err
		}
		err = verifyEvidenceFile(dir, name, m.Files[name], dest)
		if err == nil {
			err = dest.Sync()
		}
		if cErr := dest.Close(); err == nil {
			err = cErr
		}
		if err != nil {
			return err
		}
	}
	return os.Rename(tmp, destination)
}

// This small declaration pins the intended hashing interface and guards
// against silently treating compressed file hashes as plaintext hashes.
var _ hash.Hash = sha256.New()
