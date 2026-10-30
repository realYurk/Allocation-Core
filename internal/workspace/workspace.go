// Package workspace implements an offline, filesystem-backed run registry.
// It requires only the Go standard library. It is NOT a multi-host transaction database.
package workspace

import (
	"allocationcore/internal/engine"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const Version = "workspace-v1"
const HeartbeatEvery = 2 * time.Second
const StaleAfter = 10 * time.Minute

const (
	Pending     = "PENDING"
	Running     = "RUNNING"
	Succeeded   = "SUCCEEDED"
	Failed      = "FAILED"
	Interrupted = "INTERRUPTED"
)

type PublishedRule struct {
	RuleID     string `json:"rule_id"`
	Revision   string `json:"revision"`
	Name       string `json:"name"`
	Schema     string `json:"schema"`
	CreatedUTC string `json:"created_utc"`
}
type Job struct {
	ID           string            `json:"id"`
	ParentID     string            `json:"parent_id,omitempty"`
	RuleID       string            `json:"rule_id"`
	RuleRevision string            `json:"rule_revision"`
	State        string            `json:"state"`
	CreatedUTC   string            `json:"created_utc"`
	StartedUTC   string            `json:"started_utc,omitempty"`
	EndedUTC     string            `json:"ended_utc,omitempty"`
	Message      string            `json:"message,omitempty"`
	FileHashes   map[string]string `json:"sha256"`
	Summary      *engine.Summary   `json:"summary,omitempty"`
	Workers      int               `json:"workers,omitempty"`
}

func now() string               { return time.Now().UTC().Format(time.RFC3339Nano) }
func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func hashFile(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func atomicWrite(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".pending-")
	if e != nil {
		return e
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(temp, path)
}
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return atomicWrite(path, append(b, '\n'))
}
func safeID(s string) bool {
	if len(s) == 0 || len(s) > 128 || s[0] == '-' || s[0] == '.' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func jobDir(root, id string) (string, error) {
	if !safeID(id) {
		return "", fmt.Errorf("invalid job ID")
	}
	return filepath.Join(root, "jobs", id), nil
}
func uniqueID() (string, error) {
	b := make([]byte, 6)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	t := time.Now().UTC()
	return t.Format("20060102T150405") + fmt.Sprintf("_%09dZ-", t.Nanosecond()) + hex.EncodeToString(b), nil
}

// publishRule writes the revision into a temporary directory and atomically
// renames it into place. Competing local processes never see half-written metadata.
func publishRule(raw []byte, root string) (PublishedRule, error) {
	s, e := engine.ParseSpec(raw)
	if e != nil {
		return PublishedRule{}, e
	}
	if _, e = engine.Compile(s); e != nil {
		return PublishedRule{}, e
	}
	rid := "r-" + hashBytes([]byte(s.Name))[:12]
	revision := hashBytes(raw)
	dir := filepath.Join(root, "rules", rid, revision)
	item := PublishedRule{RuleID: rid, Revision: revision, Name: s.Name, Schema: s.Schema, CreatedUTC: now()}
	readExisting := func() (PublishedRule, error) {
		old, e := os.ReadFile(filepath.Join(dir, "rule.json"))
		if e != nil {
			return PublishedRule{}, e
		}
		if hashBytes(old) != revision {
			return PublishedRule{}, fmt.Errorf("published rule was modified: %s", dir)
		}
		meta := PublishedRule{}
		if e = jsonRead(filepath.Join(dir, "metadata.json"), &meta); e != nil {
			return PublishedRule{}, e
		}
		if meta.Revision != revision || meta.RuleID != rid {
			return PublishedRule{}, fmt.Errorf("published rule metadata mismatch")
		}
		return meta, nil
	}
	if _, e = os.Stat(dir); e == nil {
		return readExisting()
	} else if !os.IsNotExist(e) {
		return item, e
	}
	parent := filepath.Dir(dir)
	if e = os.MkdirAll(parent, 0700); e != nil {
		return item, e
	}
	staging, e := os.MkdirTemp(parent, ".rule-incomplete-")
	if e != nil {
		return item, e
	}
	defer os.RemoveAll(staging)
	if e = atomicWrite(filepath.Join(staging, "rule.json"), raw); e != nil {
		return item, e
	}
	if e = writeJSON(filepath.Join(staging, "metadata.json"), item); e != nil {
		return item, e
	}
	if e = os.Rename(staging, dir); e != nil {
		// Another process might have published the exact same revision first.
		if _, statErr := os.Stat(dir); statErr == nil {
			return readExisting()
		}
		return item, e
	}
	return item, nil
}
func Publish(config, root string) (PublishedRule, error) {
	raw, e := os.ReadFile(config)
	if e != nil {
		return PublishedRule{}, e
	}
	return publishRule(raw, root)
}
func jsonRead(path string, to any) error {
	raw, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return json.Unmarshal(raw, to)
}
func saveJob(dir string, j Job) error { return writeJSON(filepath.Join(dir, "job.json"), j) }
func Load(root, id string) (Job, error) {
	dir, e := jobDir(root, id)
	if e != nil {
		return Job{}, e
	}
	var j Job
	e = jsonRead(filepath.Join(dir, "job.json"), &j)
	if e == nil && j.ID != id {
		return j, errors.New("job manifest ID mismatch")
	}
	return j, e
}
func copyAndHash(src, dst string) (string, error) {
	in, e := os.Open(src)
	if e != nil {
		return "", e
	}
	defer in.Close()
	if e = os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
		return "", e
	}
	out, e := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	h := sha256.New()
	_, e = io.Copy(io.MultiWriter(out, h), in)
	if e == nil {
		e = out.Sync()
	}
	if cl := out.Close(); e == nil {
		e = cl
	}
	if e != nil {
		os.Remove(dst)
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func inputName(prefix, k string) string { return prefix + "-" + hashBytes([]byte(k))[:20] + ".csv" }

// submitInternal freezes source rule, amounts and driver files BEFORE starting the engine.
// origRaw is a copy of the original published rule; sourceBase locates its dataset paths.
func submitInternal(origRaw []byte, sourceBase, root, parent string, overrides map[string]string, opts engine.RunOptions) (Job, error) {
	rule, e := publishRule(origRaw, root)
	if e != nil {
		return Job{}, e
	}
	spec, e := engine.ParseSpec(origRaw)
	if e != nil {
		return Job{}, e
	}
	id, e := uniqueID()
	if e != nil {
		return Job{}, e
	}
	jobpath, e := jobDir(root, id)
	if e != nil {
		return Job{}, e
	}
	staging := filepath.Join(root, "jobs", ".prepare-"+id)
	if e = os.MkdirAll(staging, 0700); e != nil {
		return Job{}, e
	}
	defer os.RemoveAll(staging)
	j := Job{ID: id, ParentID: parent, RuleID: rule.RuleID, RuleRevision: rule.Revision, State: Pending, CreatedUTC: now(), FileHashes: map[string]string{}, Workers: opts.Workers}
	if e = atomicWrite(filepath.Join(staging, "original_rule.json"), origRaw); e != nil {
		return j, e
	}
	j.FileHashes["original_rule.json"] = hashBytes(origRaw)
	for _, kind := range []string{"amount", "driver"} {
		var data map[string]string
		if kind == "amount" {
			data = spec.Datasets.Amounts
		} else {
			data = spec.Datasets.Drivers
		}
		for k, loc := range data {
			src, e := engine.Resolve(sourceBase, loc)
			if e != nil {
				return j, e
			}
			if other, ok := overrides[kind+"/"+k]; ok {
				src = other
			}
			rel := filepath.ToSlash(filepath.Join("inputs", inputName(kind, k)))
			digest, e := copyAndHash(src, filepath.Join(staging, filepath.FromSlash(rel)))
			if e != nil {
				return j, fmt.Errorf("snapshot %s/%s: %w", kind, k, e)
			}
			j.FileHashes[rel] = digest
			data[k] = rel
		}
	}
	frozen, e := json.MarshalIndent(spec, "", "  ")
	if e != nil {
		return j, e
	}
	if e = atomicWrite(filepath.Join(staging, "snapshot_rule.json"), frozen); e != nil {
		return j, e
	}
	j.FileHashes["snapshot_rule.json"] = hashBytes(frozen)
	if e = saveJob(staging, j); e != nil {
		return j, e
	}
	if e = os.Rename(staging, jobpath); e != nil {
		return j, e
	}
	return execute(root, j, opts)
}
func Submit(config, root string) (Job, error) {
	return SubmitWithOptions(config, root, engine.RunOptions{Workers: 1})
}
func SubmitWithOptions(config, root string, opts engine.RunOptions) (Job, error) {
	if opts.Workers < 0 || opts.Workers > 128 {
		return Job{}, fmt.Errorf("workers out of range 0..128")
	}
	b, e := os.ReadFile(config)
	if e != nil {
		return Job{}, e
	}
	abs, e := filepath.Abs(config)
	if e != nil {
		return Job{}, e
	}
	return submitInternal(b, filepath.Dir(abs), root, "", nil, opts)
}
func execute(root string, j Job, opts engine.RunOptions) (Job, error) {
	dir, e := jobDir(root, j.ID)
	if e != nil {
		return j, e
	}
	j.State = Running
	j.StartedUTC = now()
	j.EndedUTC = ""
	j.Message = ""
	if e = saveJob(dir, j); e != nil {
		return j, e
	}
	live := filepath.Join(dir, "heartbeat")
	if e = atomicWrite(live, []byte(now())); e != nil {
		return j, e
	}
	done := make(chan struct{})
	stop := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(HeartbeatEvery)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = atomicWrite(live, []byte(now()))
			}
		}
	}()
	summary, runErr := engine.RunWithOptions(filepath.Join(dir, "snapshot_rule.json"), filepath.Join(dir, "published"), opts)
	close(stop)
	<-done
	j.EndedUTC = now()
	j.Summary = &summary
	if runErr != nil {
		j.State = Failed
		j.Message = runErr.Error()
	} else {
		j.State = Succeeded
		for _, name := range []string{"results.csv", "ledger.csv", "reconciliation.csv", "source_to_pool.csv", "run_summary.json"} {
			rel := "published/" + name
			digest, e := hashFile(filepath.Join(dir, filepath.FromSlash(rel)))
			if e != nil {
				runErr = e
				j.State = Failed
				j.Message = e.Error()
				break
			}
			j.FileHashes[rel] = digest
		}
	}
	if e = saveJob(dir, j); e != nil {
		return j, e
	}
	_ = os.Remove(live)
	return j, runErr
}
func Retry(root, id string) (Job, error) { return RetryWithOptions(root, id, 0) }
func RetryWithOptions(root, id string, workers int) (Job, error) {
	old, e := Load(root, id)
	if e != nil {
		return Job{}, e
	}
	if old.State != Failed && old.State != Interrupted {
		return Job{}, fmt.Errorf("can only retry FAILED/INTERRUPTED, got %s", old.State)
	}
	if e = verifyFiles(root, old, false); e != nil {
		return Job{}, fmt.Errorf("input snapshot invalid; cannot retry: %w", e)
	}
	dir, _ := jobDir(root, id)
	raw, e := os.ReadFile(filepath.Join(dir, "original_rule.json"))
	if e != nil {
		return Job{}, e
	}
	snap, e := os.ReadFile(filepath.Join(dir, "snapshot_rule.json"))
	if e != nil {
		return Job{}, e
	}
	ss, e := engine.ParseSpec(snap)
	if e != nil {
		return Job{}, e
	}
	overrides := map[string]string{}
	for k, p := range ss.Datasets.Amounts {
		overrides["amount/"+k] = filepath.Join(dir, filepath.FromSlash(p))
	}
	for k, p := range ss.Datasets.Drivers {
		overrides["driver/"+k] = filepath.Join(dir, filepath.FromSlash(p))
	}
	// Keep the original published rule revision; take the new job inputs exclusively from old snapshots.
	if workers == 0 {
		workers = old.Workers
	}
	if workers == 0 {
		workers = 1
	}
	return submitInternal(raw, dir, root, id, overrides, engine.RunOptions{Workers: workers})
}
func verifyFiles(root string, j Job, includeOutputs bool) error {
	dir, e := jobDir(root, j.ID)
	if e != nil {
		return e
	}
	count := 0
	for rel, want := range j.FileHashes {
		if !includeOutputs && strings.HasPrefix(rel, "published/") {
			continue
		}
		if filepath.IsAbs(rel) || strings.Contains(rel, "..") || strings.ContainsRune(rel, '\\') {
			return fmt.Errorf("unsafe manifest path %q", rel)
		}
		got, e := hashFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if e != nil {
			return fmt.Errorf("missing %s: %w", rel, e)
		}
		if got != want {
			return fmt.Errorf("modified %s: checksum mismatch", rel)
		}
		count++
	}
	if count == 0 {
		return errors.New("manifest contains no tracked files")
	}
	return nil
}
func Verify(root, id string) error {
	j, e := Load(root, id)
	if e != nil {
		return e
	}
	if j.State != Succeeded {
		return fmt.Errorf("job not successful (%s)", j.State)
	}
	if e = verifyFiles(root, j, true); e != nil {
		return e
	}
	dir, _ := jobDir(root, id)
	published := 0
	for k := range j.FileHashes {
		if strings.HasPrefix(k, "published/") {
			published++
		}
	}
	if published != 5 {
		return fmt.Errorf("incomplete output checksums: %d", published)
	}
	if hashBytesMust(filepath.Join(dir, "original_rule.json")) != j.RuleRevision {
		return fmt.Errorf("rule revision mismatch")
	}
	registered := filepath.Join(root, "rules", j.RuleID, j.RuleRevision, "rule.json")
	if hashBytesMust(registered) != j.RuleRevision {
		return fmt.Errorf("registered rule revision was removed or modified")
	}
	return nil
}
func hashBytesMust(path string) string { h, _ := hashFile(path); return h }
func Recover(root string) ([]string, error) {
	jobs, e := List(root)
	if e != nil {
		return nil, e
	}
	fixed := []string{}
	for _, j := range jobs {
		if j.State != Running && j.State != Pending {
			continue
		}
		dir, _ := jobDir(root, j.ID)
		if j.State == Running {
			info, e := os.Stat(filepath.Join(dir, "heartbeat"))
			if e != nil && !os.IsNotExist(e) {
				return fixed, e
			}
			if e == nil && time.Since(info.ModTime()) < StaleAfter {
				continue
			}
		} else {
			created, e := time.Parse(time.RFC3339Nano, j.CreatedUTC)
			if e != nil {
				return fixed, e
			}
			if time.Since(created) < StaleAfter {
				continue
			}
		}
		// Recovery marks abandoned work interrupted. Retrying explicitly restarts the whole DAG.
		j.State = Interrupted
		j.EndedUTC = now()
		j.Message = "pending task or heartbeat expired; retry to recompute from immutable input snapshots"
		if e = saveJob(dir, j); e != nil {
			return fixed, e
		}
		fixed = append(fixed, j.ID)
	}
	return fixed, nil
}
func List(root string) ([]Job, error) {
	entries, e := os.ReadDir(filepath.Join(root, "jobs"))
	if os.IsNotExist(e) {
		return []Job{}, nil
	}
	if e != nil {
		return nil, e
	}
	result := []Job{}
	for _, en := range entries {
		if !en.IsDir() || !safeID(en.Name()) {
			continue
		}
		j, e := Load(root, en.Name())
		if e != nil {
			return nil, e
		}
		result = append(result, j)
	}
	sort.Slice(result, func(i, k int) bool { return result[i].CreatedUTC > result[k].CreatedUTC })
	return result, nil
}
func ListRules(root string) ([]PublishedRule, error) {
	folder := filepath.Join(root, "rules")
	ids, e := os.ReadDir(folder)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var out []PublishedRule
	for _, id := range ids {
		if !id.IsDir() {
			continue
		}
		versions, e := os.ReadDir(filepath.Join(folder, id.Name()))
		if e != nil {
			return nil, e
		}
		for _, v := range versions {
			if !v.IsDir() {
				continue
			}
			var r PublishedRule
			if e = jsonRead(filepath.Join(folder, id.Name(), v.Name(), "metadata.json"), &r); e != nil {
				return nil, e
			}
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RuleID == out[j].RuleID {
			return out[i].CreatedUTC < out[j].CreatedUTC
		}
		return out[i].RuleID < out[j].RuleID
	})
	return out, nil
}
