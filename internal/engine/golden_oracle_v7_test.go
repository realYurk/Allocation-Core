package engine_test

// Financial conformance tests intentionally live in an external package.
// The oracle uses rational arithmetic and is independent of engine.Apportion.
// These examples are synthetic, with explicit expected cents, not company rules.
import (
	"allocationcore/internal/engine"
	"math"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type goldenCase struct {
	name    string
	total   int64
	weights []int64
	ids     []string
	signed  bool
	want    []int64
}

func TestV7IndependentGoldenAmounts(t *testing.T) {
	cases := []goldenCase{
		{"one cent ties", 1, []int64{1, 1, 1}, []string{"B", "C", "A"}, false, []int64{0, 0, 1}},
		{"one hundred three ways", 100, []int64{1, 1, 1}, []string{"A", "B", "C"}, false, []int64{34, 33, 33}},
		{"negative amount", -100, []int64{1, 1, 1}, []string{"A", "B", "C"}, false, []int64{-34, -33, -33}},
		{"nonuniform factor", 101, []int64{6, 3, 1}, []string{"A", "B", "C"}, false, []int64{61, 30, 10}},
		{"negative nonuniform", -101, []int64{6, 3, 1}, []string{"A", "B", "C"}, false, []int64{-61, -30, -10}},
		{"signed factors", 40, []int64{2, -1, 3}, []string{"A", "B", "C"}, true, []int64{20, -10, 30}},
		{"signed remainder", 1, []int64{2, -1, 3}, []string{"A", "B", "C"}, true, []int64{0, 0, 1}},
		{"min signed amount", math.MinInt64, []int64{1, 1}, []string{"A", "B"}, false, []int64{math.MinInt64 / 2, math.MinInt64 / 2}},
		{"max signed amount", math.MaxInt64, []int64{1}, []string{"A"}, false, []int64{math.MaxInt64}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := engine.Apportion(c.total, c.weights, c.ids, c.signed)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got=%v want=%v err=%v", got, c.want, err)
			}
		})
	}
}

func rationalOracle(total int64, weights []int64, ids []string) ([]int64, bool) {
	if len(weights) == 0 || len(weights) != len(ids) {
		return nil, false
	}
	denominator := new(big.Int)
	for _, w := range weights {
		denominator.Add(denominator, big.NewInt(w))
	}
	if denominator.Sign() <= 0 {
		return nil, false
	}
	type part struct {
		i        int
		fraction *big.Rat
	}
	out := make([]int64, len(weights))
	remainder := new(big.Int).SetInt64(total)
	parts := make([]part, 0, len(weights))
	for i, w := range weights {
		// Direct independent rational ideal for every target.
		ideal := new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(total), big.NewInt(w)), denominator)
		integer := new(big.Int).Quo(ideal.Num(), ideal.Denom())
		if !integer.IsInt64() {
			return nil, false
		}
		out[i] = integer.Int64()
		remainder.Sub(remainder, integer)
		fraction := new(big.Rat).Sub(ideal, new(big.Rat).SetInt(integer))
		parts = append(parts, part{i, fraction})
	}
	if !remainder.IsInt64() {
		return nil, false
	}
	rest := remainder.Int64()
	ascending := rest < 0
	sort.Slice(parts, func(i, j int) bool {
		x, y := parts[i], parts[j]
		cmp := x.fraction.Cmp(y.fraction)
		if cmp != 0 {
			if ascending {
				return cmp < 0
			}
			return cmp > 0
		}
		return ids[x.i] < ids[y.i]
	})
	if rest > int64(len(weights)) || rest < -int64(len(weights)) {
		return nil, false
	}
	for i := int64(0); i < rest || i < -rest; i++ {
		v := &out[parts[int(i)%len(parts)].i]
		if ascending {
			*v--
		} else {
			*v++
		}
	}
	return out, true
}

func TestV7RationalOracleAndPermutation(t *testing.T) {
	r := rand.New(rand.NewSource(771002))
	for iter := 0; iter < 5000; iter++ {
		n := 2 + r.Intn(8)
		total := int64(r.Intn(200001) - 100000)
		weights := make([]int64, n)
		ids := make([]string, n)
		for i := range weights {
			weights[i] = int64(r.Intn(21) - 5)
			ids[i] = string(rune('A' + i))
		}
		expected, ok := rationalOracle(total, weights, ids)
		got, err := engine.Apportion(total, weights, ids, true)
		if !ok {
			if err == nil {
				t.Fatalf("accepted invalid denominator at %d", iter)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, expected) {
			t.Fatalf("case %d, total=%d weights=%v got=%v oracle=%v err=%v", iter, total, weights, got, expected, err)
		}
		if gotAgain, e := engine.Apportion(total, weights, ids, true); e != nil || !reflect.DeepEqual(got, gotAgain) {
			t.Fatalf("not deterministic %d", iter)
		}
		perm := r.Perm(n)
		wp, idp := make([]int64, n), make([]string, n)
		for i, pi := range perm {
			wp[i], idp[i] = weights[pi], ids[pi]
		}
		gp, e := engine.Apportion(total, wp, idp, true)
		if e != nil {
			t.Fatalf("perm failed: %v", e)
		}
		for i, pi := range perm {
			if gp[i] != expected[pi] {
				t.Fatalf("permutation changed cents at %d: %v vs %v", iter, gp, expected)
			}
		}
		sum := new(big.Int)
		for _, x := range got {
			sum.Add(sum, big.NewInt(x))
		}
		if sum.Cmp(big.NewInt(total)) != 0 {
			t.Fatalf("not conservative at %d", iter)
		}
	}
}

func TestV7InvalidDriverFailClosed(t *testing.T) {
	for _, tc := range []struct {
		weights []int64
		signed  bool
	}{
		{[]int64{0, 0}, false}, {[]int64{2, -1}, false}, {[]int64{-2, -3}, true}, {[]int64{}, true},
	} {
		ids := make([]string, len(tc.weights))
		for i := range ids {
			ids[i] = string(rune('A' + i))
		}
		if _, err := engine.Apportion(1, tc.weights, ids, tc.signed); err == nil {
			t.Fatalf("accepted bad factor %+v", tc)
		}
	}
}
