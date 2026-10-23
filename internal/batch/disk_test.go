package batch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBatchDiskPartitioningAndFailureIsolation(t *testing.T) {
	manifest := filepath.Join("..", "..", "examples", "batch_manifest.json")
	dir := filepath.Join(t.TempDir(), "runs")
	report, err := RunWithDisk(manifest, dir, 2, 2, 8)
	if err != nil {
		t.Fatal(err)
	}
	if report.Succeeded != 3 || report.Failed != 0 || report.DiskPartitions != 8 {
		t.Fatalf("unexpected report %#v", report)
	}
	for _, j := range report.Jobs {
		if j.State != "PASS" && j.State != "SUCCEEDED" {
			t.Fatalf("unexpected job %#v", j)
		}
		if _, er := os.Stat(filepath.Join(dir, j.ID, "run_summary.json")); er != nil {
			t.Fatal(er)
		}
	}
}
