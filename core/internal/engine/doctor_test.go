package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDoctorValidCases(t *testing.T) {
	_, f, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(f), "..", "..", "examples")
	for _, name := range []string{"dual_driver", "triple_driver", "manufacturing", "split_merge"} {
		p := filepath.Join(root, name, "rule.json")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := ParseSpec(raw)
		if err != nil {
			t.Fatal(err)
		}
		notes, err := Doctor(p, spec)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(notes) < 2 {
			t.Fatal("missing diagnosis")
		}
	}
}
func TestDoctorMissingDriverColumn(t *testing.T) {
	_, f, _, _ := runtime.Caller(0)
	p := filepath.Join(filepath.Dir(f), "..", "..", "examples", "dual_driver", "rule.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := ParseSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	spec.Datasets.Drivers["material"] = "amounts.csv" // file exists but no material driver
	_, err = Doctor(p, spec)
	if err == nil || !strings.Contains(err.Error(), "material") {
		t.Fatal(err)
	}
}
