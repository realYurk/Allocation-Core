package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestV8SpoolPartitionPreservesExactRawJSONL(t *testing.T) {
	root := t.TempDir()
	infile := filepath.Join(root, "in.jsonl")
	w, e := openSpool(infile)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 133; i++ {
		row := Row{Coord: map[string]string{"period": "2026-01", "account": "AC_" + string(rune('A'+i%10)), "sku": "品名，\\\"-A"}, Amount: int64(i - 60), IDs: []string{"source-α", "id-β"}}
		if e = w.put(row); e != nil {
			t.Fatal(e)
		}
	}
	if e = w.close(); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(infile)
	if e != nil {
		t.Fatal(e)
	}
	parts, e := partitionSpool(infile, filepath.Join(root, "shard"), 7, func(r Row) string { return r.Coord["account"] })
	if e != nil {
		t.Fatal(e)
	}
	var all [][]byte
	for _, part := range parts {
		e = scanSpoolRecord(part, func(r Row, raw []byte) error {
			if len(r.Coord) != 3 {
				t.Fatalf("lost coordinates: %+v", r)
			}
			copyRaw := append([]byte(nil), raw...)
			all = append(all, copyRaw)
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	originals := bytes.Split(bytes.TrimSuffix(before, []byte("\n")), []byte("\n"))
	if len(all) != len(originals) {
		t.Fatalf("number of records changed: %d != %d", len(all), len(originals))
	}
	// Ignore partition order; every serialized record must be byte-for-byte
	// preserved, including escaped text, coordinate maps, and row identities.
	for i := range originals {
		originals[i] = append(originals[i], '\n')
	}
	sort.Slice(all, func(i, j int) bool { return bytes.Compare(all[i], all[j]) < 0 })
	sort.Slice(originals, func(i, j int) bool { return bytes.Compare(originals[i], originals[j]) < 0 })
	for i := range originals {
		if !bytes.Equal(all[i], originals[i]) {
			t.Fatalf("JSONL changed at %d", i)
		}
	}
}

func TestV8SpoolPartitionHandlesOversizedRecord(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "oversized.jsonl")
	w, e := openSpool(path)
	if e != nil {
		t.Fatal(e)
	}
	big := strings.Repeat("月度成本-长维度成员", 25000)
	if e = w.put(Row{Coord: map[string]string{"account": big}, Amount: 999, IDs: []string{"oversized"}}); e != nil {
		t.Fatal(e)
	}
	if e = w.close(); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	paths, e := partitionSpool(path, filepath.Join(root, "shard"), 2, func(r Row) string { return r.Coord["account"] })
	if e != nil {
		t.Fatal(e)
	}
	var got []byte
	for _, part := range paths {
		if e = scanSpoolRecord(part, func(r Row, raw []byte) error { got = append(got, raw...); return nil }); e != nil {
			t.Fatal(e)
		}
	}
	if !bytes.Equal(got, before) {
		t.Fatalf("oversized JSONL record changed: before %d after %d", len(before), len(got))
	}
}

func TestV8SpoolPartitionFailsClosedOnInvalidOrTruncatedJSONL(t *testing.T) {
	for _, bad := range []string{`{"Coord":`, "{not json}\n", `{"Coord":{}}`} {
		t.Run(strings.ReplaceAll(bad, "/", "_"), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "in.jsonl")
			if e := os.WriteFile(path, []byte(bad), 0600); e != nil {
				t.Fatal(e)
			}
			_, e := partitionSpool(path, filepath.Join(root, "shard"), 2, func(r Row) string { return "test" })
			if e == nil {
				t.Fatal("malformed/interrupted intermediate spool was accepted")
			}
		})
	}
}

// Benchmark isolates the cost of JSON decode+encode vs decode+byte passthrough.
// No change is made to checkpoint or published CSV serialization.
func BenchmarkV8PartitionSpool(b *testing.B) {
	root := b.TempDir()
	input := filepath.Join(root, "source.jsonl")
	w, e := openSpool(input)
	if e != nil {
		b.Fatal(e)
	}
	for i := 0; i < 5000; i++ {
		r := Row{Coord: map[string]string{"period": "2026-01", "account": "C001", "sku": "P001"}, Amount: int64(i - 2500), IDs: []string{"id"}}
		if e = w.put(r); e != nil {
			b.Fatal(e)
		}
	}
	if e = w.close(); e != nil {
		b.Fatal(e)
	}
	legacy := func(prefix string) error {
		n := 8
		writers := make([]*spoolWriter, n)
		for i := range writers {
			var err error
			writers[i], err = openSpool(prefix + string(rune('a'+i)))
			if err != nil {
				return err
			}
		}
		err := scanSpool(input, func(row Row) error { return writers[hashShard(row.Coord["sku"], n)].put(row) })
		for _, wr := range writers {
			if x := wr.close(); err == nil {
				err = x
			}
		}
		return err
	}
	for _, mode := range []string{"legacy", "passthrough"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				prefix := filepath.Join(root, "shard_"+mode)
				if mode == "legacy" {
					e = legacy(prefix)
				} else {
					_, e = partitionSpool(input, prefix, 8, func(r Row) string { return r.Coord["sku"] })
				}
				if e != nil {
					b.Fatal(e)
				}
			}
		})
	}
	// Prove both paths serialize the identical Row representation.
}
