// Package vault adds an optional, content-addressed lifecycle store for
// completed allocation runs. It never changes the calculation or evidence data.
// One-process-at-a-time maintenance is enforced by a filesystem directory lock.
package vault

import (
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const Format = "allocation-core/vault-v20/1"

var outputFiles = []string{"results.csv", "ledger.csv", "ledger.csv.gz", "reconciliation.csv", "source_to_pool.csv", "source_to_pool.csv.gz", "lineage_links.csv", "lineage_links.csv.gz", "run_summary.json", "evidence_stream_manifest.json", "comparison.csv"}

type FileRef struct {
	SHA256      string `json:"sha256"`
	Bytes       int64  `json:"bytes"`
	StoredBytes int64  `json:"stored_bytes"`
	Encoding    string `json:"encoding"`
}
type Record struct {
	Format       string             `json:"format"`
	ID           string             `json:"id"`
	Scenario     string             `json:"scenario"`
	Version      string             `json:"version"`
	Class        string             `json:"class"`
	CreatedUTC   string             `json:"created_utc"`
	Files        map[string]FileRef `json:"files"`
	InputAmount  string             `json:"input_amount"`
	OutputAmount string             `json:"output_amount"`
}
type Stats struct {
	Runs                     int   `json:"runs"`
	Official                 int   `json:"official_runs"`
	Draft                    int   `json:"draft_runs"`
	UniqueObjects            int   `json:"unique_objects"`
	LogicalBytes             int64 `json:"logical_bytes"`
	StoredBytes              int64 `json:"physical_bytes"`
	DedupAndCompressionSaved int64 `json:"saved_bytes"`
}
type PrunePlan struct {
	DryRun            bool     `json:"dry_run"`
	RemoveIDs         []string `json:"remove_ids"`
	ProtectedOfficial int      `json:"protected_official"`
	ProtectedRecent   int      `json:"protected_recent_drafts"`
	FreedUniqueBytes  int64    `json:"reclaimable_bytes"`
	RemainingRuns     int      `json:"remaining_runs"`
}

func validLabel(s string) bool {
	if len(s) == 0 || len(s) > 100 || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if r < ' ' || r == '/' || r == '\\' || r == ':' || r == '\x7f' {
			return false
		}
	}
	return true
}
func rootPaths(root string) (string, string) {
	return filepath.Join(root, "objects"), filepath.Join(root, "runs")
}
func objectPath(root string, ref FileRef) string {
	ext := ".gz"
	if ref.Encoding == "raw" {
		ext = ".raw"
	}
	return filepath.Join(root, "objects", ref.SHA256[:2], ref.SHA256+ext)
}
func lock(root string) (func(), error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	p := filepath.Join(root, ".maintenance.lock")
	if err := os.Mkdir(p, 0700); err != nil {
		return nil, fmt.Errorf("vault busy or previous operation interrupted: %w (lock: %s)", err, p)
	}
	return func() { _ = os.Remove(p) }, nil
}
func atomicJSON(path string, value any) error {
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".write-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func hashFile(path string) (string, int64, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func randomID() (string, error) {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return time.Now().UTC().Format("20060102T150405Z") + "_" + hex.EncodeToString(b), nil
}
func isDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, v := range s {
		if !strings.ContainsRune("0123456789abcdef", v) {
			return false
		}
	}
	return true
}
func validRef(f FileRef) bool {
	return isDigest(f.SHA256) && f.Bytes >= 0 && f.StoredBytes >= 0 && (f.Encoding == "gzip" || f.Encoding == "raw")
}
func getRecord(root, id string) (Record, error) {
	var r Record
	if !validLabel(id) || strings.ContainsAny(id, " .") {
		return r, fmt.Errorf("bad run ID")
	}
	b, e := os.ReadFile(filepath.Join(root, "runs", id+".json"))
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return r, e
	}
	if r.ID != id || r.Format != Format || !validLabel(r.Scenario) || !validLabel(r.Version) || (r.Class != "official" && r.Class != "draft") {
		return r, fmt.Errorf("invalid run metadata %s", id)
	}
	allowed := map[string]bool{}
	for _, n := range outputFiles {
		allowed[n] = true
	}
	for name, ref := range r.Files {
		if !allowed[name] || !validRef(ref) {
			return r, fmt.Errorf("invalid stored file %s", name)
		}
	}
	return r, nil
}
func List(root string) ([]Record, error) {
	ents, e := os.ReadDir(filepath.Join(root, "runs"))
	if os.IsNotExist(e) {
		return []Record{}, nil
	}
	if e != nil {
		return nil, e
	}
	out := []Record{}
	for _, entry := range ents {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		r, e := getRecord(root, strings.TrimSuffix(entry.Name(), ".json"))
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedUTC == out[j].CreatedUTC {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedUTC > out[j].CreatedUTC
	})
	return out, nil
}
func loadValidatedSuccess(input string) (map[string]any, error) {
	path := filepath.Join(input, "run_summary.json")
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, fmt.Errorf("run_summary.json required: %w", e)
	}
	var s map[string]any
	if e = json.Unmarshal(b, &s); e != nil {
		return nil, e
	}
	if s["status"] != "PASS" {
		return nil, fmt.Errorf("only PASS runs may be registered")
	}
	return s, nil
}

