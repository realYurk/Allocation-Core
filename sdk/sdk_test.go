package sdk

import (
	"allocationcore/internal/demo"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if _, err := demo.Generate(dir); err != nil {
		t.Fatal(err)
	}
	rule := filepath.Join(dir, "examples", "manufacturing", "rule.json")
	b, err := os.ReadFile(filepath.Join("..", "examples", "manufacturing", "rule.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(rule, b, 0600); err != nil {
		t.Fatal(err)
	}
	return dir, rule
}
func TestSDKCoreEndToEnd(t *testing.T) {
	dir, rule := fixture(t)
	in, err := Inspect(rule)
	if err != nil || in.Status != "PASS" {
		t.Fatalf("%+v %v", in, err)
	}
	// Hand-calculated reference: 1200 → families 400/800 → four SKUs.
	reference := filepath.Join(dir, "independent-reference.csv")
	ref := "period,plant,brand,family,sku,account,currency,amount\n" +
		"2026-01,工厂甲,品牌甲,系列甲,Item-A,Cost-A,CUR,100.00\n" +
		"2026-01,工厂甲,品牌甲,系列甲,Item-B,Cost-A,CUR,300.00\n" +
		"2026-01,工厂甲,品牌甲,系列乙,商品C,Cost-A,CUR,400.00\n" +
		"2026-01,工厂甲,品牌甲,系列乙,商品D,Cost-A,CUR,400.00\n"
	if err = os.WriteFile(reference, []byte(ref), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []Mode{Memory, Disk} {
		out := filepath.Join(dir, string(mode))
		result, err := Run(RunRequest{RulePath: rule, OutputDir: out, Mode: mode, Workers: 2})
		if err != nil || result.Status != "PASS" || result.InputAmount != "1200.00" || result.OutputAmount != "1200.00" {
			t.Fatalf("%s %+v %v", mode, result, err)
		}
		check, e := Compare(context.Background(), filepath.Join(out, "results.csv"), reference, filepath.Join(dir, string(mode)+"-reference-check"), ReconciliationOptions{Precision: 2, RejectDuplicates: true})
		if e != nil || check.Status != "MATCH" || check.Matched != 4 {
			t.Fatalf("independent reference %s: %+v %v", mode, check, e)
		}
		if _, err = Run(RunRequest{RulePath: rule, OutputDir: out, Mode: mode}); err == nil {
			t.Fatal("existing output overwritten")
		}
	}
	// Compare two independent, explicitly written inputs with equal totals and
	// different coordinates. A total-only implementation would falsely pass.
	a := filepath.Join(dir, "actual.csv")
	b := filepath.Join(dir, "reference.csv")
	if err = os.WriteFile(a, []byte("account,sku,amount\nE1,A,60.00\nE1,B,40.00\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(b, []byte("account,sku,amount\nE1,A,70.00\nE1,B,30.00\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Compare(context.Background(), a, b, filepath.Join(dir, "compare"), ReconciliationOptions{Precision: 2, RejectDuplicates: true})
	if err != nil || r.Status != "DIFFERENCES" || r.Different != 2 || r.ActualTotal != r.ReferenceTotal {
		t.Fatalf("%+v %v", r, err)
	}
}
func TestSDKRejectsInvalidConfiguration(t *testing.T) {
	for _, req := range []RunRequest{{}, {RulePath: "r", OutputDir: "o", Mode: "unknown"}, {RulePath: "r", OutputDir: "o", DriverDiskIndex: true}, {RulePath: "r", OutputDir: "o", Mode: Disk, Resume: true}, {RulePath: "r", OutputDir: "o", Mode: Disk, DriverCacheDir: "cache"}, {RulePath: "r", OutputDir: "o", Workers: 129}} {
		_, err := Run(req)
		var e *Error
		if !errors.As(err, &e) || e.Code != "INVALID_REQUEST" {
			t.Fatalf("%+v %v", req, err)
		}
	}
	if _, err := Compare(nil, "a", "b", "c", ReconciliationOptions{}); err == nil {
		t.Fatal("nil context accepted")
	}
}
func TestSDKInspectBlocksExcessPrecisionAndDoesNotModifyInputs(t *testing.T) {
	dir, rule := fixture(t)
	path := filepath.Join(dir, "examples", "manufacturing", "amounts.csv")
	data := []byte("period,plant,brand,family,sku,account,currency,amount,row_id\n2026-01,P,B,F,S,E1,CUR,1.001,X\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Inspect(rule)
	if err != nil || r.Status != "BLOCK" {
		t.Fatalf("%+v %v", r, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("input changed")
	}
}
func TestSDKComparisonCancellation(t *testing.T) {
	dir, _ := fixture(t)
	a := filepath.Join(dir, "a.csv")
	_ = os.WriteFile(a, []byte("account,amount\nA,1.00\n"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := filepath.Join(dir, "canceled")
	_, err := Compare(ctx, a, a, out, ReconciliationOptions{Precision: 2})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("canceled report published")
	}
}
