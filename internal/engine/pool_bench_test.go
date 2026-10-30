package engine

import (
	"fmt"
	"testing"
)

// BenchmarkPoolScheduler isolates executor scheduling overhead from disk IO,
// financial rounding and evidence serialization. It is not an end-to-end
// throughput number and must never be presented as one.
func BenchmarkPoolScheduler(b *testing.B) {
	keys := make([]string, 4096)
	for i := range keys {
		keys[i] = fmt.Sprintf("POOL-%05d", i)
	}
	for _, w := range []int{1, 4} {
		b.Run(fmt.Sprintf("workers_%d", w), func(b *testing.B) {
			b.ReportAllocs()
			compute := func(k string) poolResult { return poolResult{input: 1, output: 1} }
			consume := func(k string, r poolResult) error { return nil }
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if e := parallelPools(keys, w, compute, consume); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}
