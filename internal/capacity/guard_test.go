package capacity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func budget(t *testing.T, p Policy) string {
	t.Helper()
	b, _ := json.Marshal(p)
	path := filepath.Join(t.TempDir(), "budget.json")
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	return path
}
func sample() Policy {
	return Policy{Pools: 12, TargetsPerPool: 1000, FactorPaths: 2, ResultBytesPerRow: 144, EvidenceBytesPerRow: 300, OverheadPercent: 150, MaxRows: 30000, MaxPeakBytes: 1000000000, ReserveFreeBytes: 0}
}
func TestPassAndBlock(t *testing.T) {
	p := sample()
	a, e := Check(budget(t, p), t.TempDir())
	if e != nil || a.Status != "PASS" || a.EstimatedRows != "24000" {
		t.Fatalf("%+v %v", a, e)
	}
	p.TargetsPerPool = 100000
	a, e = Check(budget(t, p), t.TempDir())
	if e != nil || a.Status != "BLOCK" || !strings.Contains(strings.Join(a.Reasons, " "), "rows") {
		t.Fatalf("%+v %v", a, e)
	}
}
func TestInvalidAndFailClosed(t *testing.T) {
	p := sample()
	p.OverheadPercent = 99
	if _, e := Check(budget(t, p), t.TempDir()); e == nil {
		t.Fatal("accepted invalid budget")
	}
	t.Setenv("ALLOC_CAPACITY_POLICY", filepath.Join(t.TempDir(), "missing.json"))
	if CheckEnv(t.TempDir()) == nil {
		t.Fatal("missing enforced policy accepted")
	}
}
