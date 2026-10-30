package engine

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// HeaderPositions rejects ambiguous CSV schemas instead of silently
// keeping the last duplicate column name (which can change money semantics).
func HeaderPositions(headers []string) (map[string]int, error) {
	pos := make(map[string]int, len(headers))
	for i, raw := range headers {
		name := strings.TrimPrefix(raw, "\ufeff")
		if name == "" {
			return nil, fail("empty CSV column at position %d", i+1)
		}
		if _, duplicate := pos[name]; duplicate {
			return nil, fail("duplicate CSV column %q", name)
		}
		pos[name] = i
	}
	return pos, nil
}

func ReadAmounts(file string, dims []string, precision int) ([]Row, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	headers, err := cr.Read()
	if err != nil {
		return nil, err
	}
	pos, err := HeaderPositions(headers)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	for _, d := range dims {
		if _, ok := pos[d]; !ok {
			return nil, fail("%s missing dimension %s", file, d)
		}
	}
	if _, ok := pos["amount"]; !ok {
		return nil, fail("%s missing amount", file)
	}
	rows := []Row{}
	line := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s:%d %w", file, line+1, err)
		}
		line++
		c := map[string]string{}
		for _, d := range dims {
			if pos[d] >= len(rec) {
				return nil, fail("%s:%d short row", file, line)
			}
			v := rec[pos[d]]
			if v == "" || v == poolMember {
				return nil, fail("%s:%d invalid member %s:%s", file, line, d, v)
			}
			c[d] = v
		}
		if pos["amount"] >= len(rec) {
			return nil, fail("%s:%d missing amount", file, line)
		}
		a, err := ParseScaled(rec[pos["amount"]], precision)
		if err != nil {
			return nil, fail("%s:%d %v", file, line, err)
		}
		id := strconv.Itoa(line - 1)
		if k, ok := pos["row_id"]; ok && k < len(rec) && rec[k] != "" {
			id = rec[k]
		}
		rows = append(rows, Row{Coord: c, Amount: a, IDs: []string{id}})
	}
	return rows, nil
}
func ReadDrivers(file string, dims []string, weightField string) ([]DriverRow, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	headers, err := cr.Read()
	if err != nil {
		return nil, err
	}
	pos, err := HeaderPositions(headers)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if _, ok := pos[weightField]; !ok {
		return nil, fail("%s missing weight column %s", file, weightField)
	}
	dimsFound := 0
	for _, d := range dims {
		if _, ok := pos[d]; ok {
			dimsFound++
		}
	}
	if dimsFound == 0 {
		return nil, fail("%s has no matching dimensions", file)
	}
	rows := []DriverRow{}
	line := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		line++
		c := map[string]string{}
		for _, d := range dims {
			if ix, ok := pos[d]; ok {
				if ix >= len(rec) || rec[ix] == "" {
					return nil, fail("%s:%d missing coordinate %s", file, line, d)
				}
				c[d] = rec[ix]
			}
		}
		ix := pos[weightField]
		if ix >= len(rec) {
			return nil, fail("%s:%d missing weight", file, line)
		}
		n, err := ParseScaled(rec[ix], 6)
		if err != nil {
			return nil, fail("%s:%d invalid weight: %v", file, line, err)
		}
		rows = append(rows, DriverRow{Coord: c, Weights: map[string]int64{weightField: n}})
	}
	return rows, nil
}
func Resolve(base, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return filepath.Clean(relative), nil
	}
	p := filepath.Join(base, filepath.FromSlash(relative))
	return filepath.Clean(p), nil
}

// StreamAmounts is a no-whole-file-loading version of ReadAmounts. It uses the
// same dimension validation and produces the same row identifiers.
func StreamAmounts(file string, dims []string, precision int, emit func(Row) error) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	headers, err := cr.Read()
	if err != nil {
		return err
	}
	pos, err := HeaderPositions(headers)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	for _, d := range dims {
		if _, ok := pos[d]; !ok {
			return fail("%s missing dimension %s", file, d)
		}
	}
	if _, ok := pos["amount"]; !ok {
		return fail("%s missing amount", file)
	}
	line := 1
	for {
		rec, e := cr.Read()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return fmt.Errorf("%s:%d %w", file, line+1, e)
		}
		line++
		c := make(map[string]string, len(dims))
		for _, d := range dims {
			idx := pos[d]
			if idx >= len(rec) || rec[idx] == "" || rec[idx] == poolMember {
				return fail("%s:%d invalid member %s", file, line, d)
			}
			c[d] = rec[idx]
		}
		idx := pos["amount"]
		if idx >= len(rec) {
			return fail("%s:%d missing amount", file, line)
		}
		amt, e := ParseScaled(rec[idx], precision)
		if e != nil {
			return fail("%s:%d %v", file, line, e)
		}
		id := strconv.Itoa(line - 1)
		if idx, ok := pos["row_id"]; ok && idx < len(rec) && rec[idx] != "" {
			id = rec[idx]
		}
		if e = emit(Row{Coord: c, Amount: amt, IDs: []string{id}}); e != nil {
			return e
		}
	}
}
