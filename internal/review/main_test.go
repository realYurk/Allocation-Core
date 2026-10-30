package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoarsePostingNotDiscardedAndSourceIgnored(t *testing.T) {
	d := t.TempDir()
	h := "Scenario,Account,Source,Model,Line,Value\n"
	a := h + "S1,COST,OriginA,No Model,No Line,10\nS1,COST,OriginB,No Model,No Line,2\nS1,DRV,OriginA,M1,L1,5\n"
	b := h + "S1,COST,Base,M1,L1,12\nS1,DRV,Base,M1,L1,5\n"
	if e := os.WriteFile(filepath.Join(d, "b.csv"), []byte(a), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(d, "a.csv"), []byte(b), 0600); e != nil {
		t.Fatal(e)
	}
	c := Config{ValueColumn: "Value", AccountColumn: "Account", SourceColumn: "Source", TargetSource: "Base", ScenarioColumns: []string{"Scenario"}, CoarseMarkers: map[string][]string{"Model": {"No Model"}, "Line": {"No Line"}}, DriverAccounts: []string{"DRV"}, Tolerance: "0.01"}
	r, e := execute(c, filepath.Join(d, "b.csv"), filepath.Join(d, "a.csv"), "", filepath.Join(d, "out"), -1, -1)
	if e != nil {
		t.Fatal(e)
	}
	if r.TotalEqual != 1 || r.Drivers != 1 || r.TotalUnequal != 0 {
		t.Fatalf("unexpected %+v", r)
	}
	html, e := os.ReadFile(filepath.Join(d, "out", "index.html"))
	if e != nil || !strings.Contains(string(html), "合法的记账粒度") {
		t.Fatal("coarse posting explanation missing")
	}
}
func TestEngineComparisonDetectsChangedCoordinate(t *testing.T) {
	d := t.TempDir()
	h := "Scenario,Account,Source,Model,Line,Value\n"
	os.WriteFile(filepath.Join(d, "b.csv"), []byte(h+"S1,COST,Original,No Model,No Line,12\n"), 0600)
	os.WriteFile(filepath.Join(d, "a.csv"), []byte(h+"S1,COST,Base,M1,L1,12\n"), 0600)
	os.WriteFile(filepath.Join(d, "c.csv"), []byte(h+"S1,COST,Base,M2,L2,12\n"), 0600)
	c := Config{ValueColumn: "Value", AccountColumn: "Account", SourceColumn: "Source", TargetSource: "Base", ScenarioColumns: []string{"Scenario"}, Tolerance: "0.01"}
	r, e := execute(c, filepath.Join(d, "b.csv"), filepath.Join(d, "a.csv"), filepath.Join(d, "c.csv"), filepath.Join(d, "out"), -1, -1)
	if e != nil {
		t.Fatal(e)
	}
	if r.ExactDifferent != 2 || r.Status != "ENGINE_DIFFERENCES" {
		t.Fatalf("unexpected %+v", r)
	}
}
