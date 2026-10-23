package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

const Schema = "allocation-ir/2.0"
const poolMember = "__POOL__"
const unit int64 = 1000000

type Spec struct {
	Schema      string                         `json:"schema"`
	Name        string                         `json:"name"`
	Precision   int                            `json:"precision"`
	Dimensions  []string                       `json:"dimensions"`
	Immutable   []string                       `json:"immutable_dimensions"`
	Hierarchies map[string]map[string][]string `json:"hierarchies"`
	Datasets    struct {
		Amounts map[string]string `json:"amounts"`
		Drivers map[string]string `json:"drivers"`
	} `json:"datasets"`
	Stages []Stage `json:"stages"`
}
type Filter struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}
type Source struct {
	Grouping map[string]string `json:"grouping"`
	Filters  map[string]Filter `json:"filters"`
}
type Target struct {
	Mode    string   `json:"mode"`
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
	Value   string   `json:"value"`
}
type InputEdge struct {
	From  string `json:"from"`
	Share string `json:"share"`
}
type Allocation struct {
	ID             string            `json:"id"`
	Enabled        *bool             `json:"enabled"`
	Share          string            `json:"share"`
	DriverDataset  string            `json:"driver_dataset"`
	WeightField    string            `json:"weight_field"`
	DriverFilters  map[string]Filter `json:"driver_filters"`
	NegativePolicy string            `json:"negative_driver_policy"`
}
type Stage struct {
	Operation        string            `json:"operation"` // ALLOCATE(default), MERGE, PASS
	ID               string            `json:"id"`
	Inputs           []InputEdge       `json:"inputs"`
	Source           Source            `json:"source"`
	Target           map[string]Target `json:"target"`
	ConservationKeys []string          `json:"conservation_keys"`
	Allocations      []Allocation      `json:"allocations"`
	Enabled          *bool             `json:"enabled"`
}
type Row struct {
	Coord  map[string]string
	Amount int64
	IDs    []string
}
type DriverRow struct {
	Coord   map[string]string
	Weights map[string]int64
}

type Planned struct {
	Spec     Spec
	Ordered  []Stage
	Outgoing map[string][]route
}
type route struct {
	To    string
	Share int64
}

func ParseSpec(b []byte) (Spec, error) {
	var s Spec
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return s, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return s, fail("rule JSON must contain exactly one object, trailing data: %v", err)
	}
	return s, nil
}
func fail(f string, args ...interface{}) error { return fmt.Errorf(f, args...) }
func ParseScaled(s string, scale int) (int64, error) {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return 0, fail("empty decimal")
	}
	sign := int64(1)
	if s[0] == '-' {
		sign = -1
		s = s[1:]
	} else if s[0] == '+' {
		s = s[1:]
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || len(parts[0]) == 0 {
		return 0, fail("invalid decimal %q", s)
	}
	for _, part := range parts {
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, fail("invalid decimal %q", s)
			}
		}
	}
	whole, ok := new(big.Int).SetString(parts[0], 10)
	if !ok {
		return 0, fail("invalid number")
	}
	mult := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	whole.Mul(whole, mult)
	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) > scale {
			return 0, fail("decimal scale exceeds %d: %s", scale, s)
		}
		if frac != "" {
			f, _ := new(big.Int).SetString(frac+strings.Repeat("0", scale-len(frac)), 10)
			whole.Add(whole, f)
		}
	}
	if sign < 0 {
		whole.Neg(whole)
	}
	if !whole.IsInt64() {
		return 0, fail("decimal int64 overflow %q", s)
	}
	return whole.Int64(), nil
}
func Money(v int64, p int) string {
	neg := ""
	u := uint64(v)
	if v < 0 {
		neg = "-"
		u = uint64(-(v + 1)) + 1
	}
	d := uint64(math.Pow10(p))
	if p == 0 {
		return neg + strconv.FormatUint(u, 10)
	}
	return fmt.Sprintf("%s%d.%0*d", neg, u/d, p, u%d)
}
func checkedAdd(x, y int64) (int64, error) {
	if y > 0 && x > math.MaxInt64-y || y < 0 && x < math.MinInt64-y {
		return 0, fail("amount overflow")
	}
	return x + y, nil
}
func key(coord map[string]string, dims []string) string {
	a := make([]string, 0, len(dims))
	for _, d := range dims {
		a = append(a, coord[d])
	}
	b, _ := json.Marshal(a)
	return string(b)
}
func copyCoord(c map[string]string) map[string]string {
	n := make(map[string]string, len(c))
	for k, v := range c {
		n[k] = v
	}
	return n
}
func Enabled(flag *bool) bool { return flag == nil || *flag }

