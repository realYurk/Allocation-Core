package engine

import (
	"allocationcore/internal/demo"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func coverageContractFixture(t *testing.T) (string, Spec) {
	t.Helper()
	root := t.TempDir()
	if _, e := demo.Generate(root); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "examples", "manufacturing", "rule.json")
	raw, e := os.ReadFile(filepath.Join("..", "..", "examples", "manufacturing", "rule.json"))
	if e != nil {
		t.Fatal(e)
	}
	s, e := ParseSpec(raw)
	if e != nil {
		t.Fatal(e)
	}
	return path, s
}
func saveCoverageRule(t *testing.T, path string, s Spec) {
	t.Helper()
	b, e := json.Marshal(s)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func runCoverage(t *testing.T, rule, out string, disk bool) (Summary, error) {
	t.Helper()
	if disk {
		return RunDiskWithOptions(rule, out, RunOptions{Workers: 2, DiskPartitions: 4})
	}
	return RunWithOptions(rule, out, RunOptions{Workers: 2})
}

func TestAmountSourcesMustBeConnectedOrExplicitlyIgnored(t *testing.T) {
	rule, s := coverageContractFixture(t)
	s.Datasets.Amounts["forgotten"] = "not-present.csv"
	saveCoverageRule(t, rule, s)
	if _, e := Compile(s); e == nil || !strings.Contains(e.Error(), "not connected") {
		t.Fatalf("unconnected source accepted: %v", e)
	}
	for _, disk := range []bool{false, true} {
		out := filepath.Join(t.TempDir(), "out")
		if _, e := runCoverage(t, rule, out, disk); e == nil {
			t.Fatal("execution accepted unconnected input")
		}
		if _, e := os.Stat(out); !os.IsNotExist(e) {
			t.Fatal("invalid run published")
		}
	}
	s.IgnoredAmounts = map[string]string{"forgotten": "explicitly excluded by approved business scope"}
	saveCoverageRule(t, rule, s)
	if _, e := Compile(s); e != nil {
		t.Fatal(e)
	}
	report := InspectInputs(rule, s)
	if report.Status != "PASS" {
		t.Fatalf("ignored dataset read unexpectedly: %+v", report)
	}
	for _, disk := range []bool{false, true} {
		out := filepath.Join(t.TempDir(), "out")
		got, e := runCoverage(t, rule, out, disk)
		if e != nil || got.Status != "PASS" || got.InputAmount != "1200.00" {
			t.Fatalf("%+v %v", got, e)
		}
	}
	s.IgnoredAmounts["expenses"] = "cannot ignore a connected input"
	if _, e := Compile(s); e == nil {
		t.Fatal("connected source ignored")
	}
}
func TestSourceUnmatchedPolicyReportsGrossWithoutNetting(t *testing.T) {
	rule, s := coverageContractFixture(t)
	s.Stages = s.Stages[:1]
	s.Stages[0].Source.Filters = map[string]Filter{"plant": {Include: []string{"OTHER"}}}
	data := "period,plant,brand,family,sku,account,currency,amount,row_id\n2026-01,P,U,U,U,E1,CUR,1.00,A\n2026-01,P,U,U,U,E1,CUR,-1.00,B\n"
	if e := os.WriteFile(filepath.Join(filepath.Dir(rule), "amounts.csv"), []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
	saveCoverageRule(t, rule, s)
	for _, disk := range []bool{false, true} {
		out := filepath.Join(t.TempDir(), "out")
		got, e := runCoverage(t, rule, out, disk)
		if e != nil || got.SourceCoverage != "UNMATCHED_PASSTHROUGH" || len(got.Stages) != 1 || got.Stages[0].UnmatchedRows != 2 || got.Stages[0].UnmatchedNet != "0.00" || got.Stages[0].UnmatchedGross != "2.00" {
			t.Fatalf("%+v %v", got, e)
		}
	}
	s.Stages[0].SourceUnmatchedPolicy = "BLOCK"
	saveCoverageRule(t, rule, s)
	for _, disk := range []bool{false, true} {
		out := filepath.Join(t.TempDir(), "out")
		if _, e := runCoverage(t, rule, out, disk); e == nil || !strings.Contains(e.Error(), "source filter unmatched") {
			t.Fatalf("not blocked: %v", e)
		}
		if _, e := os.Stat(out); !os.IsNotExist(e) {
			t.Fatal("partial output published")
		}
	}
}
func TestRequiredTargetMembersExplicitAndPerPool(t *testing.T) {
	rule, s := coverageContractFixture(t)
	s.Stages[0].Allocations[0].RequiredTargetMembers = map[string][]string{"family": {"系列甲", "系列乙"}}
	saveCoverageRule(t, rule, s)
	for _, disk := range []bool{false, true} {
		out := filepath.Join(t.TempDir(), "out")
		if _, e := runCoverage(t, rule, out, disk); e != nil {
			t.Fatal(e)
		}
	}
	// Presence and strictly positive weight are distinct, opt-in policies.
	if e := os.WriteFile(filepath.Join(filepath.Dir(rule), "labor.csv"), []byte("period,plant,family,hours\n2026-01,工厂甲,系列甲,0\n2026-01,工厂甲,系列乙,2\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, disk := range []bool{false, true} {
		out := filepath.Join(t.TempDir(), "out")
		if _, e := runCoverage(t, rule, out, disk); e != nil {
			t.Fatalf("zero-weight present target should satisfy presence-only policy: %v", e)
		}
	}
	s.Stages[0].Allocations[0].RequiredPositiveTargetMembers = map[string][]string{"family": {"系列甲"}}
	saveCoverageRule(t, rule, s)
	for _, disk := range []bool{false, true} {
		out := filepath.Join(t.TempDir(), "out")
		if _, e := runCoverage(t, rule, out, disk); e == nil || !strings.Contains(e.Error(), "positive eligible driver") {
			t.Fatalf("zero weight accepted: %v", e)
		}
	}
	s.Stages[0].Allocations[0].RequiredPositiveTargetMembers = nil
	saveCoverageRule(t, rule, s)
	if e := os.WriteFile(filepath.Join(filepath.Dir(rule), "labor.csv"), []byte("period,plant,family,hours\n2026-01,工厂甲,系列乙,2\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, disk := range []bool{false, true} {
		parent := t.TempDir()
		out := filepath.Join(parent, "out")
		if _, e := runCoverage(t, rule, out, disk); e == nil || !strings.Contains(e.Error(), "required target member") {
			t.Fatalf("missing required member accepted: %v", e)
		}
		if _, e := os.Stat(out); !os.IsNotExist(e) {
			t.Fatal("missing target published")
		}
		if entries, e := os.ReadDir(parent); e != nil || len(entries) != 0 {
			t.Fatalf("failure left temporary output or snapshots: %v %v", entries, e)
		}
	}
	s.Stages[0].Allocations[0].RequiredTargetMembers = map[string][]string{"unknown": {"X"}}
	if _, e := Compile(s); e == nil {
		t.Fatal("unknown dimension accepted")
	}
}

func TestLegacyCheckpointCoverageNeverClaimsComplete(t *testing.T) {
	if got := coverageStatus([]StageSummary{{Stage: "old"}}); got != "PARTIAL_LEGACY_CHECKPOINT" {
		t.Fatalf("old checkpoint coverage guessed: %s", got)
	}
}

func TestRequiredTargetsCanDependOnSourcePool(t *testing.T) {
	rule, s := coverageContractFixture(t)
	s.Stages[1].Allocations[0].RequiredTargetsBySource = []TargetRequirement{
		{WhenSource: map[string][]string{"family": {"系列甲"}}, Members: map[string][]string{"sku": {"Item-A", "Item-B"}}},
		{WhenSource: map[string][]string{"family": {"系列乙"}}, Members: map[string][]string{"sku": {"商品C", "商品D"}}},
	}
	s.Stages[1].Allocations[0].RequireSourceTargetPolicy = true
	saveCoverageRule(t, rule, s)
	for _, disk := range []bool{false, true} {
		if _, e := runCoverage(t, rule, filepath.Join(t.TempDir(), "out"), disk); e != nil {
			t.Fatalf("valid source-specific targets: %v", e)
		}
	}
	policies := s.Stages[1].Allocations[0].RequiredTargetsBySource
	s.Stages[1].Allocations[0].RequiredTargetsBySource = policies[:1]
	saveCoverageRule(t, rule, s)
	for _, disk := range []bool{false, true} {
		if _, e := runCoverage(t, rule, filepath.Join(t.TempDir(), "out"), disk); e == nil || !strings.Contains(e.Error(), "no source-specific target policy") {
			t.Fatalf("uncovered source accepted: %v", e)
		}
	}
	s.Stages[1].Allocations[0].RequiredTargetsBySource = policies
	saveCoverageRule(t, rule, s)
	data := "period,plant,family,sku,units\n2026-01,工厂甲,系列甲,Item-B,3\n2026-01,工厂甲,系列乙,商品C,2\n2026-01,工厂甲,系列乙,商品D,2\n"
	if e := os.WriteFile(filepath.Join(filepath.Dir(rule), "sales.csv"), []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
	for _, disk := range []bool{false, true} {
		out := filepath.Join(t.TempDir(), "out")
		if _, e := runCoverage(t, rule, out, disk); e == nil || !strings.Contains(e.Error(), "Item-A") {
			t.Fatalf("missing source-specific target accepted: %v", e)
		}
		if _, e := os.Stat(out); !os.IsNotExist(e) {
			t.Fatal("partial result published")
		}
	}
	s.Stages[1].Allocations[0].RequiredTargetsBySource[0].WhenSource = map[string][]string{"sku": {"Item-A"}}
	if _, e := Compile(s); e == nil || !strings.Contains(e.Error(), "AS_IS") {
		t.Fatalf("TOTAL source predicate accepted: %v", e)
	}
}
