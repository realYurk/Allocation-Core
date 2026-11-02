package multi

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinancialFailureDiagnosesRealZeroDriverAndNeverPublishes(t *testing.T) {
	project, engine := fixture(t)
	path := filepath.Join(project, "drivers", "material.csv")
	if e := os.WriteFile(path, []byte("period,department,sku,material\n2026-01,SharedPool,Item-A,0\n2026-01,SharedPool,Item-B,0\n"), 0600); e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(t.TempDir(), "must-not-publish")
	_, err := Run(project, output, engine)
	if err == nil {
		t.Fatal("expected failure")
	}
	d, ok := DiagnosisFromError(err)
	if !ok || d.GroupID == "" || d.AccountCount == 0 || d.Scope != "EXACT_POOL" || d.Pool["period"] != "2026-01" {
		t.Fatalf("missing exact diagnosis: %+v, err=%v", d, err)
	}
	if d.Code != "DRIVER_DENOMINATOR" && d.Code != "DRIVER_NO_MATCH" {
		t.Fatalf("wrong cause: %+v", d)
	}
	if d.Stage == "" || d.Allocation == "" || d.WeightField == "" || d.DriverFile != "drivers/material.csv" {
		t.Fatalf("driver context missing: %+v", d)
	}
	if !strings.Contains(d.OriginalError, "driver") {
		t.Fatal("missing original failed-run error")
	}
	if _, e := os.Stat(output); !os.IsNotExist(e) {
		t.Fatal("published failed result")
	}
}
func TestFailureNumericAdviceRetainsOriginal(t *testing.T) {
	id := strings.Repeat("c", 20)
	for _, tc := range []struct{ raw, code string }{{"decimal scale exceeds 6: 1.1234567", "PRECISION_EXCEEDED"}, {"amount overflow", "CAPACITY_OVERFLOW"}} {
		p := &project{groups: []*Group{{ID: id, Accounts: []string{"A"}, Error: tc.raw}}}
		d, ok := DiagnosisFromError(p.explainExecutionFailure(errors.New("rule group " + id + ": " + tc.raw)))
		if !ok || d.Code != tc.code || d.OriginalError != tc.raw || len(d.NextSteps) == 0 {
			t.Fatalf("%+v", d)
		}
	}
}
func TestFinancialFailureNeverGuessesUnknownOrCanceled(t *testing.T) {
	id := strings.Repeat("a", 20)
	p := &project{groups: []*Group{{ID: id, Accounts: []string{"A", "B"}, Template: "x.json", Error: "unknown engine error"}}}
	e := p.explainExecutionFailure(errors.New("rule group " + id + ": unknown engine error"))
	d, ok := DiagnosisFromError(e)
	if !ok || d.Code != "ENGINE_FAILED" || d.Scope != "RULE_GROUP" || len(d.Pool) != 0 || d.AccountCount != 2 {
		t.Fatalf("overconfident diagnosis: %+v", d)
	}
	cancel := errors.New("context canceled")
	if p.explainExecutionFailure(cancel) != cancel {
		t.Fatal("unknown cancellation must remain untouched")
	}
}
func TestFinancialFailureUsesJSONToDecodePool(t *testing.T) {
	id := strings.Repeat("b", 20)
	g := &Group{ID: id, Accounts: []string{"Cost,\"A"}, Template: "x.json", Error: `stage S1 rule R1 pool ["2026-01","Cost,\"A"]: driver denominator must be positive`}
	p := &project{groups: []*Group{g}, canonicalHeader: []string{"period", "account"}}
	d, ok := DiagnosisFromError(p.explainExecutionFailure(errors.New("rule group " + id + ": " + g.Error)))
	if !ok || d.Pool["account"] != "Cost,\"A" || d.AccountCount != 1 || len(d.Accounts) != 1 {
		t.Fatalf("bad JSON coordinate: %+v", d)
	}
}

func TestFailureDriverComesFromRunSnapshotNotCurrentTemplate(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "drivers", "original.csv")
	rule := filepath.Join(dir, "effective-rule.json")
	snapshot := "../../snapshots/" + digest([]byte(source))[:20] + ".csv"
	data := `{"datasets":{"drivers":{"factor":"` + snapshot + `"}},"stages":[{"id":"S","allocations":[{"id":"R","driver_dataset":"factor","weight_field":"hours"}]}]}`
	if e := os.WriteFile(rule, []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
	id := strings.Repeat("d", 20)
	g := &Group{ID: id, Accounts: []string{"A"}, Rule: rule, Template: "changed.json", Error: `stage S rule R pool ["A"]: driver denominator must be positive`}
	p := &project{dir: dir, groups: []*Group{g}, canonicalHeader: []string{"account"}, driverSourceSHA: map[string]string{source: "captured"}}
	d, ok := DiagnosisFromError(p.explainExecutionFailure(errors.New("rule group " + id + ": " + g.Error)))
	if !ok || d.DriverFile != "drivers/original.csv" || d.WeightField != "hours" {
		t.Fatalf("wrong immutable context: %+v", d)
	}
}