// Split exactly: integer currency units; deterministic largest remainder; supports signed input.
func Apportion(total int64, weights []int64, identities []string, allowSigned bool) ([]int64, error) {
	if len(weights) == 0 || len(weights) != len(identities) {
		return nil, fail("empty/mismatched weights")
	}
	sum := big.NewInt(0)
	for _, w := range weights {
		if w < 0 && !allowSigned {
			return nil, fail("negative driver without explicit permission")
		}
		sum.Add(sum, big.NewInt(w))
	}
	if sum.Sign() <= 0 {
		return nil, fail("driver denominator must be positive")
	}
	vals := make([]int64, len(weights))
	rems := make([]*big.Int, len(weights))
	allocated := int64(0)
	for i, w := range weights {
		num := new(big.Int).Mul(big.NewInt(total), big.NewInt(w))
		q := new(big.Int)
		rem := new(big.Int)
		q.QuoRem(num, sum, rem)
		if !q.IsInt64() {
			return nil, fail("allocated amount overflows int64")
		}
		vals[i] = q.Int64()
		rems[i] = rem
		var err error
		allocated, err = checkedAdd(allocated, vals[i])
		if err != nil {
			return nil, err
		}
	}
	res, err := checkedAdd(total, -allocated)
	if err != nil {
		return nil, err
	}
	if res > int64(len(weights)) || res < -int64(len(weights)) {
		return nil, fail("unexpected rounding residual: %d", res)
	}
	order := make([]int, len(weights))
	for i := range order {
		order[i] = i
	}
	positive := res > 0
	sort.Slice(order, func(i, j int) bool {
		cmp := rems[order[i]].Cmp(rems[order[j]])
		if cmp != 0 {
			if positive {
				return cmp > 0
			}
			return cmp < 0
		}
		return identities[order[i]] < identities[order[j]]
	})
	for i := int64(0); i < res || i < -res; i++ {
		ix := order[i%int64(len(order))]
		if positive {
			vals[ix]++
		} else {
			vals[ix]--
		}
	}
	return vals, nil
}
func cloneRows(rows []Row) []Row { v := make([]Row, len(rows)); copy(v, rows); return v }
func has(s []string, x string) bool {
	for _, t := range s {
		if t == x {
			return true
		}
	}
	return false
}
func expand(dim string, elements []string, hierarchy map[string]map[string][]string) (map[string]bool, error) {
	set := map[string]bool{}
	stack := map[string]bool{}
	var walk func(string) error
	walk = func(m string) error {
		if stack[m] {
			return fail("hierarchy cycle: %s/%s", dim, m)
		}
		children, ok := hierarchy[dim][m]
		if !ok {
			set[m] = true
			return nil
		}
		if len(children) == 0 {
			return fail("empty hierarchy %s/%s", dim, m)
		}
		stack[m] = true
		for _, c := range children {
			if err := walk(c); err != nil {
				return err
			}
		}
		delete(stack, m)
		return nil
	}
	for _, m := range elements {
		if err := walk(m); err != nil {
			return nil, err
		}
	}
	return set, nil
}
func compileFilter(dim string, f Filter, h map[string]map[string][]string) (func(string) bool, error) {
	inc, err := expand(dim, f.Include, h)
	if err != nil {
		return nil, err
	}
	exc, err := expand(dim, f.Exclude, h)
	if err != nil {
		return nil, err
	}
	return func(m string) bool { return (len(f.Include) == 0 || inc[m]) && !exc[m] }, nil
}
