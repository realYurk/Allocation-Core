package engine

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDriverContentAddressedCache(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "driver.csv")
	if e := os.WriteFile(p, []byte("period,sku,units\n01,商品A,3\n01,商品B,1\n"), 0600); e != nil {
		t.Fatal(e)
	}
	dims := []string{"period", "sku"}
	st := Stage{Target: map[string]Target{"period": {Mode: "SOURCE"}, "sku": {Mode: "INCLUDE", Include: []string{"商品A", "商品B"}}}}
	a := Allocation{ID: "sales", WeightField: "units"}
	cache := filepath.Join(dir, "persistent-index")
	first, e := buildCachedDiskDriverIndex(p, "units", filepath.Join(dir, "run1"), cache, a, st, dims, nil)
	if e != nil {
		t.Fatal(e)
	}
	firstPath := first.ix.disk.path
	var rowCount int
	e = first.ix.disk.forEach(`["01"]`, func(r DriverRow) error { rowCount++; return nil })
	if e != nil || rowCount != 2 {
		t.Fatalf("first rows %d %v", rowCount, e)
	}
	first.ix.disk.close()
	if _, e = os.Stat(firstPath); e != nil {
		t.Fatalf("persistent cache removed: %v", e)
	}
	second, e := buildCachedDiskDriverIndex(p, "units", filepath.Join(dir, "run2"), cache, a, st, dims, nil)
	if e != nil {
		t.Fatal(e)
	}
	if second.ix.disk.path != firstPath {
		t.Fatalf("cache miss on exact input: %s vs %s", second.ix.disk.path, firstPath)
	}
	second.ix.disk.close()
	// Modifying even one weight must force an entirely different immutable index.
	if e = os.WriteFile(p, []byte("period,sku,units\n01,商品A,5\n01,商品B,1\n"), 0600); e != nil {
		t.Fatal(e)
	}
	third, e := buildCachedDiskDriverIndex(p, "units", filepath.Join(dir, "run3"), cache, a, st, dims, nil)
	if e != nil {
		t.Fatal(e)
	}
	if third.ix.disk.path == firstPath {
		t.Fatal("stale driver index reused after changed data")
	}
	third.ix.disk.close()
	// Changing the eligibility/target also invalidates the semantic cache key.
	st.Target["sku"] = Target{Mode: "INCLUDE", Include: []string{"商品B"}}
	fourth, e := buildCachedDiskDriverIndex(p, "units", filepath.Join(dir, "run4"), cache, a, st, dims, nil)
	if e != nil {
		t.Fatal(e)
	}
	if fourth.ix.disk.path == third.ix.disk.path {
		t.Fatal("cache reused with changed target filter")
	}
	fourth.ix.disk.close()
}

func TestDriverCacheParallelBuild(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "d.csv")
	if e := os.WriteFile(p, []byte("period,sku,units\n01,A,1\n01,B,2\n"), 0600); e != nil {
		t.Fatal(e)
	}
	st := Stage{Target: map[string]Target{"period": {Mode: "SOURCE"}, "sku": {Mode: "INCLUDE", Include: []string{"A", "B"}}}}
	a := Allocation{ID: "a", WeightField: "units"}
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	paths := make(chan string, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, e := buildCachedDiskDriverIndex(p, "units", filepath.Join(dir, "scratch_"+string(rune('0'+i))), filepath.Join(dir, "cache"), a, st, []string{"period", "sku"}, nil)
			if e != nil {
				errs <- e
				return
			}
			paths <- r.ix.disk.path
			r.ix.disk.close()
		}(i)
	}
	wg.Wait()
	close(errs)
	close(paths)
	for e := range errs {
		t.Fatal(e)
	}
	previous := ""
	n := 0
	for p := range paths {
		if previous != "" && p != previous {
			t.Fatal("different cache paths from concurrent build")
		}
		previous = p
		n++
	}
	if n != 5 {
		t.Fatalf("only %d successful builds", n)
	}
}
