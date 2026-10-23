package engine

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDriverDiskIndexEqualsMemoryMixedSparse(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "driver.csv")
	// Sparse: cost account is deliberately absent from drivers.
	text := "period,plant,family,sku,units\n" +
		"01,厂甲,系列甲,商品A,3\n01,厂甲,系列甲,商品B,9\n" +
		"02,厂甲,系列甲,商品A,7\n01,厂乙,系列甲,商品A,11\n" +
		"01,厂甲,系列乙,商品C,5\n01,厂甲,系列甲,试用品,99\n"
	if e := os.WriteFile(csv, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
	dims := []string{"period", "plant", "family", "sku", "account"}
	st := Stage{Target: map[string]Target{"period": {Mode: "SOURCE"}, "plant": {Mode: "SOURCE"}, "family": {Mode: "SOURCE"}, "sku": {Mode: "INCLUDE_EXCLUDE", Include: []string{"商品A", "商品B", "商品C", "试用品"}, Exclude: []string{"试用品"}}, "account": {Mode: "SOURCE"}}}
	alloc := Allocation{ID: "units", WeightField: "units", NegativePolicy: "REJECT"}
	rows, e := ReadDrivers(csv, dims, "units")
	if e != nil {
		t.Fatal(e)
	}
	mem, e := buildDriverIndex(rows, alloc, st, dims, nil)
	if e != nil {
		t.Fatal(e)
	}
	disk, e := buildDiskDriverIndex(csv, "units", filepath.Join(dir, "disk"), alloc, st, dims, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer disk.ix.disk.close()
	for _, pool := range []map[string]string{
		{"period": "01", "plant": "厂甲", "family": "系列甲", "sku": poolMember, "account": "能源费"},
		{"period": "01", "plant": "厂甲", "family": "系列甲", "sku": poolMember, "account": "物流费"},
		{"period": "02", "plant": "厂甲", "family": "系列甲", "sku": poolMember, "account": "能源费"},
		{"period": "01", "plant": "厂乙", "family": "系列甲", "sku": poolMember, "account": "能源费"},
	} {
		a, w, k, e := candidates(mem, st, dims, pool)
		if e != nil {
			t.Fatal(e)
		}
		b, x, y, e := candidates(disk, st, dims, pool)
		if e != nil {
			t.Fatal(e)
		}
		if fmt.Sprint(a, w, k) != fmt.Sprint(b, x, y) {
			t.Fatalf("mismatch for pool %v\nmem=%v %v %v\ndisk=%v %v %v", pool, a, w, k, b, x, y)
		}
	}
}
func TestDriverDiskIndexManyChunks(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "mass.csv")
	f, e := os.Create(csv)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.WriteString("period,sku,units\n")
	for i := 0; i < 27000; i++ {
		_, _ = f.WriteString(fmt.Sprintf("%02d,S%05d,1\n", i%3, i))
	}
	f.Close()
	dims := []string{"period", "sku"}
	st := Stage{Target: map[string]Target{"period": {Mode: "SOURCE"}, "sku": {Mode: "INCLUDE", Include: []string{"ALL"}}}}
	alloc := Allocation{WeightField: "units"}
	h := map[string]map[string][]string{"sku": {"ALL": {}}}
	for i := 0; i < 27000; i++ {
		h["sku"]["ALL"] = append(h["sku"]["ALL"], fmt.Sprintf("S%05d", i))
	}
	ref, e := buildDiskDriverIndex(csv, "units", filepath.Join(dir, "disk"), alloc, st, dims, h)
	if e != nil {
		t.Fatal(e)
	}
	defer ref.ix.disk.close()
	for i := 0; i < 3; i++ {
		k := fmt.Sprintf("[\"%02d\"]", i)
		rows, e := ref.ix.disk.load(k)
		if e != nil {
			t.Fatal(e)
		}
		if len(rows) != 9000 {
			t.Fatalf("key %s rows %d", k, len(rows))
		}
	}
	data, e := os.ReadFile(ref.ix.disk.path)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Count(string(data), "\n") != 27000 {
		t.Fatal("count mismatch")
	}
	_ = sha256.Sum256(data)
}
func TestDriverDiskIndexRejectsUnknownFilterColumns(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.csv")
	os.WriteFile(p, []byte("sku,units\nA,1\n"), 0600)
	a := Allocation{WeightField: "units", DriverFilters: map[string]Filter{"brand": {Include: []string{"X"}}}}
	st := Stage{Target: map[string]Target{"sku": {Mode: "INCLUDE", Include: []string{"A"}}}}
	_, e := buildDiskDriverIndex(p, "units", filepath.Join(dir, "disk"), a, st, []string{"sku", "brand"}, nil)
	if e == nil || !strings.Contains(e.Error(), "missing column") {
		t.Fatalf("expected missing filter column, got %v", e)
	}
}
