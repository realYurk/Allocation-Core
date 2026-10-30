package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMultiBoundedQueueAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix shell stub; windows smoke exercises real executable")
	}
	home := t.TempDir()
	root := filepath.Join(home, "multi")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"one", "two"} {
		if e := copyFixture(filepath.Join("..", "..", "testdata", "multi_sample"), filepath.Join(root, name)); e != nil {
			t.Fatal(e)
		}
	}
	multi := filepath.Join(t.TempDir(), "allocation_multi")
	cmd := exec.Command("go", "build", "-o", multi, ".")
	cmd.Dir = filepath.Join("..", "..")
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build Multi: %s: %v", out, e)
	}
	core := filepath.Join(t.TempDir(), "slow-core.sh")
	started := filepath.Join(t.TempDir(), "started")
	if e := os.WriteFile(core, []byte("#!/bin/sh\necho run > '"+started+"'\nexec sleep 20\n"), 0700); e != nil {
		t.Fatal(e)
	}
	app := &Application{data: home, engine: core}
	x := &MultiApp{parent: app, root: root, program: multi, jobs: map[string]*Job{}, busy: map[string]bool{}, slots: make(chan struct{}, 1), sem: make(chan struct{}, 1), cancels: map[string]context.CancelFunc{}}
	post := func(project string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/multi/run", strings.NewReader(`{"name":"`+project+`"}`))
		x.run(w, r)
		return w
	}
	first := post("one")
	if first.Code != 202 {
		t.Fatalf("first should be queued: %d %s", first.Code, first.Body.String())
	}
	var firstJob Job
	if e := json.Unmarshal(first.Body.Bytes(), &firstJob); e != nil {
		t.Fatal(e)
	}
	full := post("two")
	if full.Code != 429 {
		t.Fatalf("queue must reject when full, got %d: %s", full.Code, full.Body.String())
	}
	deadline := time.After(5 * time.Second)
	for {
		if _, e := os.Stat(started); e == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("fake core did not start")
		case <-time.After(15 * time.Millisecond):
		}
	}
	w := httptest.NewRecorder()
	x.jobDetail(w, httptest.NewRequest(http.MethodPost, "/api/multi/jobs/"+firstJob.ID+"/cancel", strings.NewReader(`{}`)))
	if w.Code != 202 {
		t.Fatalf("cancel should be accepted: %d %s", w.Code, w.Body.String())
	}
	deadline = time.After(4 * time.Second)
	for {
		x.mu.Lock()
		s := x.jobs[firstJob.ID].Status
		x.mu.Unlock()
		if s == "CANCELED" {
			break
		}
		if s == "PASS" || s == "FAILED" {
			t.Fatalf("unexpected final status: %s", s)
		}
		select {
		case <-deadline:
			t.Fatal("cancel not propagated to Core")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if _, e := os.Stat(filepath.Join(home, "multi-jobs", firstJob.ID, "results.csv")); !os.IsNotExist(e) {
		t.Fatalf("canceled job published results: %v", e)
	}
	if _, e := os.Stat(filepath.Join(home, "multi-jobs", ".cancel-"+firstJob.ID)); !os.IsNotExist(e) {
		t.Fatalf("cancel marker not cleaned: %v", e)
	}
}
