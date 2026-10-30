// Package batchvault publishes an entire successful Allocation Multi run as one
// content-addressed business version. Financial computation is not implemented here.
package batchvault

import (
	"bufio"
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
	"regexp"
	"sort"
	"strings"
	"time"
)

const Format = "allocation-multi/batch-vault/1"

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_.+-]{1,100}$`)
var hexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var groupPattern = regexp.MustCompile(`^[0-9a-f]{20}$`)
var filePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type Blob struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type Record struct {
	Format       string          `json:"format"`
	ID           string          `json:"id"`
	Scenario     string          `json:"scenario"`
	Version      string          `json:"version"`
	CreatedUTC   string          `json:"created_utc"`
	Accounts     int             `json:"accounts"`
	Groups       int             `json:"groups"`
	InputAmount  string          `json:"input_amount"`
	OutputAmount string          `json:"output_amount"`
	ResultRows   int64           `json:"result_rows"`
	Files        map[string]Blob `json:"files"`
}
type Pointer struct {
	Format        string `json:"format"`
	Scenario      string `json:"scenario"`
	Version       string `json:"version"`
	RunID         string `json:"run_id"`
	PreviousRunID string `json:"previous_run_id,omitempty"`
	PublishedUTC  string `json:"published_utc"`
}
type PrunePlan struct {
	DryRun           bool     `json:"dry_run"`
	RemoveIDs        []string `json:"remove_ids"`
	ProtectedCurrent int      `json:"protected_current"`
	ProtectedHistory int      `json:"protected_history"`
	ReclaimableBytes int64    `json:"reclaimable_bytes"`
}

func validKey(s string) bool            { return keyPattern.MatchString(s) && s != "." && s != ".." }
func validID(s string) bool             { return len(s) >= 20 && validKey(s) && !strings.ContainsRune(s, '.') }
func digest(s string) string            { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func objPath(root, sha string) string   { return filepath.Join(root, "objects", sha[:2], sha+".raw") }
func recordPath(root, id string) string { return filepath.Join(root, "runs", id+".json") }
func pointerPath(root, scenario, version string) string {
	return filepath.Join(root, "active", digest(scenario+"\x00"+version)+".json")
}
func withLock(root string) (func(), error) {
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	p := filepath.Join(root, ".batch-maintenance.lock")
	if e := os.Mkdir(p, 0700); e != nil {
		return nil, fmt.Errorf("batch vault locked (inspect before removing stale lock %s): %w", p, e)
	}
	return func() { _ = os.Remove(p) }, nil
}
func atomicJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".atomic-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	if x := f.Close(); e == nil {
		e = x
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func loadJSON(path string, v any) error {
	st, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("not regular file: %s", path)
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func safeRel(rel string) bool {
	if rel == "" || strings.Contains(rel, "\\") || filepath.IsAbs(rel) {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || !filePattern.MatchString(p) {
			return false
		}
	}
	switch {
	case len(parts) == 1:
		return parts[0] == "results.csv" || parts[0] == "account_reconciliation.csv" || parts[0] == "run_summary.json" || parts[0] == "multi_job.json"
	case len(parts) == 2 && parts[0] == "snapshots":
		return strings.HasSuffix(parts[1], ".csv")
	case len(parts) == 2 && parts[0] == "configuration":
		return strings.HasSuffix(parts[1], ".json") || strings.HasSuffix(parts[1], ".csv")
	case len(parts) == 3 && parts[0] == "groups" && groupPattern.MatchString(parts[1]):
		return parts[2] == "rule.json" || parts[2] == "group_amounts.csv"
	case len(parts) == 4 && parts[0] == "groups" && groupPattern.MatchString(parts[1]) && parts[2] == "output":
		return strings.HasSuffix(parts[3], ".csv") || strings.HasSuffix(parts[3], ".csv.gz") || strings.HasSuffix(parts[3], ".json")
	default:
		return false
	}
}
func recordValid(r Record) error {
	if r.Format != Format || !validID(r.ID) || !validKey(r.Scenario) || !validKey(r.Version) || r.Accounts < 1 || r.Groups < 1 || r.ResultRows < 1 || len(r.Files) < 5 {
		return fmt.Errorf("invalid batch record header")
	}
	if _, e := time.Parse(time.RFC3339Nano, r.CreatedUTC); e != nil {
		return e
	}
	for n, b := range r.Files {
		if !safeRel(n) || !hexPattern.MatchString(b.SHA256) || b.Bytes < 0 {
			return fmt.Errorf("invalid archived file: %s", n)
		}
	}
	for _, key := range []string{"results.csv", "account_reconciliation.csv", "run_summary.json"} {
		if _, ok := r.Files[key]; !ok {
			return fmt.Errorf("missing batch output %s", key)
		}
	}
	return nil
}
func readRecord(root, id string) (Record, error) {
	var r Record
	if !validID(id) {
		return r, fmt.Errorf("invalid batch ID")
	}
	if e := loadJSON(recordPath(root, id), &r); e != nil {
		return r, e
	}
	if e := recordValid(r); e != nil {
		return r, e
	}
	if r.ID != id {
		return r, fmt.Errorf("batch ID mismatch")
	}
	return r, nil
}
func List(root string) ([]Record, error) {
	es, e := os.ReadDir(filepath.Join(root, "runs"))
	if os.IsNotExist(e) {
		return []Record{}, nil
	}
	if e != nil {
		return nil, e
	}
	runs := []Record{}
	for _, en := range es {
		if !strings.HasSuffix(en.Name(), ".json") {
			continue
		}
		r, e := readRecord(root, strings.TrimSuffix(en.Name(), ".json"))
		if e != nil {
			return nil, e
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedUTC == runs[j].CreatedUTC {
			return runs[i].ID > runs[j].ID
		}
		return runs[i].CreatedUTC > runs[j].CreatedUTC
	})
	return runs, nil
}
func active(root, scenario, version string) (Pointer, error) {
	var p Pointer
	if !validKey(scenario) || !validKey(version) {
		return p, fmt.Errorf("invalid scenario/version")
	}
	if e := loadJSON(pointerPath(root, scenario, version), &p); e != nil {
		return p, e
	}
	if p.Format != Format || p.Scenario != scenario || p.Version != version || !validID(p.RunID) {
		return p, fmt.Errorf("invalid active pointer")
	}
	if _, e := time.Parse(time.RFC3339Nano, p.PublishedUTC); e != nil {
		return p, e
	}
	r, e := readRecord(root, p.RunID)
	if e != nil {
		return p, e
	}
	if r.Scenario != scenario || r.Version != version {
		return p, fmt.Errorf("active points to wrong scenario/version")
	}
	return p, nil
}
func Active(root, scenario, version string) (Pointer, error) { return active(root, scenario, version) }
func activeIndex(root string) (map[string]Pointer, error) {
	index := map[string]Pointer{}
	es, e := os.ReadDir(filepath.Join(root, "active"))
	if os.IsNotExist(e) {
		return index, nil
	}
	if e != nil {
		return nil, e
	}
	for _, entry := range es {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var p Pointer
		path := filepath.Join(root, "active", entry.Name())
		if e = loadJSON(path, &p); e != nil {
			return nil, e
		}
		if !validKey(p.Scenario) || !validKey(p.Version) || path != pointerPath(root, p.Scenario, p.Version) {
			return nil, fmt.Errorf("noncanonical active pointer %s", path)
		}
		if p, e = active(root, p.Scenario, p.Version); e != nil {
			return nil, e
		}
		index[p.Scenario+"\x00"+p.Version] = p
	}
	return index, nil
}
func store(root, source string) (Blob, error) {
	var ref Blob
	src, e := os.Open(source)
	if e != nil {
		return ref, e
	}
	defer src.Close()
	fi, e := src.Stat()
	if e != nil {
		return ref, e
	}
	if !fi.Mode().IsRegular() {
		return ref, fmt.Errorf("unsafe source: %s", source)
	}
	h := sha256.New()
	tmpDir := filepath.Join(root, "objects", ".staging")
	if e = os.MkdirAll(tmpDir, 0700); e != nil {
		return ref, e
	}
	f, e := os.CreateTemp(tmpDir, ".blob-")
	if e != nil {
		return ref, e
	}
	defer os.Remove(f.Name())
	n, e := io.Copy(io.MultiWriter(h, f), src)
	if e == nil {
		e = f.Sync()
	}
	if x := f.Close(); e == nil {
		e = x
	}
	if e != nil {
		return ref, e
	}
	ref = Blob{SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: n}
	dest := objPath(root, ref.SHA256)
	if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		return ref, e
	}
	if st, err := os.Lstat(dest); err == nil {
		if !st.Mode().IsRegular() || st.Size() != n {
			return ref, fmt.Errorf("existing object invalid %s", dest)
		}
		return ref, nil
	} else if !os.IsNotExist(err) {
		return ref, err
	}
	// Both temporary object and final object live on the same filesystem.
	// A crash can leave an orphan .blob in staging, never a half-written
	// published object. The vault-wide filesystem lock serializes writers.
	if e = os.Rename(f.Name(), dest); e != nil {
		return ref, e
	}
	return ref, nil
}
func digestFile(path string) (string, int64, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func Verify(root, id string) error {
	r, e := readRecord(root, id)
	if e != nil {
		return e
	}
	for name, ref := range r.Files {
		path := objPath(root, ref.SHA256)
		fi, er := os.Lstat(path)
		if er != nil {
			return fmt.Errorf("%s: %w", name, er)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("unsafe object %s", name)
		}
		sha, n, er := digestFile(path)
		if er != nil {
			return er
		}
		if sha != ref.SHA256 || n != ref.Bytes {
			return fmt.Errorf("blob corrupt %s", name)
		}
	}
	return nil
}
func validateRun(run string) (struct {
	Accounts, Groups int
	Input, Output    string
	Rows             int64
}, error) {
	var s struct {
		Status   string `json:"status"`
		Accounts int    `json:"accounts"`
		Groups   int    `json:"groups"`
		Input    string `json:"input_amount"`
		Output   string `json:"output_amount"`
		Rows     int64  `json:"result_rows"`
		Details  []struct {
			Account string `json:"account"`
			Input   string `json:"input_amount"`
			Output  string `json:"output_amount"`
			Status  string `json:"status"`
			Rows    int64  `json:"result_rows"`
		} `json:"details"`
		Engines []struct {
			ID     string `json:"group_id"`
			Status string `json:"status"`
		} `json:"engines"`
	}
	var result struct {
		Accounts, Groups int
		Input, Output    string
		Rows             int64
	}
	if e := loadJSON(filepath.Join(run, "run_summary.json"), &s); e != nil {
		return result, e
	}
	result.Accounts, result.Groups, result.Input, result.Output, result.Rows = s.Accounts, s.Groups, s.Input, s.Output, s.Rows
	if s.Status != "PASS" || s.Accounts < 1 || s.Groups < 1 || s.Rows < 1 || len(s.Details) != s.Accounts || len(s.Engines) != s.Groups {
		return result, fmt.Errorf("only complete PASS Multi runs may be registered")
	}
	in, ok := new(big.Rat).SetString(s.Input)
	if !ok {
		return result, fmt.Errorf("invalid total input")
	}
	out, ok := new(big.Rat).SetString(s.Output)
	if !ok || in.Cmp(out) != 0 {
		return result, fmt.Errorf("global amounts differ")
	}
	sums := map[string]*big.Rat{}
	counts := map[string]int64{}
	total := new(big.Rat)
	for _, d := range s.Details {
		if d.Account == "" || d.Status != "PASS" || d.Rows < 1 || sums[d.Account] != nil {
			return result, fmt.Errorf("invalid/duplicate account detail")
		}
		a, ok := new(big.Rat).SetString(d.Input)
		if !ok {
			return result, fmt.Errorf("bad account amount")
		}
		b, ok := new(big.Rat).SetString(d.Output)
		if !ok || a.Cmp(b) != 0 {
			return result, fmt.Errorf("account not conserved: %s", d.Account)
		}
		sums[d.Account] = new(big.Rat)
		counts[d.Account] = d.Rows
		total.Add(total, a)
	}
	if total.Cmp(in) != 0 {
		return result, fmt.Errorf("account totals mismatch")
	}
	gf, err := os.Open(filepath.Join(run, "results.csv"))
	if err != nil {
		return result, err
	}
	defer gf.Close()
	reader := csv.NewReader(bufio.NewReaderSize(gf, 128<<10))
	hdr, err := reader.Read()
	if err != nil {
		return result, err
	}
	ai, mi := -1, -1
	for i, h := range hdr {
		if h == "account" {
			ai = i
		}
		if h == "amount" {
			mi = i
		}
	}
	if ai < 0 || mi < 0 || mi != len(hdr)-1 {
		return result, fmt.Errorf("invalid results schema")
	}
	var rows int64
	actual := map[string]int64{}
	for {
		rec, e := reader.Read()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return result, e
		}
		v, ok := sums[rec[ai]]
		if !ok {
			return result, fmt.Errorf("unassigned output account %s", rec[ai])
		}
		x, ok := new(big.Rat).SetString(rec[mi])
		if !ok {
			return result, fmt.Errorf("invalid output amount")
		}
		v.Add(v, x)
		actual[rec[ai]]++
		rows++
	}
	if rows != s.Rows {
		return result, fmt.Errorf("results row count mismatch %d != %d", rows, s.Rows)
	}
	for _, d := range s.Details {
		out, _ := new(big.Rat).SetString(d.Output)
		if sums[d.Account].Cmp(out) != 0 || actual[d.Account] != counts[d.Account] {
			return result, fmt.Errorf("account actual amount/rows mismatch: %s", d.Account)
		}
	}
	seen := map[string]bool{}
	for _, g := range s.Engines {
		if !groupPattern.MatchString(g.ID) || g.Status != "PASS" || seen[g.ID] {
			return result, fmt.Errorf("invalid group evidence")
		}
		seen[g.ID] = true
		for _, n := range []string{"rule.json", "group_amounts.csv", "output/run_summary.json", "output/results.csv"} {
			path := filepath.Join(run, "groups", g.ID, filepath.FromSlash(n))
			if fi, e := os.Lstat(path); e != nil || !fi.Mode().IsRegular() {
				return result, fmt.Errorf("missing group evidence %s/%s", g.ID, n)
			}
		}
		var groupSummary struct {
			Status string `json:"status"`
		}
		if err := loadJSON(filepath.Join(run, "groups", g.ID, "output", "run_summary.json"), &groupSummary); err != nil || groupSummary.Status != "PASS" {
			return result, fmt.Errorf("group %s not independently PASS: %v", g.ID, err)
		}
		for _, names := range [][]string{
			{"ledger.csv", "ledger.csv.gz"},
			{"source_to_pool.csv", "source_to_pool.csv.gz"},
			{"lineage_links.csv", "lineage_links.csv.gz"},
		} {
			found := false
			for _, name := range names {
				if fi, err := os.Lstat(filepath.Join(run, "groups", g.ID, "output", name)); err == nil && fi.Mode().IsRegular() {
					found = true
				}
			}
			if !found {
				return result, fmt.Errorf("group %s missing audit evidence %v", g.ID, names)
			}
		}
	}
	return result, nil
}
func Register(root, scenario, version, run string) (Record, error) {
	var r Record
	if !validKey(scenario) || !validKey(version) {
		return r, fmt.Errorf("invalid scenario/version")
	}
	root, e := filepath.Abs(root)
	if e != nil {
		return r, e
	}
	run, e = filepath.Abs(run)
	if e != nil {
		return r, e
	}
	// Forbid either directory being nested inside the other.
	if rel, _ := filepath.Rel(run, root); rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)) {
		return r, fmt.Errorf("vault inside source run")
	}
	if rel, _ := filepath.Rel(root, run); rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)) {
		return r, fmt.Errorf("source inside vault")
	}
	unlock, e := withLock(root)
	if e != nil {
		return r, e
	}
	defer unlock()
	meta, e := validateRun(run)
	if e != nil {
		return r, e
	}
	r = Record{Format: Format, Scenario: scenario, Version: version, CreatedUTC: time.Now().UTC().Format(time.RFC3339Nano), Accounts: meta.Accounts, Groups: meta.Groups, InputAmount: meta.Input, OutputAmount: meta.Output, ResultRows: meta.Rows, Files: map[string]Blob{}}
	nonce := make([]byte, 12)
	if _, e = rand.Read(nonce); e != nil {
		return r, e
	}
	r.ID = time.Now().UTC().Format("20060102T150405Z") + "_" + hex.EncodeToString(nonce)
	var count int
	e = filepath.WalkDir(run, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(run, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		normalized := filepath.ToSlash(rel)
		if entry.IsDir() {
			if normalized == "driver-cache" {
				return filepath.SkipDir
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink directory rejected")
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("symlink or special input rejected: %s", normalized)
		}
		if !safeRel(normalized) {
			return fmt.Errorf("unexpected file in Multi output: %s", normalized)
		}
		count++
		if count > 100000 {
			return fmt.Errorf("too many files")
		}
		ref, err := store(root, path)
		if err != nil {
			return err
		}
		r.Files[normalized] = ref
		return nil
	})
	if e != nil {
		return Record{}, e
	}
	if e = recordValid(r); e != nil {
		return Record{}, e
	}
	for _, g := range []string{"results.csv", "account_reconciliation.csv", "run_summary.json"} {
		if _, ok := r.Files[g]; !ok {
			return Record{}, fmt.Errorf("missing %s", g)
		}
	}
	if e = atomicJSON(recordPath(root, r.ID), r); e != nil {
		return Record{}, e
	}
	return r, nil
}
func Publish(root, scenario, version, id, expected string) (Pointer, error) {
	var p Pointer
	if !validKey(scenario) || !validKey(version) || !(expected == "none" || validID(expected)) {
		return p, fmt.Errorf("invalid publish request; --expect required")
	}
	unlock, e := withLock(root)
	if e != nil {
		return p, e
	}
	defer unlock()
	current, e := active(root, scenario, version)
	if errors.Is(e, os.ErrNotExist) {
		if expected != "none" {
			return p, fmt.Errorf("expected old version but none exists")
		}
	} else if e != nil {
		return p, e
	} else if expected != current.RunID {
		return p, fmt.Errorf("CAS conflict: current=%s expected=%s", current.RunID, expected)
	}
	r, e := readRecord(root, id)
	if e != nil {
		return p, e
	}
	if r.Scenario != scenario || r.Version != version {
		return p, fmt.Errorf("batch belongs to different scenario/version")
	}
	if e = Verify(root, id); e != nil {
		return p, fmt.Errorf("cannot publish incomplete batch: %w", e)
	}
	if current.RunID == id {
		return current, nil
	}
	p = Pointer{Format: Format, Scenario: scenario, Version: version, RunID: id, PreviousRunID: current.RunID, PublishedUTC: time.Now().UTC().Format(time.RFC3339Nano)}
	return p, atomicJSON(pointerPath(root, scenario, version), p)
}
func Restore(root, id, dest string) error {
	if e := Verify(root, id); e != nil {
		return e
	}
	abs, e := filepath.Abs(dest)
	if e != nil {
		return e
	}
	if _, e = os.Lstat(abs); e == nil {
		return fmt.Errorf("destination already exists")
	}
	if !os.IsNotExist(e) {
		return e
	}
	r, e := readRecord(root, id)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(abs), 0700); e != nil {
		return e
	}
	temp, e := os.MkdirTemp(filepath.Dir(abs), ".restore-batch-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temp)
	names := make([]string, 0, len(r.Files))
	for n := range r.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		ref := r.Files[name]
		destPath := filepath.Join(temp, filepath.FromSlash(name))
		if e = os.MkdirAll(filepath.Dir(destPath), 0700); e != nil {
			return e
		}
		in, err := os.Open(objPath(root, ref.SHA256))
		if err != nil {
			return err
		}
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			in.Close()
			return err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(out, h), in)
		if err == nil {
			err = out.Sync()
		}
		in.Close()
		if c := out.Close(); err == nil {
			err = c
		}
		if err != nil {
			return err
		}
		if n != ref.Bytes || hex.EncodeToString(h.Sum(nil)) != ref.SHA256 {
			return fmt.Errorf("archive corrupted while restoring %s", name)
		}
	}
	return os.Rename(temp, abs)
}
func Prune(root string, keepPrevious, olderDays int, apply bool) (PrunePlan, error) {
	plan := PrunePlan{DryRun: !apply, RemoveIDs: []string{}}
	if keepPrevious < 0 || olderDays < 0 {
		return plan, fmt.Errorf("retention must be nonnegative")
	}
	unlock, e := withLock(root)
	if e != nil {
		return plan, e
	}
	defer unlock()
	runs, e := List(root)
	if e != nil {
		return plan, e
	}
	pointers, e := activeIndex(root)
	if e != nil {
		return plan, e
	}
	groups := map[string][]Record{}
	for _, r := range runs {
		key := r.Scenario + "\x00" + r.Version
		if _, ok := pointers[key]; !ok {
			return plan, fmt.Errorf("refusing prune: missing active pointer for %s/%s", r.Scenario, r.Version)
		}
		groups[key] = append(groups[key], r)
	}
	cutoff := time.Now().UTC().Add(-time.Duration(olderDays) * 24 * time.Hour)
	retained := map[string]bool{}
	drop := map[string]bool{}
	for k, items := range groups {
		kept := 0
		for _, r := range items {
			if r.ID == pointers[k].RunID {
				plan.ProtectedCurrent++
				retained[r.ID] = true
				continue
			}
			t, e := time.Parse(time.RFC3339Nano, r.CreatedUTC)
			if e != nil {
				return plan, e
			}
			if kept < keepPrevious || t.After(cutoff) {
				plan.ProtectedHistory++
				retained[r.ID] = true
			} else {
				drop[r.ID] = true
				plan.RemoveIDs = append(plan.RemoveIDs, r.ID)
			}
			kept++
		}
	}
	sort.Strings(plan.RemoveIDs)
	referenced := map[string]bool{}
	unreferenced := map[string]int64{}
	for _, r := range runs {
		for _, b := range r.Files {
			if retained[r.ID] {
				referenced[b.SHA256] = true
			} else if drop[r.ID] {
				unreferenced[b.SHA256] = b.Bytes
			}
		}
	}
	for sha, sz := range unreferenced {
		if !referenced[sha] {
			plan.ReclaimableBytes += sz
		}
	}
	if apply {
		for _, id := range plan.RemoveIDs {
			if e = os.Remove(recordPath(root, id)); e != nil {
				return plan, e
			}
		}
		for sha := range unreferenced {
			if !referenced[sha] {
				if e = os.Remove(objPath(root, sha)); e != nil && !os.IsNotExist(e) {
					return plan, e
				}
			}
		}
	}
	return plan, nil
}

// GC reclaims objects from interrupted registrations, and abandoned staging
// files. It never deletes a referenced file or a metadata record. By default
// it is a dry-run; the same lock protects batch registration and GC.
type GCPlan struct {
	DryRun           bool  `json:"dry_run"`
	OrphanObjects    int   `json:"orphan_objects"`
	StagingFiles     int   `json:"staging_files"`
	ReclaimableBytes int64 `json:"reclaimable_bytes"`
}

func GC(root string, apply bool) (GCPlan, error) {
	plan := GCPlan{DryRun: !apply}
	unlock, e := withLock(root)
	if e != nil {
		return plan, e
	}
	defer unlock()
	records, e := List(root)
	if e != nil {
		return plan, e
	}
	if _, e = activeIndex(root); e != nil {
		return plan, e
	}
	keep := map[string]bool{}
	for _, r := range records {
		for _, ref := range r.Files {
			keep[ref.SHA256] = true
		}
	}
	objects := filepath.Join(root, "objects")
	dirs, e := os.ReadDir(objects)
	if os.IsNotExist(e) {
		return plan, nil
	}
	if e != nil {
		return plan, e
	}
	for _, dir := range dirs {
		if dir.Name() == ".staging" {
			if !dir.IsDir() {
				return plan, fmt.Errorf("unsafe staging entry")
			}
			stages, e := os.ReadDir(filepath.Join(objects, ".staging"))
			if e != nil {
				return plan, e
			}
			for _, entry := range stages {
				p := filepath.Join(objects, ".staging", entry.Name())
				fi, e := os.Lstat(p)
				if e != nil {
					return plan, e
				}
				if !fi.Mode().IsRegular() || !strings.HasPrefix(entry.Name(), ".blob-") {
					return plan, fmt.Errorf("unsafe staging file %s", p)
				}
				plan.StagingFiles++
				plan.ReclaimableBytes += fi.Size()
				if apply {
					if e = os.Remove(p); e != nil {
						return plan, e
					}
				}
			}
			continue
		}
		if len(dir.Name()) != 2 || !dir.IsDir() {
			return plan, fmt.Errorf("unexpected objects entry %s", dir.Name())
		}
		entries, e := os.ReadDir(filepath.Join(objects, dir.Name()))
		if e != nil {
			return plan, e
		}
		for _, entry := range entries {
			name := entry.Name()
			sha := strings.TrimSuffix(name, ".raw")
			if !hexPattern.MatchString(sha) || name != sha+".raw" || !strings.HasPrefix(sha, dir.Name()) {
				return plan, fmt.Errorf("unexpected object %s", name)
			}
			path := filepath.Join(objects, dir.Name(), name)
			fi, e := os.Lstat(path)
			if e != nil {
				return plan, e
			}
			if !fi.Mode().IsRegular() {
				return plan, fmt.Errorf("unsafe object %s", path)
			}
			if !keep[sha] {
				plan.OrphanObjects++
				plan.ReclaimableBytes += fi.Size()
				if apply {
					if e = os.Remove(path); e != nil {
						return plan, e
					}
				}
			}
		}
	}
	return plan, nil
}
