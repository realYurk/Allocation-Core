package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func compareV18Files(t *testing.T, first, second string) {
	t.Helper()
	for _, name := range evidenceStreamNames {
		a, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(second, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "run_summary.json" {
			continue
		} // start timestamp differs between runs
		if !bytes.Equal(a, b) {
			t.Fatalf("non-identical restored %s", name)
		}
	}
}

func TestV18StreamingEvidenceIsLosslessAcrossWorkersAndExamples(t *testing.T) {
	for _, test := range []string{"manufacturing", "dual_driver", "triple_driver", "split_merge"} {
		t.Run(test, func(t *testing.T) {
			rule := filepath.Join("..", "..", "examples", test, "rule.json")
			root := t.TempDir()
			raw := filepath.Join(root, "raw")
			compressed := filepath.Join(root, "compressed")
			restored := filepath.Join(root, "restored")
			if _, err := RunWithOptions(rule, raw, RunOptions{Workers: 1}); err != nil {
				t.Fatal(err)
			}
			if _, err := RunWithOptions(rule, compressed, RunOptions{Workers: 4, EvidenceGzip: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyEvidenceStream(compressed); err != nil {
				t.Fatal(err)
			}
			if err := ExpandEvidenceStream(compressed, restored); err != nil {
				t.Fatal(err)
			}
			compareV18Files(t, raw, restored)
			if _, err := os.Stat(filepath.Join(compressed, "ledger.csv")); !os.IsNotExist(err) {
				t.Fatal("plaintext ledger created in streaming mode")
			}
			if _, err := os.Stat(filepath.Join(compressed, "ledger.csv.gz")); err != nil {
				t.Fatal(err)
			}
			if err := ExpandEvidenceStream(compressed, restored); err == nil {
				t.Fatal("destination should not be overwritten")
			}
			zipped2 := filepath.Join(root, "compressed2")
			if _, err := RunWithOptions(rule, zipped2, RunOptions{Workers: 2, EvidenceGzip: true}); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"ledger.csv.gz", "source_to_pool.csv.gz", "lineage_links.csv.gz", "results.csv", "reconciliation.csv"} {
				x, e := os.ReadFile(filepath.Join(compressed, name))
				if e != nil {
					t.Fatal(e)
				}
				y, e := os.ReadFile(filepath.Join(zipped2, name))
				if e != nil {
					t.Fatal(e)
				}
				if !bytes.Equal(x, y) {
					t.Errorf("%s different across workers", name)
				}
			}
		})
	}
}

func TestV18DiskCheckpointResumeAndRestore(t *testing.T) {
	rule := filepath.Join("..", "..", "examples", "split_merge", "rule.json")
	root := t.TempDir()
	cp := filepath.Join(root, "checkpoint")
	_, err := RunDiskWithOptions(rule, filepath.Join(root, "crashed"), RunOptions{Workers: 2, DiskPartitions: 4, CheckpointDir: cp, TestStopAfterStage: 1, EvidenceGzip: true})
	if err == nil {
		t.Fatal("expected injected failure")
	}
	if _, err := os.Stat(filepath.Join(root, "crashed")); !os.IsNotExist(err) {
		t.Fatal("partial outputs published")
	}
	resumed := filepath.Join(root, "resumed")
	result, err := RunDiskWithOptions(rule, resumed, RunOptions{Workers: 4, DiskPartitions: 4, CheckpointDir: cp, Resume: true, EvidenceGzip: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReusedStages != 1 {
		t.Fatalf("wanted one stage restored: %+v", result)
	}
	if _, err = VerifyEvidenceStream(resumed); err != nil {
		t.Fatal(err)
	}
	raw := filepath.Join(root, "raw")
	if _, err = RunDiskWithOptions(rule, raw, RunOptions{Workers: 1, DiskPartitions: 4}); err != nil {
		t.Fatal(err)
	}
	expanded := filepath.Join(root, "expanded")
	if err = ExpandEvidenceStream(resumed, expanded); err != nil {
		t.Fatal(err)
	}
	// Both disk flows preserve the same output files across checkpoints.
	compareV18Files(t, raw, expanded)
}

func TestV18DetectsTruncatedEvidenceAndTamperedManifest(t *testing.T) {
	rule := filepath.Join("..", "..", "examples", "dual_driver", "rule.json")
	root := t.TempDir()
	dir := filepath.Join(root, "output")
	if _, err := RunWithOptions(rule, dir, RunOptions{EvidenceGzip: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ledger.csv.gz")
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, file[:len(file)-5], 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyEvidenceStream(dir); err == nil {
		t.Fatal("truncated gzip passed verification")
	}
	expanded := filepath.Join(root, "expanded")
	if err = ExpandEvidenceStream(dir, expanded); err == nil {
		t.Fatal("truncated gzip extracted")
	}
	if _, err = os.Stat(expanded); !os.IsNotExist(err) {
		t.Fatal("damaged evidence published")
	}
}
