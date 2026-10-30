package engine

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestStageShardKeyCompatibility covers exact stable bytes, including escaping
// and previously missing coordinates; hash partitions must not drift.
func TestStageShardKeyCompatibility(t *testing.T) {
	dims := []string{"period", "site", "item", "account"}
	grouping := map[string]string{"period": "AS_IS", "site": "TOTAL", "item": "AS_IS", "account": "AS_IS"}
	st := Stage{Source: Source{Grouping: grouping}}
	values := []string{"a", "", "<html>&", "quote\"", "你好", "\\n", "\x00"}
	randSrc := rand.New(rand.NewSource(42))
	for i := 0; i < 2000; i++ {
		coord := make(map[string]string)
		legacy := make(map[string]string)
		for _, d := range dims {
			if i%13 != 0 {
				coord[d] = values[randSrc.Intn(len(values))]
			}
			if grouping[d] == "AS_IS" {
				legacy[d] = coord[d]
			} else {
				legacy[d] = poolMember
			}
		}
		row := Row{Coord: coord}
		got := stageShardKey(row, st, dims)
		want := key(legacy, dims)
		if got != want {
			t.Fatalf("shard changed: %q vs %q", got, want)
		}
	}
}

// BenchmarkShardEncoding explicitly compares pre-optimization representation
// with direct encoding. Synthetic microbenchmark, not end-to-end speed.
func BenchmarkShardEncoding(b *testing.B) {
	dims := []string{"period", "site", "product", "account", "region", "costcenter", "currency"}
	coord := map[string]string{"period": "2026-01", "site": "A", "product": "P-001", "account": "C-001", "region": "East", "costcenter": "CC01", "currency": "CNY"}
	st := Stage{Source: Source{Grouping: map[string]string{"period": "AS_IS", "site": "AS_IS", "product": "TOTAL", "account": "AS_IS", "region": "TOTAL", "costcenter": "AS_IS", "currency": "AS_IS"}}}
	row := Row{Coord: coord}
	b.Run("legacy_map", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m := make(map[string]string, len(dims))
			for _, d := range dims {
				if st.Source.Grouping[d] == "AS_IS" {
					m[d] = coord[d]
				} else {
					m[d] = poolMember
				}
			}
			_ = key(m, dims)
		}
	})
	b.Run("direct", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = stageShardKey(row, st, dims)
		}
	})
}

func BenchmarkLegacyCoordinateKey(b *testing.B) {
	dims := []string{"period", "site", "item", "account", "currency"}
	coord := map[string]string{"period": "2026-01", "site": "A", "item": "P001", "account": "C001", "currency": "CNY"}
	for _, n := range []int{1, 5} {
		b.Run(fmt.Sprint("dims_", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = key(coord, dims[:n])
			}
		})
	}
}
