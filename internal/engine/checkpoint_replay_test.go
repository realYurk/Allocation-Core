package engine

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCSVExcelBOMAndUnicode(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "results.csv")
	sink, err := newSink(file, []string{"stage", "sku", "amount"})
	if err != nil {
		t.Fatal(err)
	}
	if err = sink.write([]string{"第一步_工厂到系列", "Item-A", "100.00"}); err != nil {
		t.Fatal(err)
	}
	if err = sink.finish(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("CSV missing Excel UTF-8 BOM")
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if rows[1][0] != "第一步_工厂到系列" || rows[1][1] != "Item-A" {
		t.Fatalf("UTF8 roundtrip failed %#v", rows[1])
	}
	if rows[0][0] != "\ufeffstage" {
		t.Fatalf("BOM not on first header only: %q", rows[0][0])
	}
}

func TestResumeStageOutputAndEvidenceMatchesFresh(t *testing.T) {
	for _, fixture := range []string{"manufacturing", "split_merge"} {
		t.Run(fixture, func(t *testing.T) {
			rule := filepath.Join("..", "..", "examples", fixture, "rule.json")
			temp := t.TempDir()
			fresh := filepath.Join(temp, "fresh")
			failed := filepath.Join(temp, "failed")
			resumed := filepath.Join(temp, "resumed")
			checkpoint := filepath.Join(temp, "ckpt")
			_, err := RunDiskWithOptions(rule, fresh, RunOptions{DiskPartitions: 4, Workers: 2})
			if err != nil {
				t.Fatal(err)
			}
			_, err = RunDiskWithOptions(rule, failed, RunOptions{DiskPartitions: 4, Workers: 2, CheckpointDir: checkpoint, TestStopAfterStage: 1})
			if err == nil || !strings.Contains(err.Error(), "stopped after durable stage") {
				t.Fatalf("expected controlled crash %v", err)
			}
			if _, err = os.Stat(failed); !os.IsNotExist(err) {
				t.Fatalf("partial results published: %v", err)
			}
			resumedSummary, err := RunDiskWithOptions(rule, resumed, RunOptions{DiskPartitions: 4, Workers: 4, CheckpointDir: checkpoint, Resume: true})
			if err != nil {
				t.Fatal(err)
			}
			if resumedSummary.ReusedStages != 1 {
				t.Fatalf("expected reuse 1 stage got %d", resumedSummary.ReusedStages)
			}
			for _, name := range []string{"results.csv", "ledger.csv", "reconciliation.csv", "source_to_pool.csv"} {
				x, err := os.ReadFile(filepath.Join(fresh, name))
				if err != nil {
					t.Fatal(err)
				}
				y, err := os.ReadFile(filepath.Join(resumed, name))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(x, y) {
					t.Errorf("%s differs after resumed stage", name)
				}
			}
			// Already completed stages are also safe to replay into a new output directory.
			repeat, err := RunDiskWithOptions(rule, filepath.Join(temp, "repeat"), RunOptions{DiskPartitions: 4, Workers: 2, CheckpointDir: checkpoint, Resume: true})
			if err != nil {
				t.Fatal(err)
			}
			if repeat.ReusedStages != 2 {
				if fixture == "split_merge" && repeat.ReusedStages == 3 {
					return
				}
				t.Fatalf("expect all stages reused, got %d", repeat.ReusedStages)
			}
		})
	}
}

func TestCheckpointRejectsDataChangeAndTamper(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join("..", "..", "examples", "manufacturing")
	for _, name := range []string{"rule.json", "amounts.csv", "labor.csv", "sales.csv"} {
		raw, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(tmp, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cp := filepath.Join(tmp, "checkpoints")
	rule := filepath.Join(tmp, "rule.json")
	_, err := RunDiskWithOptions(rule, filepath.Join(tmp, "crashed"), RunOptions{DiskPartitions: 4, CheckpointDir: cp, TestStopAfterStage: 1})
	if err == nil {
		t.Fatal("expected stopped run")
	}
	cached := filepath.Join(cp, "stage_000", "ledger.csv")
	if err = os.WriteFile(cached, []byte("tampered data"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = RunDiskWithOptions(rule, filepath.Join(tmp, "no_publish"), RunOptions{DiskPartitions: 4, CheckpointDir: cp, Resume: true})
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("tampered checkpoint not rejected: %v", err)
	}
	if _, e := os.Stat(filepath.Join(tmp, "no_publish")); !os.IsNotExist(e) {
		t.Fatal("tampered checkpoint published output")
	}
	driver := filepath.Join(tmp, "labor.csv")
	f, e := os.OpenFile(driver, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.WriteString("\n")
	_ = f.Close()
	_, err = RunDiskWithOptions(rule, filepath.Join(tmp, "changed"), RunOptions{DiskPartitions: 4, CheckpointDir: cp, Resume: true})
	if err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("modified input not rejected: %v", err)
	}
}
