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
