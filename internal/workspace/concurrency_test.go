package workspace

import (
	"allocationcore/internal/engine"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestConcurrentSubmissionsSameRule(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	rule := filepath.Join(filepath.Dir(file), "..", "..", "examples", "manufacturing", "rule.json")
	root := filepath.Join(t.TempDir(), "workspace")
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for k := 0; k < 6; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, e := SubmitWithOptions(rule, root, engine.RunOptions{Workers: 2})
			if e != nil {
				errs <- e
				return
			}
			if e = Verify(root, j.ID); e != nil {
				errs <- e
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	jobs, e := List(root)
	if e != nil {
		t.Fatal(e)
	}
	if len(jobs) != 6 {
		t.Fatalf("got %d jobs", len(jobs))
	}
	for _, j := range jobs {
		if j.State != Succeeded || j.Workers != 2 {
			t.Errorf("bad job %+v", j)
		}
	}
	rules, e := ListRules(root)
	if e != nil {
		t.Fatal(e)
	}
	if len(rules) != 1 {
		t.Fatalf("expected one published revision, got %d", len(rules))
	}
}
