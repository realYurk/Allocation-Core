package engine

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTwoAndThreeDrivers(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "examples")
	for _, tc := range []struct {
		name string
		a, b int64
	}{
		{"dual_driver", 72500, 27500},
		{"triple_driver", 59000, 41000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := filepath.Join(root, tc.name, "rule.json")
			dest := filepath.Join(t.TempDir(), "run")
			s, err := Run(rule, dest)
			if err != nil {
				t.Fatal(err)
			}
			if s.Status != "PASS" || s.InputAmount != "1000.00" || s.OutputAmount != "1000.00" {
				t.Fatal(s)
			}
			b, err := os.ReadFile(filepath.Join(dest, "results.csv"))
			if err != nil {
				t.Fatal(err)
			}
			rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(b), "\ufeff"))).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			result := map[string]int64{}
			for _, r := range rows[1:] {
				v, e := ParseScaled(r[len(r)-1], 2)
				if e != nil {
					t.Fatal(e)
				}
				result[r[4]] += v
			}
			if result["Item-A"] != tc.a || result["Item-B"] != tc.b {
				t.Fatalf("unexpected totals %v", result)
			}
			if err := CompareBeforeAfter(rule, dest, filepath.Join(dest, "comparison.csv"), []string{"account", "period"}); err != nil {
				t.Fatal(err)
			}
			cb, err := os.ReadFile(filepath.Join(dest, "comparison.csv"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(cb), "1000.00,1000.00,0.00") {
				t.Fatal(string(cb))
			}
		})
	}
}

func TestUnalignedBeforeAfterIsNotFaked(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "examples", "dual_driver")
	dest := filepath.Join(t.TempDir(), "out")
	rule := filepath.Join(root, "rule.json")
	if _, err := Run(rule, dest); err != nil {
		t.Fatal(err)
	}
	if err := CompareBeforeAfter(rule, dest, filepath.Join(dest, "comparison.csv"), []string{"sku"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "comparison.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Unassigned,1000.00,0.00,-1000.00") || !strings.Contains(string(b), "Item-A,0.00,725.00,725.00") {
		t.Fatal(string(b))
	}
}
