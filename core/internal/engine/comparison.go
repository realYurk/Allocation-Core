package engine

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CompareBeforeAfter aggregates source and final results independently. It never
// invents SKU-level source allocations: if a dimension is only created by the
// allocation, the original pool member remains visible in the before side.
// The output is a display report; it is not the source of financial reconciliation.
func CompareBeforeAfter(rulePath, resultsDir, outputPath string, groupBy []string) error {
	raw, err := os.ReadFile(rulePath)
	if err != nil {
		return err
	}
	spec, err := ParseSpec(raw)
	if err != nil {
		return err
	}
	if _, err = Compile(spec); err != nil {
		return err
	}
	if len(groupBy) == 0 {
		return fail("comparison needs at least one group dimension")
	}
	seen := map[string]bool{}
	for _, dim := range groupBy {
		if !has(spec.Dimensions, dim) || seen[dim] {
			return fail("unknown or duplicate comparison dimension %s", dim)
		}
		seen[dim] = true
	}
	// Respect the true original source granularity. Individual SKUs may only exist after allocation.
	before, after := map[string]int64{}, map[string]int64{}
	members := map[string][]string{}
	add := func(path string, totals map[string]int64) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		cr := csv.NewReader(f)
		cr.FieldsPerRecord = -1
		header, err := cr.Read()
		if err != nil {
			return err
		}
		pos := map[string]int{}
		for i, h := range header {
			pos[strings.TrimPrefix(h, "\ufeff")] = i
		}
		for _, d := range groupBy {
			if _, ok := pos[d]; !ok {
				return fail("%s missing comparison dimension %s", path, d)
			}
		}
		amountIdx, ok := pos["amount"]
		if !ok {
			return fail("%s missing amount", path)
		}
		line := 1
		for {
			rec, err := cr.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("%s:%d: %w", path, line+1, err)
			}
			line++
			if amountIdx >= len(rec) {
				return fail("%s:%d short amount row", path, line)
			}
			coord := make([]string, len(groupBy))
			for i, d := range groupBy {
				idx := pos[d]
				if idx >= len(rec) {
					return fail("%s:%d short %s", path, line, d)
				}
				coord[i] = rec[idx]
			}
			k := strings.Join(coord, "\x00")
			value, err := ParseScaled(rec[amountIdx], spec.Precision)
			if err != nil {
				return fmt.Errorf("%s:%d: %w", path, line, err)
			}
			totals[k], err = checkedAdd(totals[k], value)
			if err != nil {
				return err
			}
			members[k] = coord
		}
		return nil
	}
	base := filepath.Dir(rulePath)
	usedSources := map[string]bool{}
	for _, stage := range spec.Stages {
		for _, edge := range stage.Inputs {
			if _, ok := spec.Datasets.Amounts[edge.From]; ok {
				usedSources[edge.From] = true
			}
		}
	}
	datasetNames := make([]string, 0, len(usedSources))
	for name := range usedSources {
		datasetNames = append(datasetNames, name)
	}
	sort.Strings(datasetNames)
	for _, name := range datasetNames {
		p, err := Resolve(base, spec.Datasets.Amounts[name])
		if err != nil {
			return err
		}
		if err := add(p, before); err != nil {
			return err
		}
	}
	if err := add(filepath.Join(resultsDir, "results.csv"), after); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}
	sink, err := newSink(outputPath, append(append([]string{}, groupBy...), "before", "after", "delta_after_minus_before"))
	if err != nil {
		return err
	}
	defer sink.abort()
	keys := make([]string, 0, len(members))
	for k := range members {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sumBefore, sumAfter int64
	for _, k := range keys {
		var err error
		sumBefore, err = checkedAdd(sumBefore, before[k])
		if err != nil {
			return err
		}
		sumAfter, err = checkedAdd(sumAfter, after[k])
		if err != nil {
			return err
		}
		diff, err := checkedAdd(after[k], -before[k])
		if err != nil {
			return err
		}
		row := append(append([]string{}, members[k]...), Money(before[k], spec.Precision), Money(after[k], spec.Precision), Money(diff, spec.Precision))
		if err := sink.write(row); err != nil {
			return err
		}
	}
	diff, err := checkedAdd(sumAfter, -sumBefore)
	if err != nil {
		return err
	}
	totalRow := make([]string, len(groupBy))
	for i := range totalRow {
		totalRow[i] = "__TOTAL__"
	}
	if err := sink.write(append(totalRow, Money(sumBefore, spec.Precision), Money(sumAfter, spec.Precision), Money(diff, spec.Precision))); err != nil {
		return err
	}
	return sink.finish()
}
