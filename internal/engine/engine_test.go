package engine

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExactApportion(t *testing.T) {
	cases := []struct {
		n    int64
		w    []int64
		want []int64
	}{{120000, []int64{1, 3}, []int64{30000, 90000}}, {100, []int64{8, 2}, []int64{80, 20}}, {100, []int64{3, 1}, []int64{75, 25}}, {-101, []int64{1, 1}, []int64{-51, -50}}}
	for _, tc := range cases {
		ids := []string{}
		for i := range tc.w {
			ids = append(ids, string(rune('A'+i)))
		}
		got, err := Apportion(tc.n, tc.w, ids, false)
		if err != nil {
			t.Fatal(err)
		}
		sum := int64(0)
		for i, x := range got {
			sum += x
			if x != tc.want[i] && tc.n != -101 {
				t.Fatalf("got %v expected %v", got, tc.want)
			}
		}
		if sum != tc.n {
			t.Fatal("not conserved")
		}
	}
}
func TestSignedDriver(t *testing.T) {
	if _, err := Apportion(100, []int64{3, -1}, []string{"A", "B"}, false); err == nil {
		t.Fatal("negative driver accepted without opt-in")
	}
	a, err := Apportion(100, []int64{3, -1}, []string{"A", "B"}, true)
	if err != nil || a[0] != 150 || a[1] != -50 {
		t.Fatalf("signed %v err %v", a, err)
	}
}
func TestParseScaled(t *testing.T) {
	for _, tc := range []struct {
		in    string
		scale int
		want  int64
	}{{"3.25", 2, 325}, {"-0.12", 2, -12}, {"10", 2, 1000}, {".1", 2, 0}} {
		v, err := ParseScaled(tc.in, tc.scale)
		if tc.in == ".1" {
			if err == nil {
				t.Fatal("invalid syntax accepted")
			}
			continue
		}
		if err != nil || v != tc.want {
			t.Fatalf("%s %d %v", tc.in, v, err)
		}
	}
	if _, e := ParseScaled("1.001", 2); e == nil {
		t.Fatal("excess decimals accepted")
	}
}
func TestGraphCycleRejected(t *testing.T) {
	s := Spec{Schema: Schema, Dimensions: []string{"period"}}
	s.Datasets.Amounts = map[string]string{"expenses": "e.csv"}
	s.Datasets.Drivers = map[string]string{"weights": "w.csv"}
	st := Stage{ID: "A", Source: Source{Grouping: map[string]string{"period": "AS_IS"}}, Target: map[string]Target{"period": {Mode: "SOURCE"}}, Allocations: []Allocation{{ID: "x", Share: "1", DriverDataset: "weights", WeightField: "w"}}}
	st.Inputs = []InputEdge{{From: "B", Share: "1"}}
	s.Stages = []Stage{st}
	st.ID = "B"
	st.Inputs = []InputEdge{{From: "A", Share: "1"}}
	s.Stages = append(s.Stages, st)
	_, err := Compile(s)
	if err == nil {
		t.Fatal("cycle accepted")
	}
}
func TestConservationScopeEnforced(t *testing.T) {
	s := Spec{Schema: Schema, Dimensions: []string{"brand"}}
	s.Datasets.Amounts = map[string]string{"e": "e.csv"}
	s.Datasets.Drivers = map[string]string{"d": "d.csv"}
	s.Stages = []Stage{{ID: "x", Inputs: []InputEdge{{From: "e"}}, Source: Source{Grouping: map[string]string{"brand": "TOTAL"}}, Target: map[string]Target{"brand": {Mode: "ELEMENT", Value: "B"}}, ConservationKeys: []string{"brand"}, Allocations: []Allocation{{ID: "x", DriverDataset: "d", WeightField: "w"}}}}
	_, err := Compile(s)
	if err == nil {
		t.Fatal("invalid scope passed")
	}
}
func TestExamplesEndToEnd(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "examples")
	for _, name := range []string{"manufacturing", "cosmetics", "split_merge"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, name, "rule.json")
			out := filepath.Join(t.TempDir(), "run")
			summary, err := Run(path, out)
			if err != nil {
				t.Fatal(err)
			}
			if summary.Status != "PASS" || summary.InputAmount != summary.OutputAmount {
				t.Fatal(summary)
			}
			if _, err := os.Stat(filepath.Join(out, "source_to_pool.csv")); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(filepath.Join(out, "results.csv"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			rows, err := csv.NewReader(f).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			if name == "manufacturing" && len(rows) != 5 {
				t.Fatalf("expected 4 SKUs, got %d", len(rows)-1)
			}
			if name == "split_merge" {
				if len(rows) != 3 {
					t.Fatalf("expected merged 2 SKU rows, got %d", len(rows)-1)
				}
				contents, _ := os.ReadFile(filepath.Join(out, "results.csv"))
				if !strings.Contains(string(contents), "725.00") || !strings.Contains(string(contents), "275.00") {
					t.Fatal(string(contents))
				}
			}
		})
	}
}
func TestNoHalfWrittenResultsOnFailure(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "examples", "cosmetics", "rule.json")
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	broken := strings.Replace(string(raw), "sales.csv", "nonexistent.csv", 1)
	temp := t.TempDir()
	copy := filepath.Join(temp, "broken.json")
	if err := os.WriteFile(copy, []byte(broken), 0600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(temp, "out")
	if _, e := Run(copy, dest); e == nil {
		t.Fatal("missing data should fail")
	}
	if _, e := os.Stat(dest); !os.IsNotExist(e) {
		t.Fatal("half-written result folder exists")
	}
}

