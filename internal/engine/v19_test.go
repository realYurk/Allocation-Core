package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestV19CompressedTraceMatchesFullPlainAcrossTopologies(t *testing.T) {
	for _, example := range []string{"split_merge", "dual_driver", "triple_driver", "manufacturing"} {
		t.Run(example, func(t *testing.T) {
			rule := filepath.Join("..", "..", "examples", example, "rule.json")
			root := t.TempDir()
			plain := filepath.Join(root, "plain")
			packed := filepath.Join(root, "packed")
			if _, err := RunWithOptions(rule, plain, RunOptions{Workers: 1}); err != nil {
				t.Fatal(err)
			}
			if _, err := RunWithOptions(rule, packed, RunOptions{Workers: 4, EvidenceGzip: true}); err != nil {
				t.Fatal(err)
			}
			id, err := FirstResultID(plain)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := TraceRow(plain, id, 500)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = BuildPackedTraceIndex(packed); err != nil {
				t.Fatal(err)
			}
			if err = VerifyPackedTraceIndex(packed); err != nil {
				t.Fatal(err)
			}
			actual, err := TraceRow(packed, id, 500)
			if err != nil {
				t.Fatal(err)
			}
			x, _ := json.Marshal(expected)
			y, _ := json.Marshal(actual)
			if !bytes.Equal(x, y) {
				t.Fatalf("different trace\nplain=%s\npacked=%s", x, y)
			}
			if _, err = os.Stat(filepath.Join(packed, "ledger.csv")); !os.IsNotExist(err) {
				t.Fatal("packed trace materialized plaintext ledger")
			}
			if _, err = BuildPackedTraceIndex(packed); err == nil {
				t.Fatal("overwrote published packed index")
			}
		})
	}
}
func TestV19CompressedTraceRejectsCorruptedShardChunkAndSource(t *testing.T) {
	rule := filepath.Join("..", "..", "examples", "split_merge", "rule.json")
	dir := filepath.Join(t.TempDir(), "result")
	if _, err := RunWithOptions(rule, dir, RunOptions{EvidenceGzip: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildPackedTraceIndex(dir); err != nil {
		t.Fatal(err)
	}
	id, err := FirstResultID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = TraceRow(dir, id, 500); err != nil {
		t.Fatal(err)
	}
	name := "ledger.csv"
	orig := filepath.Join(dir, packedTraceDir, name, "chunks.bgz")
	content, err := os.ReadFile(orig)
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)/2] ^= 0xff
	if err = os.WriteFile(orig, content, 0644); err != nil {
		t.Fatal(err)
	}
	if err = VerifyPackedTraceIndex(dir); err == nil {
		t.Fatal("tampered chunk accepted in full verification")
	}
	// Change the source and confirm that the index cannot be used as if current.
	src := filepath.Join(dir, "ledger.csv.gz")
	f, err := os.OpenFile(src, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte("garbage")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err = TraceRow(dir, id, 500); err == nil || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("modified source not rejected: %v", err)
	}
}
