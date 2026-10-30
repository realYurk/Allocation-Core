package engine

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestIndexedTraceMatchesFullScanAndRejectsStaleFiles(t *testing.T) {
	root := fixtureRoot(t)
	rule := filepath.Join(root, "dual_driver", "rule.json")
	dir := filepath.Join(t.TempDir(), "results")
	if _, err := RunWithOptions(rule, dir, RunOptions{Workers: 2}); err != nil {
		t.Fatal(err)
	}
	id, err := FirstResultID(dir)
	if err != nil {
		t.Fatal(err)
	}
	before, err := TraceRow(dir, id, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Links) == 0 {
		t.Fatal("baseline has no trace")
	}
	receipt, err := BuildTraceIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Files["ledger.csv"].Rows == 0 {
		t.Fatal("index empty")
	}
	if err = VerifyTraceIndex(dir); err != nil {
		t.Fatal(err)
	}
	after, err := TraceRow(dir, id, 500)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("index changed trace\nBEFORE=%+v\nAFTER=%+v", before, after)
	}
	// A corrupted shard must not silently hide an upstream allocation edge.
	digest := sha256.Sum256([]byte(id))
	shard := filepath.Join(dir, "trace_index", "ledger.csv", fmt.Sprintf("%02d.bin", int(digest[0])%traceShards))
	raw, err := os.ReadFile(shard)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 24 {
		t.Fatal("missing indexed ledger record")
	}
	raw[0] ^= 0x31
	if err = os.WriteFile(shard, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = TraceRow(dir, id, 500); err == nil {
		t.Fatal("corrupted index shard used silently")
	}
	// Restore shard so subsequent stale-file check covers the source file.
	raw[0] ^= 0x31
	if err = os.WriteFile(shard, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Even without changing bytes, altered file timestamps invalidate offsets.
	file := filepath.Join(dir, "ledger.csv")
	stamp := time.Now().Add(2 * time.Hour)
	if err = os.Chtimes(file, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = TraceRow(dir, id, 500); err == nil {
		t.Fatal("stale index silently used")
	}
}

func TestAuditArchiveLosslessAndIntegrityChecks(t *testing.T) {
	rule := filepath.Join(fixtureRoot(t), "manufacturing", "rule.json")
	base := t.TempDir()
	source := filepath.Join(base, "run")
	if _, err := RunWithOptions(rule, source, RunOptions{Workers: 2}); err != nil {
		t.Fatal(err)
	}
	zipfile := filepath.Join(base, "audit.zip")
	pack, err := PackEvidence(source, zipfile)
	if err != nil {
		t.Fatal(err)
	}
	if pack.OriginalBytes <= 0 || pack.CompressedBytes <= 0 {
		t.Fatalf("empty archive %+v", pack)
	}
	if _, err = VerifyEvidencePack(zipfile); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(base, "restored")
	if err = ExtractEvidencePack(zipfile, restored); err != nil {
		t.Fatal(err)
	}
	for name := range pack.Files {
		a, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(restored, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Fatalf("nonidentical restored %s", name)
		}
	}
	if err = ExtractEvidencePack(zipfile, restored); err == nil {
		t.Fatal("overwrote existing result directory")
	}
	// Any ZIP byte mutation must be detected or invalidate the ZIP itself.
	contents, err := os.ReadFile(zipfile)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) < 256 {
		t.Fatal("archive unexpectedly small")
	}
	zr, er := zip.OpenReader(zipfile)
	if er != nil {
		t.Fatal(er)
	}
	damaged := false
	for _, f := range zr.File {
		if f.Name == "ledger.csv" && f.CompressedSize64 > 2 {
			off, er := f.DataOffset()
			if er != nil {
				t.Fatal(er)
			}
			contents[int(off)+int(f.CompressedSize64/2)] ^= 0x77
			damaged = true
		}
	}
	zr.Close()
	if !damaged {
		t.Fatal("missing ledger ZIP entry")
	}

	corrupt := filepath.Join(base, "corrupt.zip")
	if err = os.WriteFile(corrupt, contents, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyEvidencePack(corrupt); err == nil {
		t.Fatal("corrupted ZIP passed audit")
	}
}
