package engine_test

import (
	"allocationcore/internal/engine"
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// An independent, human-derived expected answer for a synthetic 2-stage
// allocation. The reference amounts DO NOT come from engine-generated files.
// 1200 -> families (hours 1:2) = 400 / 800; then products (units 1:3, 2:2).
func TestV7EndToEndIndependentBusinessOracle(t *testing.T) {
	d := t.TempDir()
	rule, err := os.ReadFile(filepath.Join("..", "..", "examples", "manufacturing", "rule.json"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"amounts.csv": "period,plant,brand,family,sku,account,currency,amount,row_id\n2026-01,工厂甲,Unassigned,Unassigned,Unassigned,Cost-A,CUR,1200.00,S001\n",
		"labor.csv":   "period,plant,family,hours\n2026-01,工厂甲,系列甲,1\n2026-01,工厂甲,系列乙,2\n",
		"sales.csv":   "period,plant,family,sku,units\n2026-01,工厂甲,系列甲,Item-A,1\n2026-01,工厂甲,系列甲,Item-B,3\n2026-01,工厂甲,系列乙,商品C,2\n2026-01,工厂甲,系列乙,商品D,2\n",
		"rule.json":   string(rule),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{"Item-A": "100.00", "Item-B": "300.00", "商品C": "400.00", "商品D": "400.00"}
	var comparison []byte
	for _, opt := range []struct {
		name string
		opts engine.RunOptions
	}{
		{"memory1", engine.RunOptions{Workers: 1}},
		{"memory4", engine.RunOptions{Workers: 4}},
		{"disk4", engine.RunOptions{Workers: 2, DiskPartitions: 4}},
	} {
		t.Run(opt.name, func(t *testing.T) {
			out := filepath.Join(d, opt.name)
			var s engine.Summary
			var err error
			if opt.opts.DiskPartitions > 0 {
				s, err = engine.RunDiskWithOptions(filepath.Join(d, "rule.json"), out, opt.opts)
			} else {
				s, err = engine.RunWithOptions(filepath.Join(d, "rule.json"), out, opt.opts)
			}
			if err != nil || s.Status != "PASS" {
				t.Fatalf("run %#v error=%v", s, err)
			}
			raw, e := os.ReadFile(filepath.Join(out, "results.csv"))
			if e != nil {
				t.Fatal(e)
			}
			rd := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(raw), "\ufeff")))
			rows, e := rd.ReadAll()
			if e != nil {
				t.Fatal(e)
			}
			skuIdx, amountIdx := -1, -1
			for i, x := range rows[0] {
				if x == "sku" {
					skuIdx = i
				}
				if x == "amount" {
					amountIdx = i
				}
			}
			if skuIdx < 0 || amountIdx < 0 {
				t.Fatal("missing business coordinate/value")
			}
			got := map[string]string{}
			for _, r := range rows[1:] {
				if _, exists := got[r[skuIdx]]; exists {
					t.Fatalf("unexpected repeated target %v", r)
				}
				got[r[skuIdx]] = r[amountIdx]
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("independent oracle failed: got=%v expected=%v", got, want)
			}
			if comparison == nil {
				comparison = raw
			} else if !bytes.Equal(comparison, raw) {
				t.Fatal("executor/worker mode changed exact result bytes")
			}
		})
	}
}
