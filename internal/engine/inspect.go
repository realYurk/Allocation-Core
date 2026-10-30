package engine

// InspectInputs is a generic, read-only, streaming data quality gate.
// It does not claim to prove runtime driver coverage, allocation conservation
// or equivalence to any external accounting system.

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type InputCheck struct {
	Dataset        string   `json:"dataset"`
	Kind           string   `json:"kind"`
	File           string   `json:"file"`
	Rows           int64    `json:"rows"`
	Bytes          int64    `json:"bytes"`
	Sum            string   `json:"sum,omitempty"`
	ZeroValues     int64    `json:"zero_values,omitempty"`
	NegativeValues int64    `json:"negative_values,omitempty"`
	WeightFields   []string `json:"weight_fields,omitempty"`
	Error          string   `json:"error,omitempty"`
}
type InputReport struct {
	Status     string       `json:"status"`
	RuleSHA256 string       `json:"rule_sha256,omitempty"`
	Dimensions int          `json:"dimensions"`
	Stages     int          `json:"stages"`
	Checks     []InputCheck `json:"checks"`
	ElapsedMS  int64        `json:"elapsed_ms"`
	Scope      string       `json:"scope"`
}

// InspectInputs reads every row once per input dataset using bounded memory.
// The caller must parse and Compile(spec) first. Driver files reused by several
// stages are scanned once, validating all referenced weight columns together.
func InspectInputs(rulePath string, spec Spec) (report InputReport) {
	start := time.Now()
	report = InputReport{Status: "PASS", Dimensions: len(spec.Dimensions), Stages: len(spec.Stages),
		Scope: "Input schema and numeric values only; runtime driver eligibility, pool conservation, and reference equivalence are NOT checked."}
	defer func() { report.ElapsedMS = time.Since(start).Milliseconds() }()
	data, err := os.ReadFile(rulePath)
	if err != nil {
		report.Status = "BLOCK"
		report.Checks = append(report.Checks, InputCheck{Kind: "RULE", Error: err.Error()})
		return
	}
	hash := sha256.Sum256(data)
	report.RuleSHA256 = hex.EncodeToString(hash[:])
	base := filepath.Dir(rulePath)
	amounts := make([]string, 0, len(spec.Datasets.Amounts))
	for n := range spec.Datasets.Amounts {
		amounts = append(amounts, n)
	}
	sort.Strings(amounts)
	for _, n := range amounts {
		c := inspectFile(base, n, "AMOUNT", spec.Datasets.Amounts[n], spec.Dimensions, nil, spec.Precision)
		if c.Error != "" {
			report.Status = "BLOCK"
		}
		report.Checks = append(report.Checks, c)
	}
	driverFields := map[string]map[string]bool{}
	for _, st := range spec.Stages {
		if !Enabled(st.Enabled) || st.Operation == "MERGE" || st.Operation == "PASS" {
			continue
		}
		for _, a := range st.Allocations {
			if !Enabled(a.Enabled) {
				continue
			}
			if driverFields[a.DriverDataset] == nil {
				driverFields[a.DriverDataset] = map[string]bool{}
			}
			driverFields[a.DriverDataset][a.WeightField] = true
		}
	}
	drivers := make([]string, 0, len(driverFields))
	for n := range driverFields {
		drivers = append(drivers, n)
	}
	sort.Strings(drivers)
	for _, n := range drivers {
		fields := make([]string, 0, len(driverFields[n]))
		for field := range driverFields[n] {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		c := inspectFile(base, n, "DRIVER", spec.Datasets.Drivers[n], spec.Dimensions, fields, 6)
		if c.Error != "" {
			report.Status = "BLOCK"
		}
		report.Checks = append(report.Checks, c)
	}
	return
}

func inspectFile(base, name, kind, filename string, dims, fields []string, precision int) InputCheck {
	result := InputCheck{Dataset: name, Kind: kind, File: filename, WeightFields: fields}
	path, err := Resolve(base, filename)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	f, err := os.Open(path)
	if err != nil {
		result.Error = fmt.Sprintf("cannot open input: %v", err)
		return result
	}
	defer f.Close()
	info, err := f.Stat()
	if err == nil {
		result.Bytes = info.Size()
	}
	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	hdr, err := reader.Read()
	if err != nil {
		result.Error = fmt.Sprintf("invalid CSV header: %v", err)
		return result
	}
	pos, err := HeaderPositions(hdr)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	required := fields
	if kind == "AMOUNT" {
		required = []string{"amount"}
	}
	for _, col := range required {
		if _, ok := pos[col]; !ok {
			result.Error = fmt.Sprintf("missing required column %q", col)
			return result
		}
	}
	if kind == "AMOUNT" {
		for _, dim := range dims {
			if _, ok := pos[dim]; !ok {
				result.Error = fmt.Sprintf("missing dimension %q", dim)
				return result
			}
		}
	} else {
		matches := 0
		for _, dim := range dims {
			if _, ok := pos[dim]; ok {
				matches++
			}
		}
		if matches == 0 {
			result.Error = "driver has no matching dimension columns"
			return result
		}
	}
	dimIndex := make([]int, 0, len(dims))
	for _, d := range dims {
		if j, ok := pos[d]; ok {
			dimIndex = append(dimIndex, j)
		}
	}
	valueIdx := make([]int, 0, len(required))
	for _, field := range required {
		valueIdx = append(valueIdx, pos[field])
	}
	sum := new(big.Int)
	for {
		row, er := reader.Read()
		if er == io.EOF {
			break
		}
		if er != nil {
			result.Error = fmt.Sprintf("row %d: %v", result.Rows+2, er)
			return result
		}
		result.Rows++
		if len(row) != len(hdr) {
			result.Error = fmt.Sprintf("row %d: expected %d columns, got %d", result.Rows+1, len(hdr), len(row))
			return result
		}
		for _, i := range dimIndex {
			if strings.TrimSpace(row[i]) == "" || row[i] == poolMember {
				result.Error = fmt.Sprintf("row %d: empty/reserved coordinate in column %q", result.Rows+1, hdr[i])
				return result
			}
		}
		for _, i := range valueIdx {
			v, er := ParseScaled(row[i], precision)
			if er != nil {
				result.Error = fmt.Sprintf("row %d column %q: %v", result.Rows+1, hdr[i], er)
				return result
			}
			if v == 0 {
				result.ZeroValues++
			}
			if v < 0 {
				result.NegativeValues++
			}
			if kind == "AMOUNT" {
				sum.Add(sum, big.NewInt(v))
			}
		}
	}
	if result.Rows == 0 {
		result.Error = "input has no data rows"
		return result
	}
	if kind == "AMOUNT" {
		scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
		whole, frac := new(big.Int).QuoRem(new(big.Int).Abs(sum), scale, new(big.Int))
		if precision == 0 {
			result.Sum = whole.String()
		} else {
			result.Sum = fmt.Sprintf("%s%s.%0*s", map[bool]string{true: "-", false: ""}[sum.Sign() < 0], whole.String(), precision, frac.String())
		}
	}
	return result
}
