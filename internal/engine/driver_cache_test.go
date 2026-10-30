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
	if e := os.WriteFile(p, []byte("period,sku,units\n01,Item-A,3\n01,Item-B,1\n"), 0600); e != nil {
		t.Fatal(e)
	}
	dims := []string{"period", "sku"}
	st := Stage{Target: map[string]Target{"period": {Mode: "SOURCE"}, "sku": {Mode: "INCLUDE", Include: []string{"Item-A", "Item-B"}}}}
	a := Allocation{ID: "sales", WeightField: "units"}
	cache := filepath.Join(dir, "persistent-index")
	first, e := buildCachedDiskDriverIndex(p, "units", filepath.Join(dir, "run1"), cache, a, st, dims, nil)
	if e != nil {
		t.Fatal(e)
	}
	if first.cacheHit {
		t.Fatal("fresh cache reported hit")
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
	if !second.cacheHit {
		t.Fatal("validated entry should report cache hit")
	}
	if second.ix.disk.path != firstPath {
		t.Fatalf("cache miss on exact input: %s vs %s", second.ix.disk.path, firstPath)
	}
	second.ix.disk.close()
	// Modifying even one weight must force an entirely different immutable index.
	if e = os.WriteFile(p, []byte("period,sku,units\n01,Item-A,5\n01,Item-B,1\n"), 0600); e != nil {
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
	st.Target["sku"] = Target{Mode: "INCLUDE", Include: []string{"Item-B"}}
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

// The sorted index is a function of source bytes, weight field, driver
// filters, target modes and dimensions. IDs, shares and negative policies are
// applied after index lookup and must not fragment the cache.
func TestCacheReusesAcrossDifferentRuleLabels(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "weights.csv")
	if err := os.WriteFile(file, []byte("period,sku,units\n01,A,3\n01,B,1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	st := Stage{Target: map[string]Target{"period": {Mode: "SOURCE"}, "sku": {Mode: "INCLUDE", Include: []string{"A", "B"}}}}
	a := Allocation{ID: "cost_A", Share: "1", WeightField: "units", NegativePolicy: "REJECT"}
	b := Allocation{ID: "cost_B", Share: "0.5", WeightField: "units", NegativePolicy: "ALLOW_SIGNED"}
	one, e := buildCachedDiskDriverIndex(file, "units", filepath.Join(dir, "first"), filepath.Join(dir, "cache"), a, st, []string{"period", "sku"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if one.cacheHit {
		t.Fatal("first should build")
	}
	firstPath := one.ix.disk.path
	one.ix.disk.close()
	two, e := buildCachedDiskDriverIndex(file, "units", filepath.Join(dir, "second"), filepath.Join(dir, "cache"), b, st, []string{"period", "sku"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !two.cacheHit || two.ix.disk.path != firstPath || two.alloc.ID != "cost_B" {
		t.Fatal("different allocation identity should reuse same immutable index with correct runtime rule")
	}
	two.ix.disk.close()
	b.DriverFilters = map[string]Filter{"sku": {Include: []string{"A"}}}
	three, e := buildCachedDiskDriverIndex(file, "units", filepath.Join(dir, "third"), filepath.Join(dir, "cache"), b, st, []string{"period", "sku"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if three.cacheHit || three.ix.disk.path == firstPath {
		t.Fatal("changed driver eligibility must force a new index")
	}
	three.ix.disk.close()
}
