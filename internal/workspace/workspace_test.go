package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func demo(t *testing.T) string {
	t.Helper()
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..", "examples", "manufacturing", "rule.json")
}
func TestPublishAndPinnedRun(t *testing.T) {
	root := t.TempDir()
	config := demo(t)
	pub, e := Publish(config, root)
	if e != nil {
		t.Fatal(e)
	}
	pub2, e := Publish(config, root)
	if e != nil || pub.Revision != pub2.Revision || pub.RuleID != pub2.RuleID {
		t.Fatal("publish not idempotent", e)
	}
	rules, e := ListRules(root)
	if e != nil || len(rules) != 1 {
		t.Fatalf("rules: %v %v", rules, e)
	}
	job, e := Submit(config, root)
	if e != nil {
		t.Fatal(e)
	}
	if job.State != Succeeded || job.Summary == nil || job.Summary.InputAmount != job.Summary.OutputAmount {
		t.Fatalf("bad completion %+v", job)
	}
	if job.RuleRevision != pub.Revision {
		t.Fatal("job did not pin published revision")
	}
	if e = Verify(root, job.ID); e != nil {
		t.Fatal(e)
	}
	if list, e := List(root); e != nil || len(list) != 1 {
		t.Fatal("list", e, list)
	}
	dir, _ := jobDir(root, job.ID)
	// Published evidence alteration is detected; source evidence alteration is detected as well.
	out := filepath.Join(dir, "published", "results.csv")
	f, e := os.OpenFile(out, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteString("TAMPER\n")
	f.Close()
	if e = Verify(root, job.ID); e == nil || !strings.Contains(e.Error(), "checksum mismatch") {
		t.Fatalf("result alteration not found: %v", e)
	}
}
func TestRetryFromSnapshotAndLineage(t *testing.T) {
	root := t.TempDir()
	config := demo(t)
	job, e := Submit(config, root)
	if e != nil {
		t.Fatal(e)
	}
	// Simulate a crash after snapshotting; clean published content is not reused for retry.
	dir, _ := jobDir(root, job.ID)
	job.State = Running
	job.EndedUTC = ""
	if e = saveJob(dir, job); e != nil {
		t.Fatal(e)
	}
	old := time.Now().Add(-11 * time.Minute)
	if e = os.WriteFile(filepath.Join(dir, "heartbeat"), []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Chtimes(filepath.Join(dir, "heartbeat"), old, old); e != nil {
		t.Fatal(e)
	}
	fixed, e := Recover(root)
	if e != nil || len(fixed) != 1 || fixed[0] != job.ID {
		t.Fatal(fixed, e)
	}
	fixed, e = Recover(root)
	if e != nil || len(fixed) != 0 {
		t.Fatal("recovered more than once", fixed, e)
	}
	retry, e := Retry(root, job.ID)
	if e != nil {
		t.Fatal(e)
	}
	if retry.State != Succeeded || retry.ParentID != job.ID || retry.RuleRevision != job.RuleRevision {
		t.Fatalf("lineage not preserved: %+v", retry)
	}
	if e = Verify(root, retry.ID); e != nil {
		t.Fatal(e)
	}
	if retry.Summary.OutputAmount != job.Summary.OutputAmount {
		t.Fatal("retry changed calculated total")
	}
}
func TestOriginalInputsPinned(t *testing.T) {
	root := t.TempDir()
	copyRoot := t.TempDir()
	origin := demo(t)
	srcBase := filepath.Dir(origin)
	for _, name := range []string{"rule.json", "amounts.csv", "labor.csv", "sales.csv"} {
		b, e := os.ReadFile(filepath.Join(srcBase, name))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(copyRoot, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	config := filepath.Join(copyRoot, "rule.json")
	job, e := Submit(config, root)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(copyRoot, "amounts.csv"), []byte("garbage"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = Verify(root, job.ID); e != nil {
		t.Fatal("external input changes affected snapshot:", e)
	}
	dir, _ := jobDir(root, job.ID)
	j, e := Load(root, job.ID)
	if e != nil {
		t.Fatal(e)
	}
	j.State = Interrupted
	if e = saveJob(dir, j); e != nil {
		t.Fatal(e)
	}
	rerun, e := Retry(root, job.ID)
	if e != nil {
		t.Fatal("retry touched changed external data", e)
	}
	if rerun.Summary.OutputAmount != job.Summary.OutputAmount {
		t.Fatal("retry amounts mismatch")
	}
}
func TestFailedJobRetainsRecordAndRejectsPrematureRetry(t *testing.T) {
	root := t.TempDir()
	cfg := demo(t)
	original, e := os.ReadFile(cfg)
	if e != nil {
		t.Fatal(e)
	}
	// Valid rule whose driver CSV has an all-zero denominator.
	base := t.TempDir()
	for _, name := range []string{"amounts.csv", "labor.csv", "sales.csv"} {
		b, _ := os.ReadFile(filepath.Join(filepath.Dir(cfg), name))
		os.WriteFile(filepath.Join(base, name), b, 0600)
	}
	os.WriteFile(filepath.Join(base, "labor.csv"), []byte("period,plant,brand,family,sku,account,currency,hours\n2026-01,一号工厂,品牌甲,系列甲,Unassigned,Cost-A,CUR,0\n"), 0600)
	// Use another reproducible failure: add bad driver weights to the existing CSV rather than modifying rule.
	rule := filepath.Join(base, "rule.json")
	os.WriteFile(rule, original, 0600)
	job, e := Submit(rule, root)
	if e == nil {
		t.Fatal("expected failed run")
	}
	if job.State != Failed {
		t.Fatalf("failed job missing: %+v", job)
	}
	if _, e := os.Stat(filepath.Join(root, "jobs", job.ID, "published")); !os.IsNotExist(e) {
		t.Fatalf("partially published output: %v", e)
	}
	if _, e := Retry(root, job.ID); e == nil {
		t.Fatal("retry of identical failed input unexpectedly succeeded")
	}
	j, e := Load(root, job.ID)
	if e != nil || j.State != Failed {
		t.Fatal(j, e)
	}
	if e = Verify(root, job.ID); e == nil {
		t.Fatal("verified failed job")
	}
}
func TestBadIDsRejected(t *testing.T) {
	for _, id := range []string{"../../secret", "/etc/passwd", ".hidden", "a\\b", ""} {
		if _, e := jobDir(t.TempDir(), id); e == nil {
			t.Fatal("accepted", id)
		}
	}
}

func TestRuleVersionDiff(t *testing.T) {
	root := t.TempDir()
	orig := demo(t)
	p1, e := Publish(orig, root)
	if e != nil {
		t.Fatal(e)
	}
	old, e := os.ReadFile(orig)
	if e != nil {
		t.Fatal(e)
	}
	// Formatting change alone creates a new immutable revision (no semantic field change).
	changed := append([]byte(" \n"), old...)
	temp := filepath.Join(t.TempDir(), "rule.json")
	if e = os.WriteFile(temp, changed, 0600); e != nil {
		t.Fatal(e)
	}
	p2, e := Publish(temp, root)
	if e != nil {
		t.Fatal(e)
	}
	if p1.RuleID != p2.RuleID || p1.Revision == p2.Revision {
		t.Fatal("bad versions")
	}
	d, e := DiffPublishedRules(root, p1.RuleID, p1.Revision, p2.Revision)
	if e != nil || d.Changed != 0 {
		t.Fatal(d, e)
	}
	if _, e = DiffPublishedRules(root, p1.RuleID, "../../etc/passwd", p2.Revision); e == nil {
		t.Fatal("path traversal")
	}
}

func TestPendingRecoveryAndRuleRegistryTamper(t *testing.T) {
	root := t.TempDir()
	job, e := Submit(demo(t), root)
	if e != nil {
		t.Fatal(e)
	}
	rulefile := filepath.Join(root, "rules", job.RuleID, job.RuleRevision, "rule.json")
	raw, e := os.ReadFile(rulefile)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(rulefile, append(raw, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
	if e = Verify(root, job.ID); e == nil {
		t.Fatal("tampered rule store not detected")
	}
	if e = os.WriteFile(rulefile, raw, 0600); e != nil {
		t.Fatal(e)
	}
	dir, _ := jobDir(root, job.ID)
	job.State = Pending
	job.CreatedUTC = time.Now().Add(-15 * time.Minute).UTC().Format(time.RFC3339Nano)
	if e = saveJob(dir, job); e != nil {
		t.Fatal(e)
	}
	ids, e := Recover(root)
	if e != nil || len(ids) != 1 || ids[0] != job.ID {
		t.Fatal(ids, e)
	}
}
