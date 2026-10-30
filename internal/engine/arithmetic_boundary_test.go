package engine

import (
	"math"
	"testing"
)

func TestInt64MinApportionExact(t *testing.T) {
	out, err := Apportion(math.MinInt64, []int64{1}, []string{"X"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0] != math.MinInt64 {
		t.Fatalf("unexpected apportion %v", out)
	}
	if v, e := checkedSub(-1, math.MinInt64); e != nil || v != math.MaxInt64 {
		t.Fatalf("checkedSub boundary %d %v", v, e)
	}
	if _, e := checkedSub(0, math.MinInt64); e == nil {
		t.Fatal("overflow not detected")
	}
}
