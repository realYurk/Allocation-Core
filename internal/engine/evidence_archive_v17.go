package engine

// Audit archive preserves the original UTF-8 CSV bytes. There is no lossy
// summarization, sampled ledger, or change to allocation math. The source CSV
// is deliberately retained; archive deletion is always the user's decision.

import (
	"archive/zip"
	"compress/flate"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const auditPackFormat = "allocation-core/audit-pack-v17/1"
const auditManifestName = "AUDIT_MANIFEST.json"

var auditFiles = []string{"results.csv", "ledger.csv", "reconciliation.csv", "source_to_pool.csv", "lineage_links.csv", "run_summary.json"}

type AuditEntry struct {
	Size   int64  `json:"original_bytes"`
	SHA256 string `json:"sha256"`
}
type AuditManifest struct {
	Format          string                `json:"format"`
	CreatedUTC      string                `json:"created_utc"`
	Files           map[string]AuditEntry `json:"files"`
	OriginalBytes   int64                 `json:"original_total_bytes"`
	CompressedBytes int64                 `json:"archive_bytes,omitempty"`
}

func PackEvidence(inputDir, archive string) (manifest AuditManifest, err error) {
	manifest = AuditManifest{Format: auditPackFormat, CreatedUTC: time.Now().UTC().Format(time.RFC3339), Files: map[string]AuditEntry{}}
	if _, e := os.Stat(archive); e == nil {
		return manifest, fmt.Errorf("archive already exists: %s", archive)
	} else if !os.IsNotExist(e) {
		return manifest, e
	}
	if e := os.MkdirAll(filepath.Dir(archive), 0755); e != nil {
		return manifest, e
	}
	temp := archive + ".incomplete"
	f, e := os.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if e != nil {
		return manifest, e
	}
	defer func() {
		if err != nil {
			f.Close()
			_ = os.Remove(temp)
		}
	}()
	z := zip.NewWriter(f)
	z.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, flate.BestSpeed) })
	for _, name := range auditFiles {
		path := filepath.Join(inputDir, name)
		in, ee := os.Open(path)
		if os.IsNotExist(ee) && name == "run_summary.json" {
			continue
		}
		if ee != nil {
			return manifest, ee
		}
		info, ee := in.Stat()
		if ee != nil {
			in.Close()
			return manifest, ee
		}
		w, ee := z.Create(name)
		if ee != nil {
			in.Close()
			return manifest, ee
		}
		h := sha256.New()
		n, ee := io.Copy(w, io.TeeReader(in, h))
		_ = in.Close()
		if ee != nil {
			return manifest, ee
		}
		if n != info.Size() {
			return manifest, fmt.Errorf("%s changed while archiving", name)
		}
		manifest.Files[name] = AuditEntry{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
		manifest.OriginalBytes += n
	}
	b, e := json.MarshalIndent(manifest, "", "  ")
	if e != nil {
		return manifest, e
	}
	w, e := z.Create(auditManifestName)
	if e != nil {
		return manifest, e
	}
	if _, e = w.Write(b); e != nil {
		return manifest, e
	}
	if e = z.Close(); e != nil {
		return manifest, e
	}
	if e = f.Sync(); e != nil {
		return manifest, e
	}
	if e = f.Close(); e != nil {
		return manifest, e
	}
	if e = os.Rename(temp, archive); e != nil {
		return manifest, e
	}
	stat, e := os.Stat(archive)
	if e != nil {
		return manifest, e
	}
	manifest.CompressedBytes = stat.Size()
	return manifest, nil
}

func VerifyEvidencePack(archive string) (AuditManifest, error) {
	var manifest AuditManifest
	zr, e := zip.OpenReader(archive)
	if e != nil {
		return manifest, e
	}
	defer zr.Close()
	entries := map[string]*zip.File{}
	for _, f := range zr.File {
		if filepath.Base(f.Name) != f.Name {
			return manifest, fmt.Errorf("invalid archive path %q", f.Name)
		}
		if _, dup := entries[f.Name]; dup {
			return manifest, fmt.Errorf("duplicate archive file %q", f.Name)
		}
		entries[f.Name] = f
	}
	mf := entries[auditManifestName]
	if mf == nil {
		return manifest, fmt.Errorf("missing audit manifest")
	}
	in, e := mf.Open()
	if e != nil {
		return manifest, e
	}
	dec := json.NewDecoder(io.LimitReader(in, 1<<20))
	if e = dec.Decode(&manifest); e != nil {
		in.Close()
		return manifest, e
	}
	in.Close()
	if manifest.Format != auditPackFormat {
		return manifest, fmt.Errorf("unsupported archive format")
	}
	permitted := map[string]bool{}
	for _, name := range auditFiles {
		permitted[name] = true
	}
	var total int64
	for _, name := range auditFiles {
		if name == "run_summary.json" {
			continue
		}
		if _, ok := manifest.Files[name]; !ok {
			return manifest, fmt.Errorf("manifest missing %s", name)
		}
	}
	for name, entry := range manifest.Files {
		if !permitted[name] {
			return manifest, fmt.Errorf("unexpected manifest entry %s", name)
		}
		f := entries[name]
		if f == nil {
			return manifest, fmt.Errorf("archive missing %s", name)
		}
		if entry.Size < 0 {
			return manifest, fmt.Errorf("negative size")
		}
		rc, ee := f.Open()
		if ee != nil {
			return manifest, ee
		}
		h := sha256.New()
		n, ee := io.Copy(h, io.LimitReader(rc, entry.Size+1))
		_ = rc.Close()
		if ee != nil {
			return manifest, ee
		}
		if n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
			return manifest, fmt.Errorf("archive evidence altered: %s", name)
		}
		total += n
	}
	if total != manifest.OriginalBytes {
		return manifest, fmt.Errorf("archive total size mismatch")
	}
	if len(entries) != len(manifest.Files)+1 {
		return manifest, fmt.Errorf("unexpected extra files in archive")
	}
	return manifest, nil
}

func ExtractEvidencePack(archive, outputDir string) error {
	manifest, e := VerifyEvidencePack(archive)
	if e != nil {
		return e
	}
	if _, e = os.Stat(outputDir); e == nil {
		return fmt.Errorf("output directory exists: %s", outputDir)
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(outputDir), 0755); e != nil {
		return e
	}
	tmp, e := os.MkdirTemp(filepath.Dir(outputDir), ".audit-extract-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	zr, e := zip.OpenReader(archive)
	if e != nil {
		return e
	}
	defer zr.Close()
	names := make([]string, 0, len(manifest.Files))
	for k := range manifest.Files {
		names = append(names, k)
	}
	sort.Strings(names)
	items := map[string]*zip.File{}
	for _, f := range zr.File {
		items[f.Name] = f
	}
	for _, name := range names {
		in, er := items[name].Open()
		if er != nil {
			return er
		}
		out, er := os.Create(filepath.Join(tmp, name))
		if er != nil {
			in.Close()
			return er
		}
		_, er = io.Copy(out, in)
		if er == nil {
			er = out.Sync()
		}
		_ = out.Close()
		_ = in.Close()
		if er != nil {
			return er
		}
	}
	return os.Rename(tmp, outputDir)
}
