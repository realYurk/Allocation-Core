package engine

// Packed coordinate IDs are an INTERNAL optimization only. Never emit these IDs
// as business keys, evidence IDs or row IDs: those must use original members.
// The dictionary is scoped to one merge; it is not a persistent cross-run ID.
import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

type coordinateDictionary struct {
	dims  []string
	index []map[string]uint32
}

func newCoordinateDictionary(dims []string) *coordinateDictionary {
	d := &coordinateDictionary{dims: append([]string(nil), dims...), index: make([]map[string]uint32, len(dims))}
	for i := range d.index {
		d.index[i] = make(map[string]uint32)
	}
	return d
}

func (c *coordinateDictionary) packed(coord map[string]string) (string, error) {
	var stack [64]byte // common case: <=16 dimensions, no transient heap buffer
	encoded := stack[:0]
	if len(c.dims) > 16 {
		encoded = make([]byte, 0, 4*len(c.dims))
	}
	for i, d := range c.dims {
		v := coord[d] // match legacy key: missing member and empty member are equivalent
		id, exists := c.index[i][v]
		if !exists {
			if uint64(len(c.index[i])) >= math.MaxUint32 {
				return "", fmt.Errorf("too many distinct members in %s", d)
			}
			id = uint32(len(c.index[i]) + 1)
			c.index[i][v] = id
		}
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], id)
		encoded = append(encoded, b[:]...)
	}
	return string(encoded), nil
}

// mergeRowsIndexed only substitutes an internal grouping key. It still sorts
// using the legacy JSON coordinate key so result order/evidence stay identical.
func mergeRowsIndexed(rows []Row, dims []string) ([]Row, error) {
	c := newCoordinateDictionary(dims)
	type entry struct {
		row    Row
		legacy string
	}
	groups := make(map[string]*entry)
	for _, r := range rows {
		packed, err := c.packed(r.Coord)
		if err != nil {
			return nil, err
		}
		if existing, found := groups[packed]; found {
			next, e := checkedAdd(existing.row.Amount, r.Amount)
			if e != nil {
				return nil, e
			}
			existing.row.Amount = next
		} else {
			groups[packed] = &entry{row: r, legacy: key(r.Coord, dims)}
		}
	}
	keys := make([]string, 0, len(groups))
	for packed := range groups {
		keys = append(keys, packed)
	}
	sort.Slice(keys, func(i, j int) bool { return groups[keys[i]].legacy < groups[keys[j]].legacy })
	out := make([]Row, 0, len(keys))
	for _, packed := range keys {
		out = append(out, groups[packed].row)
	}
	return out, nil
}

func shouldUsePackedMerge(rows []Row, dims []string) bool {
	if len(rows) < 4096 || len(dims) == 0 || len(dims) > 32 {
		return false
	}
	const window = 256
	seen := make(map[string]struct{}, window)
	duplicates := 0
	for i := 0; i < window; i++ {
		k := key(rows[i].Coord, dims)
		if _, found := seen[k]; found {
			duplicates++
		} else {
			seen[k] = struct{}{}
		}
	}
	// In many multi-dimensional datasets all rows are unique; the extra
	// dictionaries would waste memory. Stay on the original grouping path.
	return duplicates >= 32
}
