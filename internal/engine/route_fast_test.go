package engine

import (
	"math"
	"reflect"
	"testing"
)

func legacyRouteForTest(rows []Row, edges []route, inbox map[string][]Row) error {
	shares := make([]int64, len(edges))
	names := make([]string, len(edges))
	for i, e := range edges {
		shares[i] = e.Share
		names[i] = e.To
	}
	for _, r := range rows {
		values, err := Apportion(r.Amount, shares, names, false)
		if err != nil {
			return err
		}
		for i, v := range values {
			if v != 0 {
				inbox[edges[i].To] = append(inbox[edges[i].To], Row{Coord: r.Coord, Amount: v, IDs: r.IDs})
			}
		}
	}
	return nil
}
func TestFullShareRouteMatchesOriginal(t *testing.T) {
	for _, tc := range []struct {
		name     string
		amounts  []int64
		existing bool
		edges    []route
	}{
		{"signed", []int64{1, -1, math.MaxInt64, math.MinInt64}, false, []route{{To: "a", Share: unit}}},
		{"empty", nil, false, []route{{To: "a", Share: unit}}},
		{"zero", []int64{1, 0, -1}, false, []route{{To: "a", Share: unit}}},
		{"existing", []int64{1, 2}, true, []route{{To: "a", Share: unit}}},
		{"branch", []int64{1, -1, 100}, false, []route{{To: "a", Share: 500000}, {To: "b", Share: 500000}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := make([]Row, len(tc.amounts))
			for i, v := range tc.amounts {
				rows[i] = Row{Coord: map[string]string{"sku": "A"}, Amount: v, IDs: []string{"original"}}
			}
			old, actual := map[string][]Row{}, map[string][]Row{}
			if tc.existing {
				old["a"] = []Row{{Amount: 42}}
				actual["a"] = []Row{{Amount: 42}}
			}
			e1 := legacyRouteForTest(rows, tc.edges, old)
			e2 := routeRows(rows, tc.edges, actual)
			if (e1 == nil) != (e2 == nil) || !reflect.DeepEqual(old, actual) {
				t.Fatalf("route differs: old=%v/%v new=%v/%v", old, e1, actual, e2)
			}
		})
	}
}
