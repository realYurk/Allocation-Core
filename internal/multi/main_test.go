package multi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join("..", "..", "testdata", "multi_sample")
	if _, err := os.Stat(root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "project")
	if err := filepath.Walk(root, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		dst := filepath.Join(dir, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0700)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		return os.WriteFile(dst, b, 0600)
	}); err != nil {
		t.Fatal(err)
	}
	name := "alloc_test"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join("..", "..")
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build core: %s: %v", out, e)
	}
	return dir, bin
}
func TestFourAccountEndToEnd(t *testing.T) {
	project, engine := fixture(t)
	out := filepath.Join(t.TempDir(), "out")
	r, e := Run(project, out, engine)
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != "PASS" || r.Input != "3000.00" || r.Output != "3000.00" || len(r.Details) != 4 || r.ResultRows != 12 || r.Groups != 4 {
		t.Fatalf("bad multi result: %#v", r)
	}
	for _, a := range r.Details {
		if a.Input != a.Output {
			t.Fatalf("not conserved: %#v", a)
		}
	}
	if _, e = os.Stat(filepath.Join(out, "results.csv")); e != nil {
		t.Fatal(e)
	}
}
func TestUnassignedAccountFailClosed(t *testing.T) {
	project, engine := fixture(t)
	f := filepath.Join(project, "assignments.csv")
	b, _ := os.ReadFile(f)
	b = []byte(strings.ReplaceAll(string(b), "Cost-A,material.json\n", ""))
	_ = os.WriteFile(f, b, 0600)
	out := filepath.Join(t.TempDir(), "out")
	_, e := Run(project, out, engine)
	if e == nil || !strings.Contains(e.Error(), "unassigned") {
		t.Fatalf("expected unassigned error, got %v", e)
	}
	if _, e = os.Stat(out); !os.IsNotExist(e) {
		t.Fatal("published failed output")
	}
}
func TestDuplicateAccountRejected(t *testing.T) {
	project, engine := fixture(t)
	f := filepath.Join(project, "assignments.csv")
	b, _ := os.ReadFile(f)
	b = append(b, []byte("Cost-A,revenue.json\n")...)
	_ = os.WriteFile(f, b, 0600)
	out := filepath.Join(t.TempDir(), "out")
	_, e := Run(project, out, engine)
	if e == nil || !strings.Contains(e.Error(), "duplicate assignment") {
		t.Fatalf("expected duplicate reject: %v", e)
	}
}
func TestUnsafeAccountRuleRejected(t *testing.T) {
	project, engine := fixture(t)
	f := filepath.Join(project, "templates", "material.json")
	var s map[string]any
	b, _ := os.ReadFile(f)
	_ = json.Unmarshal(b, &s)
	s["immutable_dimensions"] = []string{"period", "currency"}
	b, _ = json.Marshal(s)
	_ = os.WriteFile(f, b, 0600)
	_, e := Run(project, filepath.Join(t.TempDir(), "out"), engine)
	if e == nil || !strings.Contains(e.Error(), "immutable") {
		t.Fatalf("must reject cross-account rule: %v", e)
	}
}
func TestMissingAccountRowsRejected(t *testing.T) {
	project, engine := fixture(t)
	f := filepath.Join(project, "amounts.csv")
	b, _ := os.ReadFile(f)
	lines := strings.Split(string(b), "\n")
	s := ""
	for _, v := range lines {
		if !strings.Contains(v, "Cost-D") {
			s += v + "\n"
		}
	}
	_ = os.WriteFile(f, []byte(s), 0600)
	_, e := Run(project, filepath.Join(t.TempDir(), "out"), engine)
	if e == nil || !strings.Contains(e.Error(), "no input") {
		t.Fatalf("must reject missing account rows: %v", e)
	}
}
func TestBadDriverRejectedNoPublish(t *testing.T) {
	project, engine := fixture(t)
	_ = os.WriteFile(filepath.Join(project, "drivers", "material.csv"), []byte("period,department,sku,material\n2026-01,SharedPool,Item-A,0\n2026-01,SharedPool,Item-B,0\n"), 0600)
	out := filepath.Join(t.TempDir(), "out")
	_, e := Run(project, out, engine)
	if e == nil {
		t.Fatal("zero denominator should fail")
	}
	if _, e = os.Stat(out); !os.IsNotExist(e) {
		t.Fatal("failed output published")
	}
}

func TestCancellationBeforePublish(t *testing.T) {
	dir, engine := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := filepath.Join(t.TempDir(), "must-not-exist")
	_, err := RunContext(ctx, dir, out, engine)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wanted context canceled, got %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("canceled run published output: %v", err)
	}
}

func TestTemplateMutationDetectedBeforePublish(t *testing.T) {
	dir, _ := fixture(t)
	p := &project{dir: dir, stage: t.TempDir(), ctx: context.Background()}
	if err := p.initialize(); err != nil {
		t.Fatal(err)
	}
	if err := p.verifyControls(); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, "templates", "material.json")
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, append(b, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.verifyControls(); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("template mutation not detected: %v", err)
	}
}

func TestRunningEngineCancelledAndNoPartialPublish(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("slow shell stub uses /bin/sh")
	}
	dir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	sample := filepath.Join("..", "..", "testdata", "multi_sample")
	if err := filepath.Walk(sample, func(src string, fi os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(sample, src)
		if e != nil {
			return e
		}
		dst := filepath.Join(dir, rel)
		if fi.IsDir() {
			return os.MkdirAll(dst, 0700)
		}
		raw, e := os.ReadFile(src)
		if e != nil {
			return e
		}
		return os.WriteFile(dst, raw, 0600)
	}); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(t.TempDir(), "slow-engine.sh")
	marker := filepath.Join(t.TempDir(), "started")
	script := "#!/bin/sh\necho started > '" + marker + "'\nexec sleep 20\n"
	if err := os.WriteFile(stub, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := filepath.Join(t.TempDir(), "must-not-publish")
	complete := make(chan error, 1)
	go func() { _, err := RunContext(ctx, dir, out, stub); complete <- err }()
	deadline := time.After(6 * time.Second)
wait:
	for {
		if _, err := os.Stat(marker); err == nil {
			break wait
		}
		select {
		case err := <-complete:
			t.Fatalf("ended too early: %v", err)
		case <-deadline:
			t.Fatal("engine did not start")
		case <-time.After(15 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-complete:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cancel failed to stop child process")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("canceled run published output: %v", err)
	}
}
