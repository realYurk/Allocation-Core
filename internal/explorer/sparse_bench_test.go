package explorer

import (
	"bufio"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

// Synthetic performance probe only; not a forecast for real multidimensional data.
func BenchmarkMillionRowExactFilter(b *testing.B) {
	p := filepath.Join(b.TempDir(), "synthetic.csv")
	f, e := os.Create(p)
	if e != nil {
		b.Fatal(e)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	fmt.Fprintln(w, "period,metric,value")
	for i := 0; i < 1000000; i++ {
		fmt.Fprintf(w, "P%04d,COST,1.00\n", i/4096)
	}
	if e := w.Flush(); e != nil {
		b.Fatal(e)
	}
	f.Close()
	a, e := newApp(b.TempDir())
	if e != nil {
		b.Fatal(e)
	}
	dims := []string{"period", "metric"}
	filters := map[string]string{"period": "P0007"}
	_, _, e = a.scanFiltered(p, dims, filters, func(_ map[string]string, _ *big.Rat) error { return nil })
	if e != nil {
		b.Fatal(e)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hit := 0
		scanned, skipped, e := a.scanFiltered(p, dims, filters, func(_ map[string]string, _ *big.Rat) error { hit++; return nil })
		if e != nil || hit != 4096 || skipped < 950000 || scanned > 10000 {
			b.Fatalf("hit %d scan %d skip %d err %v", hit, scanned, skipped, e)
		}
	}
}
func BenchmarkMillionRowRawFilter(b *testing.B) {
	p := filepath.Join(b.TempDir(), "synthetic.csv")
	f, e := os.Create(p)
	if e != nil {
		b.Fatal(e)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	fmt.Fprintln(w, "period,metric,value")
	for i := 0; i < 1000000; i++ {
		fmt.Fprintf(w, "P%04d,COST,1.00\n", i/4096)
	}
	if e := w.Flush(); e != nil {
		b.Fatal(e)
	}
	f.Close()
	dims := []string{"period", "metric"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hit := 0
		e := scanRecords(p, dims, func(coord map[string]string, _ *big.Rat) error {
			if coord["period"] == "P0007" {
				hit++
			}
			return nil
		})
		if e != nil || hit != 4096 {
			b.Fatalf("hit %d err %v", hit, e)
		}
	}
}