// copyObject streams at most a small fixed buffer; objects are immutable and
// identified by SHA-256 of the exact original bytes, including any UTF-8 BOM.
func copyObject(root, path string, compressedInput bool) (FileRef, error) {
	var ref FileRef
	f, e := os.Open(path)
	if e != nil {
		return ref, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return ref, e
	}
	if !info.Mode().IsRegular() {
		return ref, fmt.Errorf("nonregular input rejected: %s", path)
	}
	h := sha256.New()
	n, e := io.Copy(h, f)
	if e != nil {
		return ref, e
	}
	ref.SHA256 = hex.EncodeToString(h.Sum(nil))
	ref.Bytes = n
	ref.Encoding = "gzip"
	if compressedInput {
		ref.Encoding = "raw"
	}
	dest := objectPath(root, ref)
	if info, e = os.Stat(dest); e == nil {
		if !info.Mode().IsRegular() {
			return ref, fmt.Errorf("unsafe existing blob")
		}
		ref.StoredBytes = info.Size()
		if e := verifyBlob(dest, ref, nil); e != nil {
			return ref, fmt.Errorf("reused object corrupted: %w", e)
		}
		return ref, nil
	}
	if !os.IsNotExist(e) {
		return ref, e
	}
	if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		return ref, e
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return ref, e
	}
	tmp, e := os.CreateTemp(filepath.Dir(dest), ".blob-")
	if e != nil {
		return ref, e
	}
	defer os.Remove(tmp.Name())
	var writer io.Writer = tmp
	var gz *gzip.Writer
	if ref.Encoding == "gzip" {
		gz, e = gzip.NewWriterLevel(tmp, gzip.BestSpeed)
		if e != nil {
			tmp.Close()
			return ref, e
		}
		writer = gz
	}
	actualHash := sha256.New()
	actualBytes, copyErr := io.Copy(writer, io.TeeReader(f, actualHash))
	if gz != nil {
		if ce := gz.Close(); copyErr == nil {
			copyErr = ce
		}
	}
	if copyErr == nil {
		copyErr = tmp.Sync()
	}
	if ce := tmp.Close(); copyErr == nil {
		copyErr = ce
	}
	if copyErr != nil {
		return ref, copyErr
	}
	if actualBytes != ref.Bytes || hex.EncodeToString(actualHash.Sum(nil)) != ref.SHA256 {
		return ref, fmt.Errorf("input modified during registration: %s", path)
	}
	if e = os.Rename(tmp.Name(), dest); e != nil {
		return ref, e
	}
	stat, e := os.Stat(dest)
	if e != nil {
		return ref, e
	}
	ref.StoredBytes = stat.Size()
	return ref, nil
}
func Register(root, scenario, version, class, input string) (Record, error) {
	var r Record
	if !validLabel(scenario) || !validLabel(version) {
		return r, fmt.Errorf("scenario/version must be nonempty safe labels (max 100 chars)")
	}
	if class != "draft" && class != "official" {
		return r, fmt.Errorf("class must be draft or official")
	}
	unlock, e := lock(root)
	if e != nil {
		return r, e
	}
	defer unlock()
	if _, e = os.Stat(filepath.Join(input, "results.csv")); e != nil {
		return r, fmt.Errorf("results.csv required: %w", e)
	}
	if _, e = os.Stat(filepath.Join(input, "reconciliation.csv")); e != nil {
		return r, fmt.Errorf("reconciliation.csv required: %w", e)
	}
	s, e := loadValidatedSuccess(input)
	if e != nil {
		return r, e
	}
	if e = checkReconciliation(filepath.Join(input, "reconciliation.csv")); e != nil {
		return r, e
	}
	// In compressed mode verify the whole stream before accepting an archive.
	if _, e = os.Stat(filepath.Join(input, "evidence_stream_manifest.json")); e == nil {
		if _, e = engineVerify(input); e != nil {
			return r, e
		}
	}
	id, e := randomID()
	if e != nil {
		return r, e
	}
	r = Record{Format: Format, ID: id, Scenario: scenario, Version: version, Class: class, CreatedUTC: time.Now().UTC().Format(time.RFC3339Nano), Files: map[string]FileRef{}}
	if v, ok := s["input_amount"].(string); ok {
		r.InputAmount = v
	}
	if v, ok := s["output_amount"].(string); ok {
		r.OutputAmount = v
	}
	for _, name := range outputFiles {
		path := filepath.Join(input, name)
		info, e := os.Lstat(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return r, e
		}
		if !info.Mode().IsRegular() {
			return r, fmt.Errorf("only regular files allowed: %s", name)
		}
		ref, e := copyObject(root, path, strings.HasSuffix(name, ".gz"))
		if e != nil {
			return r, e
		}
		r.Files[name] = ref
	}
	if _, ok := r.Files["ledger.csv"]; !ok {
		if _, ok = r.Files["ledger.csv.gz"]; !ok {
			return r, errors.New("ledger evidence required")
		}
	}
	// The mandatory summary, result and reconciliation ensure this is a complete run.
	if e = atomicJSON(filepath.Join(root, "runs", id+".json"), r); e != nil {
		return r, e
	}
	return r, nil
}

