// Package acceptance performs exact, coordinate-level comparison between
// published allocation results and an external reference. It never allocates
// amounts or changes a financial calculation. Both inputs are read-only.
package acceptance

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"allocationcore/internal/engine"
)

const shards = 32

type Options struct {
	Precision int
	Tolerance string
}
type Account struct {
	Account   string `json:"account"`
	Matched   int64  `json:"matched"`
	Different int64  `json:"different"`
	Missing   int64  `json:"missing"`
	Extra     int64  `json:"extra"`
}
type Difference struct {
	Kind       string `json:"kind"`
	Account    string `json:"account"`
	Coordinate string `json:"coordinate"`
	Reference  string `json:"reference_amount"`
	Actual     string `json:"actual_amount"`
	Delta      string `json:"difference"`
}
type Report struct {
	Status          string       `json:"status"`
	Scope           string       `json:"scope"`
	Dimensions      []string     `json:"dimensions"`
	Precision       int          `json:"precision"`
	Tolerance       string       `json:"tolerance"`
	ActualRows      int64        `json:"actual_rows"`
	ReferenceRows   int64        `json:"reference_rows"`
	Matched         int64        `json:"matched"`
	Different       int64        `json:"different"`
	Missing         int64        `json:"missing"`
	Extra           int64        `json:"extra"`
	ActualTotal     string       `json:"actual_total"`
	ReferenceTotal  string       `json:"reference_total"`
	ActualSHA256    string       `json:"actual_sha256"`
	ReferenceSHA256 string       `json:"reference_sha256"`
	ElapsedMS       int64        `json:"elapsed_ms"`
	Accounts        []Account    `json:"accounts"`
	TopDifferences  []Difference `json:"top_differences"`
}

type fileInfo struct {
	value  int
	coords map[string]int
	hash   string
	rows   int64
	total  *big.Int
}

func validateHeaders(path string) (fileInfo, []string, error) {
	var o fileInfo
	f, err := os.Open(path)
	if err != nil {
		return o, nil, err
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReaderSize(f, 256<<10))
	r.FieldsPerRecord = -1
	head, err := r.Read()
	if err != nil {
		return o, nil, err
	}
	o.coords = make(map[string]int)
	o.value = -1
	countValues := 0
	seen := make(map[string]bool)
	dims := make([]string, 0, len(head))
	for i, k := range head {
		k = strings.TrimPrefix(k, "\ufeff")
		if k == "" || seen[k] {
			return o, nil, fmt.Errorf("%s duplicate or empty header %q", filepath.Base(path), k)
		}
		seen[k] = true
		if k == "amount" || k == "value" {
			o.value = i
			countValues++
			continue
		}
		if k == "terminal_stage" || k == "row_id" {
			continue
		} // engine-generated provenance, not business coordinates
		dims = append(dims, k)
		o.coords[k] = i
	}
	if countValues != 1 || len(dims) == 0 || len(dims) > 64 {
		return o, nil, fmt.Errorf("%s must have exactly one amount/value column and 1..64 coordinate dimensions", filepath.Base(path))
	}
	return o, dims, nil
}
func scan(ctx context.Context, input string, side string, dimension []string, opt Options, files []*os.File, writers []*csv.Writer) (fileInfo, error) {
	info, dims, err := validateHeaders(input)
	if err != nil {
		return info, err
	}
	if len(dims) != len(dimension) {
		return info, fmt.Errorf("%s coordinate dimensions differ: got %v expected %v", filepath.Base(input), dims, dimension)
	}
	for _, d := range dimension {
		if _, ok := info.coords[d]; !ok {
			return info, fmt.Errorf("%s missing coordinate dimension %q", filepath.Base(input), d)
		}
	}
	f, err := os.Open(input)
	if err != nil {
		return info, err
	}
	defer f.Close()
	hash := sha256.New()
	rd := csv.NewReader(bufio.NewReaderSize(io.TeeReader(f, hash), 256<<10))
	rd.FieldsPerRecord = -1
	header, err := rd.Read()
	if err != nil {
		return info, err
	}
	info.total = new(big.Int)
	_ = header
	n := int64(1)
	for {
		if err = ctx.Err(); err != nil {
			return info, err
		}
		row, e := rd.Read()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return info, fmt.Errorf("%s row %d: %w", filepath.Base(input), n+1, e)
		}
		n++
		if len(row) != len(header) {
			return info, fmt.Errorf("%s row %d: field count mismatch", filepath.Base(input), n)
		}
		amount, e := engine.ParseScaled(strings.TrimSpace(row[info.value]), opt.Precision)
		if e != nil {
			return info, fmt.Errorf("%s row %d invalid amount: %w", filepath.Base(input), n, e)
		}
		coords := make([]string, len(dimension))
		size := 0
		for i, d := range dimension {
			value := row[info.coords[d]]
			if strings.TrimSpace(value) == "" {
				return info, fmt.Errorf("%s row %d empty coordinate %q", filepath.Base(input), n, d)
			}
			size += len(value)
			if size > 8192 {
				return info, fmt.Errorf("%s row %d coordinate too large", filepath.Base(input), n)
			}
			coords[i] = value
		}
		keyBytes, _ := json.Marshal(coords)
		key := string(keyBytes)
		h := fnv.New64a()
		_, _ = h.Write(keyBytes)
		partition := int(h.Sum64() % shards)
		if e = writers[partition].Write([]string{side, key, fmt.Sprint(amount)}); e != nil {
			return info, e
		}
		info.total.Add(info.total, big.NewInt(amount))
		info.rows++
	}
	// Hash is derived from the raw bytes that actually reached the CSV parser.
	info.hash = hex.EncodeToString(hash.Sum(nil))
	_ = files
	return info, nil
}
func amountString(v *big.Int, precision int) string {
	neg := v.Sign() < 0
	a := new(big.Int).Abs(v)
	if precision == 0 {
		if neg {
			return "-" + a.String()
		}
		return a.String()
	}
	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
	whole, frac := new(big.Int).QuoRem(a, divisor, new(big.Int))
	s := whole.String() + "." + fmt.Sprintf("%0*s", precision, frac.String())
	if neg {
		return "-" + s
	}
	return s
}
func pushTop(top []Difference, d Difference, precision int) []Difference {
	top = append(top, d)
	sort.SliceStable(top, func(i, j int) bool {
		parse := func(s string) *big.Int {
			neg := strings.HasPrefix(s, "-")
			s = strings.TrimPrefix(s, "-")
			s = strings.Replace(s, ".", "", 1)
			v, ok := new(big.Int).SetString(s, 10)
			if !ok {
				return new(big.Int)
			}
			_ = neg
			return v
		}
		a, b := parse(top[i].Delta), parse(top[j].Delta)
		if c := a.Cmp(b); c != 0 {
			return c > 0
		}
		return top[i].Coordinate < top[j].Coordinate
	})
	if len(top) > 25 {
		top = top[:25]
	}
	return top
}

