package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectInputsGenericAndExact(t *testing.T) {
	root := t.TempDir()
	// A legal coarse-grain member is not an error or a missing coordinate.
	write := func(n, s string) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(root, n), []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("amount.csv", "period,item,amount\n2026-01,Unassigned,100.02\n2026-01,No Model,-0.01\n2026-02,X,0.00\n")
	write("driver.csv", "period,item,hours,units\n2026-01,A,2,1\n2026-01,B,3,0\n")
	write("rule.json", "{}")
	spec := Spec{Schema: Schema, Precision: 2, Dimensions: []string{"period", "item"}}
	spec.Datasets.Amounts = map[string]string{"base": "amount.csv"}
	spec.Datasets.Drivers = map[string]string{"factors": "driver.csv"}
	spec.Stages = []Stage{{ID: "s1", Allocations: []Allocation{{ID: "a", DriverDataset: "factors", WeightField: "hours"}, {ID: "b", DriverDataset: "factors", WeightField: "units"}}}}
	r := InspectInputs(filepath.Join(root, "rule.json"), spec)
	if r.Status != "PASS" || len(r.Checks) != 2 || r.Checks[0].Rows != 3 || r.Checks[0].Sum != "100.01" || r.Checks[0].NegativeValues != 1 || r.Checks[0].ZeroValues != 1 || r.Checks[1].Rows != 2 {
		t.Fatalf("unexpected report: %#v", r)
	}
	if len(r.Checks[1].WeightFields) != 2 {
		t.Fatalf("driver was not deduplicated: %#v", r.Checks[1])
	}
}

func TestInspectInputsFailClosedOnBadRows(t *testing.T) {
	root := t.TempDir()
	rule := filepath.Join(root, "rule.json")
	if e := os.WriteFile(rule, []byte("{}"), 0600); e != nil {
		t.Fatal(e)
	}
	spec := Spec{Schema: Schema, Precision: 2, Dimensions: []string{"period", "item"}}
	spec.Datasets.Amounts = map[string]string{"expenses": "amount.csv"}
	for _, tc := range []struct{ name, text, want string }{
		{"missing", "period,amount\n2026-01,12.00\n", "missing dimension"},
		{"invalid", "period,item,amount\n2026-01,A,NaN\n", "invalid decimal"},
		{"short", "period,item,amount\n2026-01,A\n", "expected 3 columns"},
		{"empty", "period,item,amount\n2026-01,,12.00\n", "empty/reserved"},
		{"reserved", "period,item,amount\n2026-01,__POOL__,12.00\n", "empty/reserved"},
		{"zeroRows", "period,item,amount\n", "no data rows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if e := os.WriteFile(filepath.Join(root, "amount.csv"), []byte(tc.text), 0600); e != nil {
				t.Fatal(e)
			}
			r := InspectInputs(rule, spec)
			if r.Status != "BLOCK" || len(r.Checks) != 1 || !strings.Contains(r.Checks[0].Error, tc.want) {
				t.Fatalf("expected BLOCK %q, got %#v", tc.want, r)
			}
		})
	}
}

func TestParallelPoolsStableOrderAndEarlyFailure(t *testing.T) {
	keys := make([]string, 1203)
	for i := range keys {
		keys[i] = string(rune(0x5000 + i))
	}
	for _, workers := range []int{1, 2, 4, 8} {
		got := []string{}
		e := parallelPools(keys, workers, func(k string) poolResult { return poolResult{input: 1, output: 1} }, func(k string, r poolResult) error { got = append(got, k); return nil })
		if e != nil || len(got) != len(keys) {
			t.Fatalf("worker=%d result count=%d err=%v", workers, len(got), e)
		}
		for i, k := range keys {
			if got[i] != k {
				t.Fatalf("worker=%d changed order at %d", workers, i)
			}
		}
	}
	for _, workers := range []int{2, 4} {
		processed := 0
		e := parallelPools(keys, workers, func(k string) poolResult {
			if k == keys[2] {
				return poolResult{err: errTestPool}
			}
			return poolResult{}
		}, func(k string, r poolResult) error { processed++; return nil })
		if e != errTestPool || processed != 2 {
			t.Fatalf("failure not fail-fast; processed=%d err=%v", processed, e)
		}
	}
}

var errTestPool = &testPoolError{}

type testPoolError struct{}

func (*testPoolError) Error() string { return "test pool failed" }
