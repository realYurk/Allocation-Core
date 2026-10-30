package explorer

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

func TestChunkSkipExactAndFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.csv")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	fmt.Fprintln(f, "period,metric,value")
	for i := 0; i < 12288; i++ {
		period := "P1"
		if i >= 4096 {
			period = "P2"
		}
		if i >= 8192 {
			period = "P3"
		}
		fmt.Fprintf(f, "%s,A,1\n", period)
	}
	f.Close()
	a, e := newApp(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	sum := new(big.Rat)
	scanned, skipped, e := a.scanFiltered(path, []string{"period", "metric"}, map[string]string{"period": "P2"}, func(_ map[string]string, v *big.Rat) error { sum.Add(sum, v); return nil })
	if e != nil || scanned != 4096 || skipped != 8192 || sum.Cmp(big.NewRat(4096, 1)) != 0 {
		t.Fatalf("scanned %d skip %d sum %s err %v", scanned, skipped, sum, e)
	}
}
func TestDictionaryOverflowNeverDropsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.csv")
	f, _ := os.Create(path)
	fmt.Fprintln(f, "item,value")
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(f, "I%d,1\n", i)
	}
	f.Close()
	a, _ := newApp(t.TempDir())
	found := 0
	_, _, e := a.scanFiltered(path, []string{"item"}, map[string]string{"item": "I3999"}, func(_ map[string]string, _ *big.Rat) error { found++; return nil })
	if e != nil || found != 1 {
		t.Fatal(e, found)
	}
}
