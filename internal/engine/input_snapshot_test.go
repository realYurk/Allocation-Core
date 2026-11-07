package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInputSnapshotConsumesCapturedBytesAndDeduplicatesPaths(t *testing.T) {
	rule, spec := coverageContractFixture(t)
	base := filepath.Dir(rule)
	plan, e := Compile(spec)
	if e != nil {
		t.Fatal(e)
	}
	original := filepath.Join(base, "amounts.csv")
	before, e := os.ReadFile(original)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := snapshotInputs(&spec, plan, base, t.TempDir(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer snapshot.close()
	if e = os.WriteFile(original, []byte("later modification"), 0600); e != nil {
		t.Fatal(e)
	}
	copyPath := spec.Datasets.Amounts["expenses"]
	captured, e := os.ReadFile(copyPath)
	if e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(captured)
	if string(captured) != string(before) || snapshot.hashes[original] != hex.EncodeToString(h[:]) {
		t.Fatal("snapshot digest does not identify captured bytes")
	}
	if !filepath.IsAbs(copyPath) {
		t.Fatal("snapshot path is not absolute")
	}
}
func TestInputSnapshotErrorsReferToOriginalSource(t *testing.T) {
	rule, spec := coverageContractFixture(t)
	saveCoverageRule(t, rule, spec)
	path := filepath.Join(filepath.Dir(rule), "amounts.csv")
	if e := os.WriteFile(path, []byte("period,plant,brand,family,sku,account,currency,amount\n2026-01,P,U,U,U,E,CUR,1.001\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, disk := range []bool{false, true} {
		_, e := runCoverage(t, rule, filepath.Join(t.TempDir(), "out"), disk)
		if e == nil || !strings.Contains(e.Error(), path) || strings.Contains(e.Error(), ".allocation-inputs-") {
			t.Fatalf("lost original source location: %v", e)
		}
	}
}
func TestInputSnapshotFailureLeavesNoTemporaryDirectory(t *testing.T) {
	rule, spec := coverageContractFixture(t)
	plan, e := Compile(spec)
	if e != nil {
		t.Fatal(e)
	}
	parent := t.TempDir()
	if e = os.Remove(filepath.Join(filepath.Dir(rule), "sales.csv")); e != nil {
		t.Fatal(e)
	}
	if _, e = snapshotInputs(&spec, plan, filepath.Dir(rule), parent, nil); e == nil {
		t.Fatal("missing input accepted")
	}
	entries, e := os.ReadDir(parent)
	if e != nil || len(entries) != 0 {
		t.Fatalf("temporary input snapshots leaked: %v %v", entries, e)
	}
}

func TestInputSnapshotRejectsSourceChangedBeforeVerification(t *testing.T) {
	rule, spec := coverageContractFixture(t)
	plan, err := Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	amount := filepath.Join(filepath.Dir(rule), "amounts.csv")
	originalPath := spec.Datasets.Amounts["expenses"]
	modified := false
	_, err = snapshotInputsWithCopyHook(&spec, plan, filepath.Dir(rule), parent, nil, func(path string) error {
		if path == amount {
			modified = true
			return os.WriteFile(path, []byte("changed while capturing synthetic input"), 0600)
		}
		return nil
	})
	if !modified || err == nil || !strings.Contains(err.Error(), "input changed while taking snapshot") || !strings.Contains(err.Error(), amount) {
		t.Fatalf("changed input should fail closed: modified=%v error=%v", modified, err)
	}
	if spec.Datasets.Amounts["expenses"] != originalPath {
		t.Fatal("failed snapshot modified rule dataset paths")
	}
	entries, readErr := os.ReadDir(parent)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("failed snapshot left temporary files: %v %v", entries, readErr)
	}
}
func TestInputSnapshotCheckpointIdentityMismatchBlocks(t *testing.T) {
	rule, spec := coverageContractFixture(t)
	plan, e := Compile(spec)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = snapshotInputs(&spec, plan, filepath.Dir(rule), t.TempDir(), map[string]string{}); e == nil {
		t.Fatal("checkpoint mismatch accepted")
	}
}

func TestPublishedSummaryUsesOriginalInputPathAndCapturedHash(t *testing.T) {
	rule, spec := coverageContractFixture(t)
	saveCoverageRule(t, rule, spec)
	path := filepath.Join(filepath.Dir(rule), "amounts.csv")
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	for _, disk := range []bool{false, true} {
		parent := t.TempDir()
		out := filepath.Join(parent, "published")
		result, e := runCoverage(t, rule, out, disk)
		if e != nil || result.DataSHA256[path] != hex.EncodeToString(sum[:]) {
			t.Fatalf("missing original input hash: %+v %v", result, e)
		}
		entries, e := os.ReadDir(parent)
		if e != nil || len(entries) != 1 || entries[0].Name() != "published" {
			t.Fatalf("temporary snapshot left behind: %v %v", entries, e)
		}
	}
}
