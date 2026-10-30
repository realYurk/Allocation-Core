package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkersProduceIdenticalEvidence(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "examples")
	for _, name := range []string{"manufacturing", "cosmetics", "split_merge"} {
		t.Run(name, func(t *testing.T) {
			rule := filepath.Join(root, name, "rule.json")
			dir := t.TempDir()
			one := filepath.Join(dir, "one")
			many := filepath.Join(dir, "many")
			r1, e1 := RunWithOptions(rule, one, RunOptions{Workers: 1})
			r4, e4 := RunWithOptions(rule, many, RunOptions{Workers: 4})
			if e1 != nil || e4 != nil {
				t.Fatalf("single %v parallel %v", e1, e4)
			}
			if r1.InputAmount != r4.InputAmount || r1.OutputAmount != r4.OutputAmount || r1.PoolChecks != r4.PoolChecks {
				t.Fatal("summary different")
			}
			for _, fileName := range []string{"results.csv", "ledger.csv", "reconciliation.csv", "source_to_pool.csv"} {
				a, e := os.ReadFile(filepath.Join(one, fileName))
				if e != nil {
					t.Fatal(e)
				}
				b, e := os.ReadFile(filepath.Join(many, fileName))
				if e != nil {
					t.Fatal(e)
				}
				if !bytes.Equal(a, b) {
					t.Errorf("different output: %s", fileName)
				}
			}
		})
	}
}
func TestParallelBoundedAndOrdered(t *testing.T) {
	var current, peak int64
	keys := make([]string, 30)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%02d", i)
	}
	got := []string{}
	e := parallelPools(keys, 4, func(k string) poolResult {
		active := atomic.AddInt64(&current, 1)
		for {
			prev := atomic.LoadInt64(&peak)
			if active <= prev || atomic.CompareAndSwapInt64(&peak, prev, active) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		atomic.AddInt64(&current, -1)
		return poolResult{}
	}, func(k string, r poolResult) error { got = append(got, k); return nil })
	if e != nil {
		t.Fatal(e)
	}
	if peak <= 1 || peak > 4 {
		t.Fatalf("unexpected concurrency %d", peak)
	}
	for i, k := range got {
		if k != keys[i] {
			t.Fatalf("non deterministic order at %d", i)
		}
	}
}
func TestParallelFailureDoesNotPublish(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	original := filepath.Join(filepath.Dir(file), "..", "..", "examples", "cosmetics")
	raw, err := os.ReadFile(filepath.Join(original, "rule.json"))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := ParseSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	spec.Datasets.Amounts["expenses"] = filepath.Join(original, "amounts.csv")
	spec.Datasets.Drivers["sales"] = filepath.Join(temp, "zero.csv")
	driver := []byte("period,region,brand,sku,units\n2026-01,华东,品牌甲,Item-A,0\n2026-01,华东,品牌甲,Item-B,0\n")
	if err = os.WriteFile(spec.Datasets.Drivers["sales"], driver, 0600); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	rule := filepath.Join(temp, "rule.json")
	if err = os.WriteFile(rule, b, 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(temp, "result")
	_, err = RunWithOptions(rule, out, RunOptions{Workers: 4})
	if err == nil {
		t.Fatal("expected zero denominator error")
	}
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("failed job published output: %v", err)
	}
}
