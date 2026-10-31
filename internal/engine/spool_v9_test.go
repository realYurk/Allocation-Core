package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestV9BoundedShardBuffers(t *testing.T) {
	for _, tc := range []struct{ n, want int }{{2, 256 << 10}, {8, 256 << 10}, {16, 128 << 10}, {32, 64 << 10}, {64, 32 << 10}} {
		if got := shardBufferBytes(tc.n); got != tc.want {
			t.Fatalf("shards=%d: buffer=%d want=%d", tc.n, got, tc.want)
		}
	}
	if 64*shardBufferBytes(64) != 2<<20 {
		t.Fatal("64-way partition buffers exceed 2 MiB budget")
	}
}

func TestV9Sparse64ShardCompatibility(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.jsonl")
	w, err := openSpool(source)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]byte, 0)
	for i := 0; i < 23; i++ {
		row := Row{Coord: map[string]string{"period": "2026-01", "account": "A", "sku": "SKU-001"}, Amount: int64(i - 11), IDs: []string{"r"}}
		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, raw...)
		want = append(want, '\n')
		if err = w.put(row); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.close(); err != nil {
		t.Fatal(err)
	}
	paths, err := partitionSpool(source, filepath.Join(dir, "shard"), 64, func(r Row) string { return r.Coord["account"] })
	if err != nil {
		t.Fatal(err)
	}
	nonempty := 0
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 0 {
			nonempty++
			if string(data) != string(want) {
				t.Fatal("shard record bytes or record order changed")
			}
		}
	}
	if nonempty != 1 {
		t.Fatalf("expected 1 nonempty shard, got %d", nonempty)
	}
}

func BenchmarkV9PartitionSpool64(b *testing.B) {
	dir := b.TempDir()
	source := filepath.Join(dir, "input.jsonl")
	w, err := openSpool(source)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 5000; i++ {
		row := Row{Coord: map[string]string{"account": "ACC001", "sku": "S" + strings.Repeat("x", 16)}, Amount: int64(i), IDs: []string{"id"}}
		if err = w.put(row); err != nil {
			b.Fatal(err)
		}
	}
	if err = w.close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err = partitionSpool(source, filepath.Join(dir, "parts"), 64, func(r Row) string { return r.Coord["account"] })
		if err != nil {
			b.Fatal(err)
		}
	}
}
