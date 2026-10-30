package batch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBatchIndependent(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(here), "..", "..", "examples")
	m := Manifest{Schema: Schema, Jobs: []JobSpec{{ID: "a", Rule: filepath.Join(root, "manufacturing", "rule.json")}, {ID: "b", Rule: filepath.Join(root, "cosmetics", "rule.json")}, {ID: "c", Rule: filepath.Join(root, "split_merge", "rule.json")}}}
	raw, _ := json.Marshal(m)
	input := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "results")
	report, err := Run(input, out, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Succeeded != 3 || report.Failed != 0 {
		t.Fatalf("%+v", report)
	}
	for _, j := range report.Jobs {
		if j.Summary == nil || j.Summary.InputAmount != j.Summary.OutputAmount {
			t.Fatal(j)
		}
		if _, err := os.Stat(filepath.Join(out, j.ID, "ledger.csv")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "batch_report.json")); err != nil {
		t.Fatal(err)
	}
}
func TestBatchFailuresIsolated(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(here), "..", "..", "examples")
	m := Manifest{Schema: Schema, Jobs: []JobSpec{{ID: "good", Rule: filepath.Join(root, "cosmetics", "rule.json")}, {ID: "bad", Rule: "missing.json"}}}
	raw, _ := json.Marshal(m)
	dir := t.TempDir()
	path := filepath.Join(dir, "batch.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	report, err := Run(path, filepath.Join(dir, "results"), 2, 2)
	if err == nil || report.Succeeded != 1 || report.Failed != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "results", "good", "results.csv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "results", "bad")); !os.IsNotExist(err) {
		t.Fatalf("failed published dir: %v", err)
	}
}
func TestManifestRejectsTraversalIDs(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "m.json")
	if err := os.WriteFile(f, []byte(`{"schema":"allocation-batch/1.0","jobs":[{"id":"../evil","rule":"x"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(f); err == nil {
		t.Fatal("unsafe ID accepted")
	}
}

func TestBatchStreamEvidence(t *testing.T) {
	root := filepath.Join("..", "..", "examples")
	fixture, errAbs := filepath.Abs(filepath.Join(root, "dual_driver", "rule.json"))
	if errAbs != nil {
		t.Fatal(errAbs)
	}
	path := filepath.Join(t.TempDir(), "batch.json")
	raw, _ := json.Marshal(Manifest{Schema: Schema, Jobs: []JobSpec{{ID: "job", Rule: fixture}}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	r, err := RunWithCompression(path, out, 1, 2, 4, true)
	if err != nil || r.Succeeded != 1 {
		t.Fatalf("result %+v error %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(out, "job", "ledger.csv.gz")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "job", "ledger.csv")); !os.IsNotExist(err) {
		t.Fatal("unexpected uncompressed evidence")
	}
}