// engineVerify is supplied by an adapter file so this package can validate compressed-evidence manifests.
func verifyBlob(path string, ref FileRef, output io.Writer) error {
	if !validRef(ref) {
		return errors.New("invalid ref")
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	if fi, e := f.Stat(); e != nil || fi.Size() != ref.StoredBytes {
		return fmt.Errorf("stored blob size differs: %s", path)
	}
	var reader io.Reader = f
	var gz *gzip.Reader
	if ref.Encoding == "gzip" {
		gz, e = gzip.NewReader(f)
		if e != nil {
			return e
		}
		defer gz.Close()
		reader = gz
	}
	h := sha256.New()
	dest := io.Writer(h)
	if output != nil {
		dest = io.MultiWriter(h, output)
	}
	n, e := io.Copy(dest, io.LimitReader(reader, ref.Bytes+1))
	if e != nil {
		return e
	}
	if n != ref.Bytes || hex.EncodeToString(h.Sum(nil)) != ref.SHA256 {
		return fmt.Errorf("blob hash or size mismatch: %s", path)
	}
	if gz != nil {
		buf := make([]byte, 1)
		if count, ee := gz.Read(buf); count != 0 || ee != io.EOF {
			return fmt.Errorf("trailing uncompressed data: %s", path)
		}
	}
	return nil
}
func Verify(root, id string) error {
	r, e := getRecord(root, id)
	if e != nil {
		return e
	}
	for name, ref := range r.Files {
		if e = verifyBlob(objectPath(root, ref), ref, nil); e != nil {
			return fmt.Errorf("%s: %w", name, e)
		}
	}
	return nil
}
func Restore(root, id, destination string) error {
	r, e := getRecord(root, id)
	if e != nil {
		return e
	}
	if _, e = os.Stat(destination); e == nil {
		return fmt.Errorf("restore destination already exists")
	}
	if !os.IsNotExist(e) {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(destination), 0700); e != nil {
		return e
	}
	tmp, e := os.MkdirTemp(filepath.Dir(destination), ".vault-restore-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	for name, ref := range r.Files {
		out, e := os.OpenFile(filepath.Join(tmp, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		e = verifyBlob(objectPath(root, ref), ref, out)
		if e == nil {
			e = out.Sync()
		}
		if ce := out.Close(); e == nil {
			e = ce
		}
		if e != nil {
			return fmt.Errorf("restore %s: %w", name, e)
		}
	}
	return os.Rename(tmp, destination)
}
func StatsFor(root string) (Stats, error) {
	runs, e := List(root)
	if e != nil {
		return Stats{}, e
	}
	st := Stats{Runs: len(runs)}
	used := map[string]FileRef{}
	for _, r := range runs {
		if r.Class == "official" {
			st.Official++
		} else {
			st.Draft++
		}
		for _, ref := range r.Files {
			st.LogicalBytes += ref.Bytes
			used[objectPath(root, ref)] = ref
		}
	}
	st.UniqueObjects = len(used)
	for _, ref := range used {
		st.StoredBytes += ref.StoredBytes
	}
	st.DedupAndCompressionSaved = st.LogicalBytes - st.StoredBytes
	return st, nil
}
func Prune(root string, keepDrafts int, olderThanDays int, apply bool) (PrunePlan, error) {
	var plan PrunePlan
	if keepDrafts < 0 || olderThanDays < 0 {
		return plan, fmt.Errorf("retention limits must be nonnegative")
	}
	unlock, e := lock(root)
	if e != nil {
		return plan, e
	}
	defer unlock()
	runs, e := List(root)
	if e != nil {
		return plan, e
	}
	plan.DryRun = !apply
	group := map[string]int{}
	cutoff := time.Now().UTC().Add(-time.Duration(olderThanDays) * 24 * time.Hour)
	keepRuns := []Record{}
	removeRuns := []Record{}
	for _, r := range runs {
		if r.Class == "official" {
			plan.ProtectedOfficial++
			keepRuns = append(keepRuns, r)
			continue
		}
		group[r.Scenario]++
		date, e := time.Parse(time.RFC3339Nano, r.CreatedUTC)
		if e != nil {
			return plan, e
		}
		if group[r.Scenario] <= keepDrafts || date.After(cutoff) {
			plan.ProtectedRecent++
			keepRuns = append(keepRuns, r)
			continue
		}
		removeRuns = append(removeRuns, r)
		plan.RemoveIDs = append(plan.RemoveIDs, r.ID)
	}
	keepPaths := map[string]bool{}
	removeRefs := map[string]FileRef{}
	for _, r := range keepRuns {
		for _, ref := range r.Files {
			keepPaths[objectPath(root, ref)] = true
		}
	}
	for _, r := range removeRuns {
		for _, ref := range r.Files {
			removeRefs[objectPath(root, ref)] = ref
		}
	}
	for path, ref := range removeRefs {
		if !keepPaths[path] {
			plan.FreedUniqueBytes += ref.StoredBytes
		}
	}
	plan.RemainingRuns = len(keepRuns)
	if apply {
		for _, r := range removeRuns {
			if e = os.Remove(filepath.Join(root, "runs", r.ID+".json")); e != nil {
				return plan, e
			}
		}
		for path := range removeRefs {
			if !keepPaths[path] {
				if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
					return plan, e
				}
			}
		}
	}
	return plan, nil
}

// GarbageCollect reclaims orphan objects left behind by a crash during
// registration or interrupted pruning. It NEVER removes blobs referenced by
// any current run record. Dry-run is always the default.
type GCPlan struct {
	DryRun           bool     `json:"dry_run"`
	OrphanObjects    []string `json:"orphan_objects"`
	ReclaimableBytes int64    `json:"reclaimable_bytes"`
}

func GarbageCollect(root string, apply bool) (GCPlan, error) {
	plan := GCPlan{DryRun: !apply, OrphanObjects: []string{}}
	unlock, e := lock(root)
	if e != nil {
		return plan, e
	}
	defer unlock()
	records, e := List(root)
	if e != nil {
		return plan, e
	}
	used := map[string]bool{}
	for _, r := range records {
		for _, ref := range r.Files {
			used[objectPath(root, ref)] = true
		}
	}
	base := filepath.Join(root, "objects")
	dirs, e := os.ReadDir(base)
	if os.IsNotExist(e) {
		return plan, nil
	}
	if e != nil {
		return plan, e
	}
	for _, dir := range dirs {
		if !dir.IsDir() || len(dir.Name()) != 2 {
			continue
		}
		entries, e := os.ReadDir(filepath.Join(base, dir.Name()))
		if e != nil {
			return plan, e
		}
		for _, entry := range entries {
			p := filepath.Join(base, dir.Name(), entry.Name())
			if used[p] {
				continue
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			name := entry.Name()
			parts := strings.Split(name, ".")
			if len(parts) != 2 || !isDigest(parts[0]) || (parts[1] != "gz" && parts[1] != "raw") || parts[0][:2] != dir.Name() {
				continue
			}
			info, e := entry.Info()
			if e != nil {
				return plan, e
			}
			plan.OrphanObjects = append(plan.OrphanObjects, p)
			plan.ReclaimableBytes += info.Size()
		}
	}
	sort.Strings(plan.OrphanObjects)
	if apply {
		for _, p := range plan.OrphanObjects {
			if e = os.Remove(p); e != nil && !os.IsNotExist(e) {
				return plan, e
			}
		}
	}
	return plan, nil
}

// ReleaseSource only deletes exact, hash-matched output files already stored
// in the vault. It never recursively deletes a directory or touches inputs.
// This is NOT a default action. A caller must explicitly pass --apply.
type ReleasePlan struct {
	DryRun           bool     `json:"dry_run"`
	Files            []string `json:"files"`
	ReclaimableBytes int64    `json:"reclaimable_bytes"`
}

func ReleaseSource(root, id, input string, apply bool) (ReleasePlan, error) {
	plan := ReleasePlan{DryRun: !apply, Files: []string{}}
	unlock, e := lock(root)
	if e != nil {
		return plan, e
	}
	defer unlock()
	r, e := getRecord(root, id)
	if e != nil {
		return plan, e
	}
	if e = Verify(root, id); e != nil {
		return plan, e
	}
	rootAbs, e := filepath.Abs(root)
	if e != nil {
		return plan, e
	}
	inAbs, e := filepath.Abs(input)
	if e != nil {
		return plan, e
	}
	if inAbs == rootAbs || strings.HasPrefix(inAbs, rootAbs+string(filepath.Separator)) {
		return plan, fmt.Errorf("source must not be inside vault")
	}
	// Prevalidate EVERY source before deleting any; do not ever remove an input file.
	for name, ref := range r.Files {
		if name == "run_summary.json" {
			continue
		}
		path := filepath.Join(input, name)
		info, e := os.Lstat(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return plan, e
		}
		if !info.Mode().IsRegular() {
			return plan, fmt.Errorf("unsafe source %s", name)
		}
		sha, n, e := hashFile(path)
		if e != nil {
			return plan, e
		}
		if sha != ref.SHA256 || n != ref.Bytes {
			return plan, fmt.Errorf("refusing cleanup: source differs from archived file %s", name)
		}
		plan.Files = append(plan.Files, name)
		plan.ReclaimableBytes += n
	}
	sort.Strings(plan.Files)
	if apply {
		for _, name := range plan.Files {
			if e = os.Remove(filepath.Join(input, name)); e != nil {
				return plan, e
			}
		}
	}
	return plan, nil
}

// checkReconciliation rejects a run that claims PASS in its summary but
// contains a failed amount pool. It never treats a total-only match as enough.
func checkReconciliation(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	reader := csv.NewReader(f)
	header, e := reader.Read()
	if e != nil {
		return e
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	col := map[string]int{}
	for i, h := range header {
		if _, ok := col[h]; ok || h == "" {
			return fmt.Errorf("reconciliation duplicate/empty header")
		}
		col[h] = i
	}
	status, ok := col["status"]
	if !ok {
		return fmt.Errorf("reconciliation missing status column")
	}
	delta, hasDelta := col["delta"]
	rows := 0
	for {
		row, ee := reader.Read()
		if ee == io.EOF {
			break
		}
		if ee != nil {
			return ee
		}
		rows++
		if row[status] != "PASS" {
			return fmt.Errorf("failed pool in reconciliation row %d: %s", rows, row[status])
		}
		if hasDelta {
			v, ok := new(big.Rat).SetString(row[delta])
			if !ok || v.Sign() != 0 {
				return fmt.Errorf("nonzero or invalid delta at reconciliation row %d", rows)
			}
		}
	}
	if rows == 0 {
		return fmt.Errorf("empty reconciliation")
	}
	return nil
}
