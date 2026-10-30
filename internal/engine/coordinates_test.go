package engine

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func legacyMergeRowsForTest(rows []Row, dims []string) ([]Row, error) {
	// Ground truth: call the original branch by using small chunks? For tests,
	// clone original implementation directly to catch ordering and ID changes.
	m := map[string]Row{}
	for _, r := range rows {
		k := key(r.Coord, dims)
		if old, ok := m[k]; ok {
			v, e := checkedAdd(old.Amount, r.Amount)
			if e != nil {
				return nil, e
			}
			old.Amount = v
			m[k] = old
		} else {
			m[k] = r
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Row, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out, nil
}

func syntheticCoordinates(n, cardinality int) ([]Row, []string) {
	dims := []string{"period", "site", "item", "account", "currency"}
	rng := rand.New(rand.NewSource(87))
	rows := make([]Row, n)
	for i := range rows {
		j := rng.Intn(cardinality)
		rows[i] = Row{Coord: map[string]string{"period": fmt.Sprint("2026-", 1+j%12), "site": fmt.Sprintf("Site%03d", j%10), "item": fmt.Sprintf("I%05d", j), "account": fmt.Sprintf("A%03d", j%100), "currency": "CNY"}, Amount: int64(j%91 + 1), IDs: []string{fmt.Sprint(i)}}
	}
	return rows, dims
}
func TestPackedCoordinatesExactAndSafe(t *testing.T) {
	d := newCoordinateDictionary([]string{"a", "b"})
	ids := map[string]string{}
	for _, coord := range []map[string]string{{"a": "", "b": "x"}, {"a": "x", "b": ""}, {"a": "<>&", "b": "你好"}, {"a": "\x00", "b": "\\n"}, {"a": "x", "b": ""}, {"b": "x"}} {
		packed, e := d.packed(coord)
		if e != nil {
			t.Fatal(e)
		}
		prior, seen := ids[packed]
		logical := key(coord, []string{"a", "b"})
		if seen && prior != logical {
			t.Fatalf("ID collision: %q %q", prior, logical)
		}
		ids[packed] = logical
	}
}
func TestPackedMergeMatchesLegacy(t *testing.T) {
	for _, card := range []int{20, 500, 5000, 10000} {
		rows, dims := syntheticCoordinates(10000, card)
		want, e := legacyMergeRowsForTest(rows, dims)
		if e != nil {
			t.Fatal(e)
		}
		got, e := mergeRowsIndexed(rows, dims)
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("packed merge differs at cardinality %d", card)
		}
		adaptive, e := mergeRows(rows, dims)
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(adaptive, want) {
			t.Fatalf("adaptive merge differs at cardinality %d", card)
		}
	}
}
func TestPackedMergeOverflow(t *testing.T) {
	dims := []string{"d"}
	rows := []Row{{Coord: map[string]string{"d": "x"}, Amount: 9223372036854775807}, {Coord: map[string]string{"d": "x"}, Amount: 1}}
	if _, e := mergeRowsIndexed(rows, dims); e == nil {
		t.Fatal("overflow must fail closed")
	}
}
func BenchmarkMergeCoordinate(b *testing.B) {
	for _, card := range []int{100, 20000} {
		rows, dims := syntheticCoordinates(20000, card)
		b.Run(fmt.Sprintf("card_%d_legacy", card), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, e := legacyMergeRowsForTest(rows, dims); e != nil {
					b.Fatal(e)
				}
			}
		})
		b.Run(fmt.Sprintf("card_%d_adaptive", card), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, e := mergeRows(rows, dims); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}