// Compare streams two files to 32 private temporary partitions. Each partition
// aggregates complete coordinate keys exactly using big.Int. Peak coordinate
// memory scales with the largest partition, not with the full input table.
// It publishes a new report directory only after a successful comparison.
func Compare(ctx context.Context, actual, reference, out string, opt Options) (Report, error) {
	start := time.Now()
	r := Report{Status: "BLOCK", Scope: "EXACT_COORDINATES_ONLY; total conservation is not sufficient; missing and extra coordinates always fail", Precision: opt.Precision, Tolerance: opt.Tolerance, Accounts: []Account{}, TopDifferences: []Difference{}}
	if opt.Precision < 0 || opt.Precision > 6 {
		return r, fmt.Errorf("precision must be 0..6")
	}
	if opt.Tolerance == "" {
		opt.Tolerance = "0"
	}
	tolerance, e := engine.ParseScaled(opt.Tolerance, opt.Precision)
	if e != nil || tolerance < 0 {
		return r, fmt.Errorf("tolerance must be a nonnegative decimal with <=%d fractional digits", opt.Precision)
	}
	r.Tolerance = opt.Tolerance
	ai, dimensions, e := validateHeaders(actual)
	if e != nil {
		return r, e
	}
	_, _, e = validateHeaders(reference)
	if e != nil {
		return r, e
	}
	_ = ai
	sort.Strings(dimensions)
	r.Dimensions = dimensions
	if _, e = os.Lstat(out); e == nil {
		return r, fmt.Errorf("report directory already exists: %s", out)
	} else if !os.IsNotExist(e) {
		return r, e
	}
	parent := filepath.Dir(out)
	if e = os.MkdirAll(parent, 0700); e != nil {
		return r, e
	}
	scratch, e := os.MkdirTemp(parent, ".reconcile-")
	if e != nil {
		return r, e
	}
	defer os.RemoveAll(scratch)
	var files []*os.File
	var writers []*csv.Writer
	for i := 0; i < shards; i++ {
		f, er := os.Create(filepath.Join(scratch, fmt.Sprintf("part-%02d.csv", i)))
		if er != nil {
			for _, x := range files {
				x.Close()
			}
			return r, er
		}
		files = append(files, f)
		writers = append(writers, csv.NewWriter(f))
	}
	// CSV writers buffer their own rows; avoid double wrapping (the small write
	// chunks are buffered by the OS). The underlying file remains private.
	actualInfo, e := scan(ctx, actual, "A", dimensions, opt, files, writers)
	if e == nil {
		var refInfo fileInfo
		refInfo, e = scan(ctx, reference, "R", dimensions, opt, files, writers)
		if e == nil {
			r.ReferenceRows = refInfo.rows
			r.ReferenceTotal = amountString(refInfo.total, opt.Precision)
			r.ReferenceSHA256 = refInfo.hash
		}
	}
	r.ActualRows = actualInfo.rows
	if actualInfo.total != nil {
		r.ActualTotal = amountString(actualInfo.total, opt.Precision)
	}
	r.ActualSHA256 = actualInfo.hash
	for i, w := range writers {
		w.Flush()
		if er := w.Error(); e == nil && er != nil {
			e = er
		}
		if er := files[i].Close(); e == nil && er != nil {
			e = er
		}
	}
	if e != nil {
		return r, e
	}
	resultFile, e := os.Create(filepath.Join(scratch, "differences.csv"))
	if e != nil {
		return r, e
	}
	dw := csv.NewWriter(resultFile)
	_ = dw.Write([]string{"kind", "account", "coordinate", "reference_amount", "actual_amount", "difference"})
	accounts := map[string]*Account{}
	accIndex := -1
	for i, k := range dimensions {
		if k == "account" {
			accIndex = i
		}
	}
	toleranceBig := big.NewInt(tolerance)
	for part := 0; part < shards; part++ {
		if e = ctx.Err(); e != nil {
			resultFile.Close()
			return r, e
		}
		f, er := os.Open(filepath.Join(scratch, fmt.Sprintf("part-%02d.csv", part)))
		if er != nil {
			resultFile.Close()
			return r, er
		}
		rd := csv.NewReader(bufio.NewReader(f))
		type sums struct {
			actual, reference *big.Int
			a, r              bool
		}
		data := map[string]*sums{}
		for {
			fields, er := rd.Read()
			if errors.Is(er, io.EOF) {
				break
			}
			if er != nil {
				f.Close()
				resultFile.Close()
				return r, er
			}
			if len(fields) != 3 {
				f.Close()
				resultFile.Close()
				return r, errors.New("corrupt partition")
			}
			v, ok := new(big.Int).SetString(fields[2], 10)
			if !ok {
				f.Close()
				resultFile.Close()
				return r, errors.New("invalid partition amount")
			}
			s := data[fields[1]]
			if s == nil {
				s = &sums{actual: new(big.Int), reference: new(big.Int)}
				data[fields[1]] = s
			}
			if fields[0] == "A" {
				s.actual.Add(s.actual, v)
				s.a = true
			} else if fields[0] == "R" {
				s.reference.Add(s.reference, v)
				s.r = true
			} else {
				f.Close()
				resultFile.Close()
				return r, errors.New("invalid partition side")
			}
		}
		f.Close()
		keys := make([]string, 0, len(data))
		for k := range data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s := data[k]
			var coords []string
			if er = json.Unmarshal([]byte(k), &coords); er != nil {
				resultFile.Close()
				return r, er
			}
			account := "ALL"
			if accIndex >= 0 {
				account = coords[accIndex]
			}
			v := accounts[account]
			if v == nil {
				v = &Account{Account: account}
				accounts[account] = v
			}
			delta := new(big.Int).Sub(s.actual, s.reference)
			kind := ""
			switch {
			case !s.a:
				kind = "MISSING"
				r.Missing++
				v.Missing++
			case !s.r:
				kind = "EXTRA"
				r.Extra++
				v.Extra++
			case new(big.Int).Abs(delta).Cmp(toleranceBig) > 0:
				kind = "DIFFERENT"
				r.Different++
				v.Different++
			default:
				r.Matched++
				v.Matched++
			}
			if kind == "" {
				continue
			}
			coordinate := make([]string, len(coords))
			for i, c := range coords {
				coordinate[i] = dimensions[i] + "=" + c
			}
			d := Difference{Kind: kind, Account: account, Coordinate: strings.Join(coordinate, " | "), Reference: amountString(s.reference, opt.Precision), Actual: amountString(s.actual, opt.Precision), Delta: amountString(delta, opt.Precision)}
			if er = dw.Write([]string{d.Kind, d.Account, d.Coordinate, d.Reference, d.Actual, d.Delta}); er != nil {
				resultFile.Close()
				return r, er
			}
			r.TopDifferences = pushTop(r.TopDifferences, d, opt.Precision)
		}
	}
	dw.Flush()
	e = dw.Error()
	if er := resultFile.Close(); e == nil {
		e = er
	}
	if e != nil {
		return r, e
	}
	for _, v := range accounts {
		r.Accounts = append(r.Accounts, *v)
	}
	sort.Slice(r.Accounts, func(i, j int) bool { return r.Accounts[i].Account < r.Accounts[j].Account })
	r.Status = "MATCH"
	if r.Missing+r.Extra+r.Different > 0 {
		r.Status = "DIFFERENCES"
	}
	r.ElapsedMS = time.Since(start).Milliseconds()
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return r, e
	}
	if e = os.WriteFile(filepath.Join(scratch, "summary.json"), b, 0600); e != nil {
		return r, e
	}
	// Partition files are private scratch only, never part of the published report.
	for i := 0; i < shards; i++ {
		_ = os.Remove(filepath.Join(scratch, fmt.Sprintf("part-%02d.csv", i)))
	}
	if e = os.Rename(scratch, out); e != nil {
		return r, e
	}
	return r, nil
}
