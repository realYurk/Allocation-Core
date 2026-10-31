package engine

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func makeSkewSpool(t *testing.T, count, cardinality int) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "skew.jsonl")
	w, err := openSpool(f)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		r := Row{Coord: map[string]string{"period": "2026-01", "product": fmt.Sprintf("SKU-%06d", i%cardinality)}, Amount: int64(i%11 - 5), IDs: []string{fmt.Sprintf("source-%09d-%s", i, strings.Repeat("x", 160))}}
		if err := w.put(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestV10StreamingCoalesceExactOrderAndTrace(t *testing.T) {
	dims := []string{"period", "product"}
	path := makeSkewSpool(t, 20000, 16)
	stream, err := shouldStreamCoalesceSpool(path, dims)
	if err != nil || !stream {
		t.Fatalf("expected skew streaming path, stream=%v err=%v", stream, err)
	}
	original := []Row{}
	if err := scanSpool(path, func(r Row) error { original = append(original, r); return nil }); err != nil {
		t.Fatal(err)
	}
	expected, err := mergeRows(original, dims)
	if err != nil {
		t.Fatal(err)
	}
	var baseline, output [][]string
	if err := linkCoalesced(original, expected, dims, "S1", 6, func(r []string) error { baseline = append(baseline, append([]string(nil), r...)); return nil }); err != nil {
		t.Fatal(err)
	}
	actual, err := streamCoalescedSpool(path, dims, "S1", 6, func(r []string) error { output = append(output, append([]string(nil), r...)); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("output rows differ: %v vs %v", actual, expected)
	}
	if !reflect.DeepEqual(output, baseline) {
		t.Fatalf("lineage mismatch length %d != %d", len(output), len(baseline))
	}
	if len(actual) != 16 {
		t.Fatalf("got %d groups", len(actual))
	}
}
func TestV10SparseCoalesceUsesLegacyFastPath(t *testing.T) {
	path := makeSkewSpool(t, 10000, 10000)
	use, err := shouldStreamCoalesceSpool(path, []string{"period", "product"})
	if err != nil || use {
		t.Fatalf("unique coordinate fast path use=%t err=%v", use, err)
	}
}
func TestV10SkewOverflowAndCorruptionFailClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.jsonl")
	w, e := openSpool(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, amount := range []int64{math.MaxInt64, 1} {
		if e = w.put(Row{Coord: map[string]string{"sku": "A"}, Amount: amount, IDs: []string{"src"}}); e != nil {
			t.Fatal(e)
		}
	}
	if e = w.close(); e != nil {
		t.Fatal(e)
	}
	if _, e = streamCoalescedSpool(path, []string{"sku"}, "S", 6, func([]string) error { return nil }); e == nil {
		t.Fatal("expected signed overflow rejection")
	}
	if e = os.WriteFile(path, []byte("{\"Coord\": {\"sku\": \"A\"}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = streamCoalescedSpool(path, []string{"sku"}, "S", 6, func([]string) error { return nil }); e == nil {
		t.Fatal("expected malformed JSONL rejection")
	}
}
func BenchmarkV10SkewCoalescing(b *testing.B) {
	// Benchmark a low-cardinality partition, excluding fixture generation.
	root := b.TempDir()
	path := filepath.Join(root, "spool.jsonl")
	w, e := openSpool(path)
	if e != nil {
		b.Fatal(e)
	}
	for i := 0; i < 30000; i++ {
		if e = w.put(Row{Coord: map[string]string{"period": "2026-01", "product": fmt.Sprintf("SKU-%04d", i%100)}, Amount: int64(i % 7), IDs: []string{fmt.Sprintf("source-%08d", i)}}); e != nil {
			b.Fatal(e)
		}
	}
	if e = w.close(); e != nil {
		b.Fatal(e)
	}
	dims := []string{"period", "product"}
	b.Run("legacy", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var rows []Row
			if e := scanSpool(path, func(r Row) error { rows = append(rows, r); return nil }); e != nil {
				b.Fatal(e)
			}
			merged, e := mergeRows(rows, dims)
			if e != nil {
				b.Fatal(e)
			}
			if e = linkCoalesced(rows, merged, dims, "S", 6, func([]string) error { return nil }); e != nil {
				b.Fatal(e)
			}
		}
	})
	b.Run("stream", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, e := streamCoalescedSpool(path, dims, "S", 6, func([]string) error { return nil }); e != nil {
				b.Fatal(e)
			}
		}
	})
}


// A unique first-256 prefix must not push a large, late-skewed spool into
// the full []Row path. Compare both financial rows and provenance edges.
func TestLargeLateRepeatedCoordinateSpool(t *testing.T) {
 dims:=[]string{"period","product"}
 path:=filepath.Join(t.TempDir(),"late-repeat.jsonl")
 w,err:=openSpool(path);if err!=nil{t.Fatal(err)}
 for i:=0;i<13500;i++{
  product:="HotProduct"
  if i<256{product=fmt.Sprintf("Unique-%03d",i)}
  row:=Row{Coord:map[string]string{"period":"2026-01","product":product},Amount:int64(i%7-3),IDs:[]string{fmt.Sprintf("id-%06d-%s",i,strings.Repeat("x",1400))}}
  if err:=w.put(row);err!=nil{t.Fatal(err)}
 }
 if err:=w.close();err!=nil{t.Fatal(err)}
 info,err:=os.Stat(path);if err!=nil{t.Fatal(err)}
 if info.Size()<16<<20{t.Fatalf("fixture size %d less than cutoff",info.Size())}
 use,err:=shouldStreamCoalesceSpool(path,dims)
 if err!=nil||!use{t.Fatalf("large delayed skew not protected: use=%v err=%v",use,err)}
 var original []Row
 if err:=scanSpool(path,func(r Row)error{original=append(original,r);return nil});err!=nil{t.Fatal(err)}
 expected,err:=mergeRows(original,dims);if err!=nil{t.Fatal(err)}
 var oldTrace,newTrace [][]string
 if err:=linkCoalesced(original,expected,dims,"S1",2,func(v []string)error{oldTrace=append(oldTrace,append([]string(nil),v...));return nil});err!=nil{t.Fatal(err)}
 actual,err:=streamCoalescedSpool(path,dims,"S1",2,func(v []string)error{newTrace=append(newTrace,append([]string(nil),v...));return nil})
 if err!=nil{t.Fatal(err)}
 if !reflect.DeepEqual(expected,actual)||!reflect.DeepEqual(oldTrace,newTrace){t.Fatal("spool path changed outputs or lineage")}
 if len(actual)!=257{t.Fatalf("expected 257 coordinates, got %d",len(actual))}
}
