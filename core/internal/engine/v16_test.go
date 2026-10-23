package engine

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestV16RuleParsingRejectsTrailingJSON(t *testing.T) {
	base := `{"schema":"allocation-ir/2.0"}`
	if _, err := ParseSpec([]byte(base + " true")); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := ParseSpec([]byte(base + " invalid")); err == nil {
		t.Fatal("junk trailing JSON accepted")
	}
}
func TestV16CSVHeaderDuplicatesRejected(t *testing.T) {
	if _, err := HeaderPositions([]string{"period", "amount", "amount"}); err == nil {
		t.Fatal("duplicate headers accepted")
	}
	if _, err := HeaderPositions([]string{"\ufeffamount", "amount"}); err == nil {
		t.Fatal("BOM duplicate accepted")
	}
	if _, err := HeaderPositions([]string{"period", ""}); err == nil {
		t.Fatal("blank header accepted")
	}
}
func V16TestRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "examples")
}
func TestV16LineageCoalescingAndTrace(t *testing.T) {
	ruleFile := filepath.Join(V16TestRoot(t), "dual_driver", "rule.json")
	raw, err := os.ReadFile(ruleFile)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := ParseSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	original := spec.Stages[0]
	finish := original
	finish.Operation = "PASS"
	finish.ID = "查看汇总"
	finish.Inputs = []InputEdge{{From: original.ID, Share: "1"}}
	finish.Allocations = nil
	spec.Stages = append(spec.Stages, finish)
	// Resolve the two original CSV datasets from the temporary rule location.
	for k, p := range spec.Datasets.Amounts {
		spec.Datasets.Amounts[k] = filepath.Join(filepath.Dir(ruleFile), p)
	}
	for k, p := range spec.Datasets.Drivers {
		spec.Datasets.Drivers[k] = filepath.Join(filepath.Dir(ruleFile), p)
	}
	folder := t.TempDir()
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(folder, "rule.json")
	if err = os.WriteFile(config, b, 0600); err != nil {
		t.Fatal(err)
	}
	outs := []string{}
	for _, kind := range []string{"memory", "disk"} {
		dir := filepath.Join(folder, kind)
		opts := RunOptions{Workers: 2}
		if kind == "disk" {
			opts.DiskPartitions = 4
		}
		if kind == "disk" {
			_, err = RunDiskWithOptions(config, dir, opts)
		} else {
			_, err = RunWithOptions(config, dir, opts)
		}
		if err != nil {
			t.Fatal(kind, err)
		}
		f, err := os.Open(filepath.Join(dir, "lineage_links.csv"))
		if err != nil {
			t.Fatal(err)
		}
		records, err := csv.NewReader(f).ReadAll()
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 3 {
			t.Fatalf("%s expected 2 coalesce links got %d: %v", kind, len(records)-1, records)
		}
		rf, err := os.Open(filepath.Join(dir, "results.csv"))
		if err != nil {
			t.Fatal(err)
		}
		rs, err := csv.NewReader(rf).ReadAll()
		rf.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(rs) != 3 {
			t.Fatalf("%s expected 2 merged target SKUs got %d", kind, len(rs)-1)
		}
		trace, err := TraceRow(dir, rs[1][1], 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(trace.Roots) != 1 {
			t.Fatalf("%s expected one source root got %+v", kind, trace.Roots)
		}
		found := map[string]bool{}
		for _, e := range trace.Links {
			if e.Rule != "" {
				found[e.Rule] = true
			}
		}
		if !found["material"] || !found["revenue"] {
			t.Fatalf("%s dual-factor ancestry lost: %+v", kind, trace)
		}
		outs = append(outs, dir)
	}
	a, err := os.ReadFile(filepath.Join(outs[0], "results.csv"))
	if err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(filepath.Join(outs[1], "results.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("memory and disk calculation differ")
	}
}
func TestV16DifferentDatasetSourceIDsAreUnique(t *testing.T) {
	if sourceRowID("expenseA", "1", 1) == sourceRowID("expenseB", "1", 1) {
		t.Fatal("source id collision")
	}
	if sourceRowID("expenseA", "1", 1) == sourceRowID("expenseA", "1", 2) {
		t.Fatal("source line collision")
	}
	if !strings.Contains(sourceRowID("expenseA", "1", 1), "expenseA") {
		t.Fatal("cannot identify origin")
	}
}
