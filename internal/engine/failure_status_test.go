package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutionErrorsAlwaysReturnBlockedSummary(t *testing.T) {
	rule, spec := coverageContractFixture(t)
	saveCoverageRule(t, rule, spec)
	amounts := filepath.Join(filepath.Dir(rule), "amounts.csv")
	if err := os.WriteFile(amounts, []byte("period,plant,brand,family,sku,account,currency,amount\n2026-01,P,U,U,U,E,CUR,1.001\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, disk := range []bool{false, true} {
		name := "memory"
		if disk {
			name = "disk"
		}
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			result, err := runCoverage(t, rule, out, disk)
			if err == nil || result.Status != "BLOCK" {
				t.Fatalf("failure returned non-blocked summary: %+v, %v", result, err)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("failure published output: %v", err)
			}
		})
	}
	for _, disk := range []bool{false, true} {
		options := RunOptions{Workers: -1}
		var result Summary
		var err error
		if disk {
			options.DiskPartitions = 4
			result, err = RunDiskWithOptions(rule, filepath.Join(t.TempDir(), "out"), options)
		} else {
			result, err = RunWithOptions(rule, filepath.Join(t.TempDir(), "out"), options)
		}
		if err == nil || result.Status != "BLOCK" {
			t.Fatalf("invalid options returned non-blocked summary: %+v, %v", result, err)
		}
	}
}
