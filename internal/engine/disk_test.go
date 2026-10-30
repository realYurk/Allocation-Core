package engine

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func readCanonical(t *testing.T, file string) []string {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	all, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]string, 0, len(all)-1)
	for _, row := range all[1:] {
		b := []byte{}
		for _, v := range row {
			b = append(b, []byte(v)...)
			b = append(b, 0)
		}
		rows = append(rows, string(b))
	}
	sort.Strings(rows)
	return rows
}
func TestDiskMatchesMemoryForMultiStageAndBranches(t *testing.T) {
	cases := []string{"manufacturing", "cosmetics", "split_merge"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			spec := filepath.Join("..", "..", "examples", name, "rule.json")
			normal := filepath.Join(t.TempDir(), "normal")
			disk := filepath.Join(t.TempDir(), "disk")
			a, e := RunWithOptions(spec, normal, RunOptions{Workers: 2})
			if e != nil {
				t.Fatal(e)
			}
			b, e := RunDiskWithOptions(spec, disk, RunOptions{Workers: 4, DiskPartitions: 8})
			if e != nil {
				t.Fatal(e)
			}
			if a.InputAmount != b.InputAmount || a.OutputAmount != b.OutputAmount || a.TerminalRows != b.TerminalRows || a.LedgerRows != b.LedgerRows || a.PoolChecks != b.PoolChecks {
				t.Fatalf("different summaries: %#v vs %#v", a, b)
			}
			for _, file := range []string{"results.csv", "ledger.csv", "reconciliation.csv", "source_to_pool.csv"} {
				left, right := readCanonical(t, filepath.Join(normal, file)), readCanonical(t, filepath.Join(disk, file))
				if !reflect.DeepEqual(left, right) {
					t.Errorf("%s differs (unordered row comparison)", file)
				}
			}
		})
	}
}
func TestDiskDeterministicAcrossWorkers(t *testing.T) {
	spec := filepath.Join("..", "..", "examples", "manufacturing", "rule.json")
	a := filepath.Join(t.TempDir(), "w1")
	b := filepath.Join(t.TempDir(), "w4")
	if _, e := RunDiskWithOptions(spec, a, RunOptions{Workers: 1, DiskPartitions: 8}); e != nil {
		t.Fatal(e)
	}
	if _, e := RunDiskWithOptions(spec, b, RunOptions{Workers: 4, DiskPartitions: 8}); e != nil {
		t.Fatal(e)
	}
	for _, file := range []string{"results.csv", "ledger.csv", "reconciliation.csv", "source_to_pool.csv"} {
		x, e := os.ReadFile(filepath.Join(a, file))
		if e != nil {
			t.Fatal(e)
		}
		y, e := os.ReadFile(filepath.Join(b, file))
		if e != nil {
			t.Fatal(e)
		}
		if string(x) != string(y) {
			t.Errorf("worker-dependent file: %s", file)
		}
	}
}
func TestDiskFailuresDoNotPublish(t *testing.T) {
	spec := filepath.Join("..", "..", "examples", "manufacturing", "rule.json")
	base := t.TempDir()
	out := filepath.Join(base, "final")
	_, e := RunDiskWithOptions(spec, out, RunOptions{Workers: 4, DiskPartitions: 1})
	if e == nil {
		t.Fatal("expected invalid disk partition error")
	}
	if _, er := os.Stat(out); !os.IsNotExist(er) {
		t.Fatalf("invalid run created output %v", er)
	}
}

// A runtime driver error must leave neither successful result folder nor a
// visible .allocation-v11-incomplete- directory next to it.
func TestDiskRuntimeFailureDoesNotPublishPartialFiles(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join("..", "..", "examples", "cosmetics")
	for _, name := range []string{"rule.json", "amounts.csv", "sales.csv"} {
		b, e := os.ReadFile(filepath.Join(src, name))
		if e != nil {
			t.Fatal(e)
		}
		if name == "sales.csv" {
			b = []byte("period,region,brand,sku,units\n2026-01,华东,品牌甲,Item-A,0\n2026-01,华东,品牌甲,Item-B,0\n")
		}
		if e = os.WriteFile(filepath.Join(root, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	dest := filepath.Join(root, "final")
	if _, e := RunDiskWithOptions(filepath.Join(root, "rule.json"), dest, RunOptions{Workers: 3, DiskPartitions: 4}); e == nil {
		t.Fatal("expected zero-denominator run to fail")
	}
	if _, e := os.Stat(dest); !os.IsNotExist(e) {
		t.Fatalf("partial output published: %v", e)
	}
	rest, e := filepath.Glob(filepath.Join(root, ".allocation-v11-incomplete-*"))
	if e != nil || len(rest) != 0 {
		t.Fatalf("temp files still present: %v, err %v", rest, e)
	}
}
