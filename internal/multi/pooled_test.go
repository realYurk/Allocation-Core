package multi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPooledWorkersProduceIdenticalFinancialFiles(t *testing.T) {
	dir, exe := fixture(t)
	config := filepath.Join(dir, "project.json")
	original, e := os.ReadFile(config)
	if e != nil {
		t.Fatal(e)
	}
	standalone := filepath.Join(t.TempDir(), "standalone")
	if _, e = Run(dir, standalone, exe); e != nil {
		t.Fatalf("isolated: %v", e)
	}
	raw := strings.Replace(string(original), "\"jobs\": 2,", "\"jobs\": 2,\n  \"reuse_workers\": true,", 1)
	if raw == string(original) {
		t.Fatal("cannot switch worker mode in fixture")
	}
	if e = os.WriteFile(config, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	pooled := filepath.Join(t.TempDir(), "pooled")
	if _, e = Run(dir, pooled, exe); e != nil {
		t.Fatalf("pooled: %v", e)
	}
	for _, file := range []string{"results.csv", "account_reconciliation.csv"} {
		a, e := os.ReadFile(filepath.Join(standalone, file))
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(filepath.Join(pooled, file))
		if e != nil {
			t.Fatal(e)
		}
		if string(a) != string(b) {
			t.Fatalf("worker mode changed %s financial output", file)
		}
	}
}
func TestPooledWorkersFailClosedAndNoPublishedOutput(t *testing.T) {
	dir, exe := fixture(t)
	config := filepath.Join(dir, "project.json")
	b, e := os.ReadFile(config)
	if e != nil {
		t.Fatal(e)
	}
	b = []byte(strings.Replace(string(b), "\"jobs\": 2,", "\"jobs\": 2,\n  \"reuse_workers\": true,", 1))
	if e = os.WriteFile(config, b, 0600); e != nil {
		t.Fatal(e)
	}
	// The input shape is valid; only runtime eligibility (all-zero denominator)
	// can detect this. The pooled executor must not publish partial results.
	for _, filename := range []string{"material.csv", "revenue.csv"} {
		path := filepath.Join(dir, "drivers", filename)
		data, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		text := string(data)
		text = strings.ReplaceAll(text, ",5\n", ",0\n")
		text = strings.ReplaceAll(text, ",10\n", ",0\n")
		text = strings.ReplaceAll(text, ",20\n", ",0\n")
		text = strings.ReplaceAll(text, ",50\n", ",0\n")
		if e = os.WriteFile(path, []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
	}
	out := filepath.Join(t.TempDir(), "not-published")
	_, e = Run(dir, out, exe)
	if e == nil {
		t.Fatal("expected runtime zero-denominator failure")
	}
	if _, e = os.Stat(out); !os.IsNotExist(e) {
		t.Fatal("failed batch published partial directory")
	}
}
func TestPooledWorkerHonorsPreCanceledContext(t *testing.T) {
	dir, exe := fixture(t)
	config := filepath.Join(dir, "project.json")
	b, _ := os.ReadFile(config)
	b = []byte(strings.Replace(string(b), "\"jobs\": 2,", "\"jobs\": 2,\n  \"reuse_workers\": true,", 1))
	os.WriteFile(config, b, 0600)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := filepath.Join(t.TempDir(), "out")
	_, e := RunContext(ctx, dir, out, exe)
	if e == nil {
		t.Fatal("cancellation must fail")
	}
	if _, e = os.Stat(out); !os.IsNotExist(e) {
		t.Fatal("canceled run published output")
	}
}
