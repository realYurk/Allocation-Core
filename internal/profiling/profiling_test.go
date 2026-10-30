package profiling

import (
	"allocationcore/internal/demo"
	"allocationcore/internal/engine"
	"os"
	"path/filepath"
	"testing"
)

func TestProfileProducesPrivateArtifactsAndRejectsOverwrite(t *testing.T) {
	dir := t.TempDir()
	if _, e := demo.Generate(dir); e != nil {
		t.Fatal(e)
	}
	rule := filepath.Join(dir, "examples", "manufacturing", "rule.json")
	sourceRule, e := os.ReadFile(filepath.Join("..", "..", "examples", "manufacturing", "rule.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(rule, sourceRule, 0600); e != nil {
		t.Fatal(e)
	}
	out := filepath.Join(dir, "result")
	r, e := Measure(rule, out, engine.RunOptions{Workers: 2, DiskPartitions: 4, EvidenceGzip: true})
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != "PASS" || r.OutputBytes == 0 || r.Files == 0 || r.PeakHeapAllocBytes == 0 {
		t.Fatalf("incomplete profile %+v", r)
	}
	for _, p := range []string{r.CPUProfile, r.HeapProfile, filepath.Join(out+".profile", "metrics.json")} {
		if s, e := os.Stat(p); e != nil || s.Size() == 0 {
			t.Fatalf("profile artifact missing %s: %v", p, e)
		}
	}
	if _, e := Measure(rule, out, engine.RunOptions{Workers: 1}); e == nil {
		t.Fatal("profile would overwrite an existing result")
	}
}
