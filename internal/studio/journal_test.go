package studio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJournalDoesNotCreateResultBeforeEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "multi-jobs")
	id := "0123456789abcdef01234567"
	j := &Job{ID: id, Case: "four", Status: "QUEUED", Start: time.Now().UTC().Format(time.RFC3339), Output: filepath.Join(root, id)}
	if err := atomicJobUpdate(j, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(j.Output); !os.IsNotExist(err) {
		t.Fatalf("engine output must not exist yet: %v", err)
	}
	saved, err := readJournal(root, id, true)
	if err != nil || saved.Status != "QUEUED" || saved.Output != j.Output {
		t.Fatalf("invalid queue journal: %+v %v", saved, err)
	}
	j.Status = "RUNNING"
	if err := atomicJobUpdate(j, true); err != nil {
		t.Fatal(err)
	}
	recovered, err := recoverJournals(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if recovered[id].Status != "INTERRUPTED" || recovered[id].Error == "" {
		t.Fatalf("unsafe restart: %+v", recovered[id])
	}
	again, err := recoverJournals(root, true)
	if err != nil || again[id].Status != "INTERRUPTED" {
		t.Fatalf("not idempotent: %+v %v", again[id], err)
	}
}

func TestJournalRestoresPASSAndRejectsCorruptSummary(t *testing.T) {
	root := filepath.Join(t.TempDir(), "multi-jobs")
	id := "a123456789abcdef01234567"
	out := filepath.Join(root, id)
	if err := os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	j := &Job{ID: id, Case: "account_case", Status: "PASS", Start: time.Now().UTC().Format(time.RFC3339), Output: out}
	if err := atomicJobUpdate(j, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "run_summary.json"), []byte(`{"status":"PASS"}`), 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := recoverJournals(root, true)
	if err != nil || recovered[id].Status != "PASS" {
		t.Fatalf("valid PASS lost %v %+v", err, recovered[id])
	}
	if err := os.WriteFile(filepath.Join(out, "run_summary.json"), []byte(`{"status":"FAILED"}`), 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err = recoverJournals(root, true)
	if err != nil || recovered[id].Status != "INTERRUPTED" {
		t.Fatalf("corrupt PASS retained %v %+v", err, recovered[id])
	}
}

func TestJournalBackwardCompatibilityAndHistory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "multi-jobs")
	id := "bbbbbbbbbbbbbbbbbbbbbbbb"
	out := filepath.Join(root, id)
	if err := os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	j := Job{ID: id, Case: "old_case", Status: "PASS", Start: "2026-10-01T12:00:00Z"}
	raw, _ := json.Marshal(j)
	if err := os.WriteFile(filepath.Join(out, "multi_job.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "run_summary.json"), []byte(`{"status":"PASS"}`), 0600); err != nil {
		t.Fatal(err)
	}
	res, err := recoverJournals(root, true)
	if err != nil || res[id].Status != "PASS" {
		t.Fatalf("historical VNext3 status not restored: %v %+v", err, res[id])
	}
	listing := historicalJobs(root, res, true, 100)
	if len(listing) != 1 || listing[0].ID != id {
		t.Fatalf("historical list: %+v", listing)
	}
}

func TestJournalCannotUseArbitraryPaths(t *testing.T) {
	root := t.TempDir()
	j := &Job{ID: "../../etc/passwd", Output: filepath.Join(root, "escape"), Case: "x"}
	if err := atomicJobUpdate(j, true); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := readJournal(root, "../bad", true); err == nil {
		t.Fatal("invalid job id accepted")
	}
}