func TestMinIntAndDeterministic(t *testing.T) {
	if got := Money(-9223372036854775808, 2); got != "-92233720368547758.08" {
		t.Fatal(got)
	}
	for i := 0; i < 100; i++ {
		vals, err := Apportion(101, []int64{1, 1, 1}, []string{"A", "B", "C"}, false)
		if err != nil || vals[0] != 34 || vals[1] != 34 || vals[2] != 33 {
			t.Fatalf("non deterministic %v %v", vals, err)
		}
	}
}

func TestInvalidSharesRejected(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	config := filepath.Join(filepath.Dir(file), "..", "..", "examples", "split_merge", "rule.json")
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(raw), `"share": "0.25"`, `"share": "0.20"`, 1)
	s, err := ParseSpec([]byte(bad))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Compile(s); err == nil {
		t.Fatal("source shares total 95% accepted")
	}
}
func TestZeroDenominatorRunFailsClosed(t *testing.T) {
	tmp := t.TempDir()
	_, file, _, _ := runtime.Caller(0)
	original := filepath.Join(filepath.Dir(file), "..", "..", "examples", "cosmetics")
	raw, err := os.ReadFile(filepath.Join(original, "rule.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	s.Datasets.Amounts["expenses"] = filepath.Join(original, "amounts.csv")
	s.Datasets.Drivers["sales"] = filepath.Join(tmp, "zero.csv")
	if err := os.WriteFile(filepath.Join(tmp, "zero.csv"), []byte("period,region,brand,sku,units\n2026-01,华东,品牌甲,Item-A,0\n2026-01,华东,品牌甲,Item-B,0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(s)
	path := filepath.Join(tmp, "rule.json")
	os.WriteFile(path, js, 0600)
	dir := filepath.Join(tmp, "published")
	if _, err := Run(path, dir); err == nil || !strings.Contains(err.Error(), "denominator") {
		t.Fatalf("zero denominator not blocked: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("partial output was published")
	}
}
func TestExcludedMemberGetsNoAmount(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "examples", "cosmetics", "rule.json")
	out := filepath.Join(t.TempDir(), "out")
	if _, err := Run(path, out); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(out, "results.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "试用品") || !strings.Contains(string(body), "300.00") || !strings.Contains(string(body), "700.00") {
		t.Fatal(string(body))
	}
}
func TestMergeProvenanceHasBothParents(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "examples", "split_merge", "rule.json")
	out := filepath.Join(t.TempDir(), "out")
	if _, err := Run(path, out); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(out, "source_to_pool.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(body), "合并分摊结果"); got != 4 {
		t.Fatalf("expected 4 branch parent edges, got %d", got)
	}
}
