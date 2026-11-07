package acceptance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if e := os.WriteFile(p, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestExactComparisonAggregatesAndReorders(t *testing.T) {
	d := t.TempDir()
	actual := write(t, d, "actual.csv", "terminal_stage,row_id,account,period,sku,amount\nS,1,A,2026-01,P1,6.00\nS,2,A,2026-01,P1,4.00\nS,3,A,2026-01,P2,10.00\n")
	ref := write(t, d, "ref.csv", "sku,account,value,period\nP2,A,10.00,2026-01\nP1,A,10.00,2026-01\n")
	out := filepath.Join(d, "result")
	r, e := Compare(context.Background(), actual, ref, out, Options{Precision: 2})
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != "MATCH" || r.Matched != 2 || r.ActualRows != 3 || r.ReferenceRows != 2 || len(r.Accounts) != 1 || r.ActualSHA256 == "" || r.ActualTotal != "20.00" {
		t.Fatalf("bad result %#v", r)
	}
	if _, e = os.Stat(filepath.Join(out, "summary.json")); e != nil {
		t.Fatal(e)
	}
}

func TestIndependentDuplicatePoliciesAndNoPartialPublication(t *testing.T) {
	d := t.TempDir()
	actual := write(t, d, "actual.csv", "account,sku,amount\nA,X,6.00\nA,X,4.00\n")
	reference := write(t, d, "reference.csv", "account,sku,amount\nA,X,10.00\n")
	result, e := Compare(context.Background(), actual, reference, filepath.Join(d, "reference-only"), Options{Precision: 2, RejectReferenceDuplicates: true})
	if e != nil || result.Status != "MATCH" || result.DuplicatePolicy != "REJECT_REFERENCE" || result.ActualDuplicateRows != 1 || result.ReferenceDuplicateRows != 0 {
		t.Fatalf("legitimate actual factors: %+v %v", result, e)
	}
	badReference := write(t, d, "bad-reference.csv", "account,sku,amount\nA,X,5.00\nA,X,5.00\n")
	for _, tc := range []struct {
		name              string
		actual, reference string
		options           Options
	}{
		{"reference", actual, badReference, Options{Precision: 2, RejectReferenceDuplicates: true}},
		{"actual", actual, reference, Options{Precision: 2, RejectActualDuplicates: true}},
		{"legacy", actual, reference, Options{Precision: 2, RejectDuplicates: true}},
	} {
		out := filepath.Join(d, tc.name)
		_, e := Compare(context.Background(), tc.actual, tc.reference, out, tc.options)
		if e == nil || !strings.Contains(e.Error(), "duplicate full coordinate") {
			t.Fatalf("%s not rejected: %v", tc.name, e)
		}
		if _, e = os.Stat(out); !os.IsNotExist(e) {
			t.Fatalf("%s partial output published", tc.name)
		}
	}
	result, e = Compare(context.Background(), reference, badReference, filepath.Join(d, "actual-only"), Options{Precision: 2, RejectActualDuplicates: true})
	if e != nil || result.Status != "MATCH" || result.DuplicatePolicy != "REJECT_ACTUAL" || result.ReferenceDuplicateRows != 1 {
		t.Fatalf("actual-only: %+v %v", result, e)
	}
}
func TestMismatchesNoOffsettingFalsePass(t *testing.T) {
	d := t.TempDir()
	a := write(t, d, "a.csv", "account,sku,amount\nA,P1,60.00\nA,P2,40.00\nA,P3,0.00\n")
	b := write(t, d, "b.csv", "account,sku,amount\nA,P1,70.00\nA,P2,30.00\nA,P4,0.00\n")
	r, e := Compare(context.Background(), a, b, filepath.Join(d, "result"), Options{Precision: 2})
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != "DIFFERENCES" || r.Different != 2 || r.Missing != 1 || r.Extra != 1 || r.ActualTotal != "100.00" || r.ReferenceTotal != "100.00" {
		t.Fatalf("bad result %#v", r)
	}
}
func TestInvalidDoesNotPublish(t *testing.T) {
	d := t.TempDir()
	a := write(t, d, "a.csv", "account,sku,amount\nA,P1,1.0\n")
	b := write(t, d, "b.csv", "account,sku,amount\nA,P1,broken\n")
	out := filepath.Join(d, "out")
	_, e := Compare(context.Background(), a, b, out, Options{Precision: 2})
	if e == nil {
		t.Fatal("should block invalid amount")
	}
	if _, e = os.Stat(out); !os.IsNotExist(e) {
		t.Fatal("published failed report")
	}
	// Reject changes in the business coordinate schema, not just grand total.
	b = write(t, d, "b.csv", "account,sku,location,amount\nA,P1,L1,1.00\n")
	_, e = Compare(context.Background(), a, b, out, Options{Precision: 2})
	if e == nil || !strings.Contains(e.Error(), "dimensions differ") {
		t.Fatal(e)
	}
}
func TestDuplicateBusinessColumnsAndCancellation(t *testing.T) {
	d := t.TempDir()
	a := write(t, d, "a.csv", "account,account,amount\nA,A,1.00\n")
	_, e := Compare(context.Background(), a, a, filepath.Join(d, "out"), Options{Precision: 2})
	if e == nil {
		t.Fatal("duplicate header")
	}
	a = write(t, d, "a.csv", "account,amount\nA,1.00\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := filepath.Join(d, "out")
	_, e = Compare(ctx, a, a, out, Options{Precision: 2})
	if e == nil {
		t.Fatal("should cancel")
	}
	if _, e = os.Stat(out); !os.IsNotExist(e) {
		t.Fatal("published canceled report")
	}
}
func TestToleranceAndNegativeAmounts(t *testing.T) {
	d := t.TempDir()
	a := write(t, d, "a.csv", "account,amount\nA,-12.34\n")
	b := write(t, d, "b.csv", "account,value\nA,-12.35\n")
	r, e := Compare(context.Background(), a, b, filepath.Join(d, "out"), Options{Precision: 2, Tolerance: "0.01"})
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != "MATCH" || r.Matched != 1 {
		t.Fatalf("%#v", r)
	}
}

func TestV7DuplicateCoordinatePolicy(t *testing.T) {
	d := t.TempDir()
	a := write(t, d, "a.csv", "period,account,sku,amount\n2026-01,A,P1,6.00\n2026-01,A,P1,4.00\n")
	ref := write(t, d, "ref.csv", "sku,period,account,amount\nP1,2026-01,A,10.00\n")
	r, e := Compare(context.Background(), a, ref, filepath.Join(d, "aggregate"), Options{Precision: 2})
	if e != nil || r.Status != "MATCH" || r.ActualDuplicateRows != 1 || r.ReferenceDuplicateRows != 0 || r.DuplicatePolicy != "AGGREGATE_WITH_COUNTS" {
		t.Fatalf("duplicate accounting: %+v error=%v", r, e)
	}
	strict := filepath.Join(d, "strict")
	_, e = Compare(context.Background(), a, ref, strict, Options{Precision: 2, RejectDuplicates: true})
	if e == nil || !strings.Contains(e.Error(), "duplicate") {
		t.Fatalf("strict did not reject: %v", e)
	}
	if _, e = os.Stat(strict); !os.IsNotExist(e) {
		t.Fatal("published strict duplicate result")
	}
}
