package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestV14GeneratorUsesASCIIFilenames(t *testing.T) {
	// Reuse the public manufacturing fixture for a real multi-stage rule.
	rule := filepath.Join("..", "..", "examples", "manufacturing", "rule.json")
	dest := filepath.Join(t.TempDir(), "new_company")
	if e := Generate(rule, dest); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"Windows_Run_Allocation.bat", "NEW_COMPANY_START_HERE.md", "rule.json"} {
		if _, e := os.Stat(filepath.Join(dest, name)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := os.Stat(filepath.Join(dest, "Windows_双击运行.bat")); !os.IsNotExist(e) {
		t.Fatal("old Unicode launcher still generated")
	}
}
