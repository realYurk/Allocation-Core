package engine

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Doctor checks the presence and CSV column contracts of source and driver files.
// It is a cheap static preflight: it does not check every row or driver denominator.
func Doctor(rulePath string, spec Spec) ([]string, error) {
	notes := []string{}
	base := filepath.Dir(rulePath)
	headers := func(relative string) (map[string]bool, error) {
		p, err := Resolve(base, relative)
		if err != nil {
			return nil, err
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("input missing %s: %w", relative, err)
		}
		defer f.Close()
		row, err := csv.NewReader(f).Read()
		if err != nil {
			return nil, fmt.Errorf("cannot read CSV header %s: %w", relative, err)
		}
		pos, err := HeaderPositions(row)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", relative, err)
		}
		hs := map[string]bool{}
		for k := range pos {
			hs[k] = true
		}
		return hs, nil
	}
	sources := make([]string, 0, len(spec.Datasets.Amounts))
	for n := range spec.Datasets.Amounts {
		sources = append(sources, n)
	}
	sort.Strings(sources)
	for _, n := range sources {
		file := spec.Datasets.Amounts[n]
		hdr, err := headers(file)
		if err != nil {
			return notes, err
		}
		if !hdr["amount"] {
			return notes, fail("source CSV %s needs amount column", file)
		}
		for _, dim := range spec.Dimensions {
			if !hdr[dim] {
				return notes, fail("source CSV %s needs dimension %s", file, dim)
			}
		}
		notes = append(notes, fmt.Sprintf("AMOUNT %s OK (%s)", n, file))
	}
	for _, s := range spec.Stages {
		if !Enabled(s.Enabled) || s.Operation == "MERGE" || s.Operation == "PASS" {
			continue
		}
		for _, a := range s.Allocations {
			if !Enabled(a.Enabled) {
				continue
			}
			file := spec.Datasets.Drivers[a.DriverDataset]
			hdr, err := headers(file)
			if err != nil {
				return notes, err
			}
			if !hdr[a.WeightField] {
				return notes, fail("driver %s needs weight column %s", file, a.WeightField)
			}
			matches := false
			for _, dim := range spec.Dimensions {
				if hdr[dim] {
					matches = true
				}
			}
			if !matches {
				return notes, fail("driver %s has no matching dimension columns", file)
			}
			for dim, t := range s.Target {
				if t.Mode == "INCLUDE" || t.Mode == "INCLUDE_EXCLUDE" {
					if !hdr[dim] {
						return notes, fail("driver %s must include target dimension %s for rule %s", file, dim, s.ID)
					}
				}
			}
			notes = append(notes, fmt.Sprintf("DRIVER stage=%s factor=%s OK (%s, weight=%s)", s.ID, a.ID, file, a.WeightField))
		}
	}
	notes = append(notes, "NOTE: CSV headers checked. Valid driver denominator and source row values require a real run.")
	return notes, nil
}
