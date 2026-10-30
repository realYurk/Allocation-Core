// Cube Explorer is a local, generic multidimensional CSV/Bedrock viewer.
// No allocation algorithms, business-specific rules, cube names or account identifiers live here.
package explorer

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed static/*
var assets embed.FS

type ImportSpec struct {
	Format           string   `json:"format"`
	Delimiter        string   `json:"delimiter,omitempty"`
	HeaderRows       int      `json:"header_rows,omitempty"`
	CubeColumn       bool     `json:"cube_column,omitempty"`
	Dimensions       []string `json:"dimensions,omitempty"`
	ValueColumn      string   `json:"value_column,omitempty"`
	IgnoreDimensions []string `json:"ignore_dimensions,omitempty"`
	FilterDimension  string   `json:"filter_dimension,omitempty"`
	FilterValue      string   `json:"filter_value,omitempty"`
}
type ImportResult struct {
	Rows       int64    `json:"rows"`
	Skipped    int64    `json:"skipped"`
	SHA256     string   `json:"sha256"`
	Dimensions []string `json:"dimensions"`
}
type responseError struct {
	Error string `json:"error"`
}

func parseNumber(text string) (*big.Rat, error) {
	v := strings.TrimSpace(text)
	if v == "" {
		return nil, errors.New("blank numeric cell")
	}
	r := new(big.Rat)
	if _, ok := r.SetString(v); !ok {
		return nil, fmt.Errorf("invalid decimal %q (export with dot decimal separator)", v)
	}
	return r, nil
}
func csvRune(s string) (rune, error) {
	if s == "" {
		return ',', nil
	}
	a := []rune(s)
	if len(a) != 1 {
		return 0, errors.New("delimiter must be one Unicode character")
	}
	return a[0], nil
}
func namesValid(ds []string) error {
	if len(ds) == 0 || len(ds) > 40 {
		return fmt.Errorf("expected between 1 and 40 dimensions; got %d", len(ds))
	}
	m := map[string]bool{}
	for _, d := range ds {
		if strings.TrimSpace(d) == "" || d == "value" || m[d] {
			return fmt.Errorf("invalid/duplicate dimension name %q", d)
		}
		m[d] = true
	}
	return nil
}
func configOK(s ImportSpec) error {
	if s.Format != "bedrock" && s.Format != "header" {
		return fmt.Errorf("format must be bedrock or header")
	}
	if s.HeaderRows < 0 || s.HeaderRows > 100 {
		return errors.New("header_rows out of range 0..100")
	}
	if _, err := csvRune(s.Delimiter); err != nil {
		return err
	}
	if s.Format == "bedrock" {
		if err := namesValid(s.Dimensions); err != nil {
			return err
		}
	}
	ignore := map[string]bool{}
	for _, d := range s.IgnoreDimensions {
		if d == "" || ignore[d] {
			return fmt.Errorf("invalid repeated ignored dimension %q", d)
		}
		ignore[d] = true
	}
	return nil
}
func orderedOutputDimensions(all []string, spec ImportSpec) ([]string, error) {
	known := map[string]bool{}
	for _, d := range all {
		known[d] = true
	}
	for _, d := range spec.IgnoreDimensions {
		if !known[d] {
			return nil, fmt.Errorf("ignored dimension %q is not an input column", d)
		}
	}
	if spec.FilterDimension != "" && !known[spec.FilterDimension] {
		return nil, fmt.Errorf("filtered dimension %q is not an input column", spec.FilterDimension)
	}
	drop := map[string]bool{}
	for _, d := range spec.IgnoreDimensions {
		drop[d] = true
	}
	out := []string{}
	for _, d := range all {
		if !drop[d] {
			out = append(out, d)
		}
	}
	if err := namesValid(out); err != nil {
		return nil, err
	}
	return out, nil
}

// Normalize converts a raw cube export to a sparse, headered CSV.
// Dimension values, including placeholders describing the original posting grain, are never synthesized or discarded.
// If a dimension is explicitly ignored, its values are grouped later during pivot/engine processing, not multiplied out.
func Normalize(input, output string, spec ImportSpec) (result ImportResult, err error) {
	if err = configOK(spec); err != nil {
		return result, err
	}
	f, err := os.Open(input)
	if err != nil {
		return result, err
	}
	defer f.Close()
	h := sha256.New()
	rd := csv.NewReader(bufio.NewReader(io.TeeReader(f, h)))
	rd.FieldsPerRecord = -1
	rd.ReuseRecord = false
	rd.Comma, _ = csvRune(spec.Delimiter)
	var cols []string
	if spec.Format == "header" {
		for i := 0; i < spec.HeaderRows; i++ {
			if _, err = rd.Read(); err != nil {
				return result, fmt.Errorf("skip header row %d: %w", i+1, err)
			}
		}
		cols, err = rd.Read()
		if err != nil {
			return result, err
		}
		if len(cols) > 0 {
			cols[0] = strings.TrimPrefix(cols[0], "\ufeff")
		}
		cols = append([]string{}, cols...)
		names := map[string]bool{}
		for _, v := range cols {
			if v == "" || names[v] {
				return result, fmt.Errorf("bad/duplicate input header %q", v)
			}
			names[v] = true
		}
	} else {
		for i := 0; i < spec.HeaderRows; i++ {
			if _, err = rd.Read(); err != nil {
				return result, fmt.Errorf("skip export title row %d: %w", i+1, err)
			}
		}
		if spec.CubeColumn {
			cols = append(cols, "__cube__")
		}
		cols = append(cols, spec.Dimensions...)
		cols = append(cols, "__value__")
	}
	valueColumn := spec.ValueColumn
	if valueColumn == "" {
		valueColumn = "value"
	}
	if spec.Format == "bedrock" {
		valueColumn = "__value__"
	}
	pos := map[string]int{}
	for i, k := range cols {
		pos[k] = i
	}
	valIdx, ok := pos[valueColumn]
	if !ok {
		return result, fmt.Errorf("value column %q not found", valueColumn)
	}
	dims := []string{}
	for _, c := range cols {
		if c != valueColumn && c != "__cube__" {
			dims = append(dims, c)
		}
	}
	outDims, err := orderedOutputDimensions(dims, spec)
	if err != nil {
		return result, err
	}
	if err = os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		return result, err
	}
	tmp := output + ".part"
	o, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	defer func() {
		o.Close()
		if err != nil {
			os.Remove(tmp)
		}
	}()
	w := csv.NewWriter(o)
	if err = w.Write(append(append([]string{}, outDims...), "value")); err != nil {
		return result, err
	}
	var line int
	var oneCube string
	for {
		var rec []string
		rec, err = rd.Read()
		if errors.Is(err, io.EOF) {
			err = nil
			break
		}
		if err != nil {
			return result, fmt.Errorf("source row %d: %w", line+1, err)
		}
		line++
		if len(rec) != len(cols) {
			return result, fmt.Errorf("source row %d has %d fields, expected %d", line, len(rec), len(cols))
		}
		// Bedrock title-record mode 2 can emit a filter record; it is not a numeric data cell.
		if spec.Format == "bedrock" && len(rec) >= 2 && strings.EqualFold(strings.TrimSpace(rec[1]), "Filter") {
			result.Skipped++
			continue
		}
		if spec.Format == "bedrock" && spec.CubeColumn {
			if oneCube == "" {
				oneCube = rec[0]
			}
			if strings.TrimSpace(rec[0]) == "" || rec[0] != oneCube {
				return result, fmt.Errorf("source data row %d mixes cube identifiers; import each cube separately", line)
			}
		}
		if spec.FilterDimension != "" && rec[pos[spec.FilterDimension]] != spec.FilterValue {
			result.Skipped++
			continue
		}
		numeric, e := parseNumber(rec[valIdx])
		if e != nil {
			return result, fmt.Errorf("source data row %d: %w", line, e)
		}
		// Preserve exact decimal value as written, rather than floating-point conversion.
		_ = numeric
		out := make([]string, 0, len(outDims)+1)
		for _, d := range outDims {
			out = append(out, rec[pos[d]])
		}
		out = append(out, strings.TrimSpace(rec[valIdx]))
		if err = w.Write(out); err != nil {
			return result, err
		}
		result.Rows++
	}
	w.Flush()
	if err = w.Error(); err != nil {
		return result, err
	}
	if err = o.Sync(); err != nil {
		return result, err
	}
	if err = o.Close(); err != nil {
		return result, err
	}
	if err = os.Rename(tmp, output); err != nil {
		return result, err
	}
	result.Dimensions = outDims
	result.SHA256 = hex.EncodeToString(h.Sum(nil))
	return result, nil
}

type snapshot struct{ Before, After, Engine string }
type App struct {
	mu        sync.RWMutex
	path      string
	slots     snapshot
	csrf      string
	maxUpload int64
	indexes   indexStore
}

func newApp(path string) (*App, error) {
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}
	t := make([]byte, 24)
	if _, err := rand.Read(t); err != nil {
		return nil, err
	}
	a := &App{path: path, csrf: hex.EncodeToString(t), maxUpload: 500 << 20, indexes: indexStore{m: map[string]*sparseIndex{}}}
	for _, slot := range []string{"before", "after", "engine"} {
		p := filepath.Join(path, slot+".csv")
		if _, err := os.Stat(p); err == nil {
			switch slot {
			case "before":
				a.slots.Before = p
			case "after":
				a.slots.After = p
			case "engine":
				a.slots.Engine = p
			}
		}
	}
	return a, nil
}
func (a *App) get(slot string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	switch slot {
	case "before":
		return a.slots.Before
	case "after":
		return a.slots.After
	case "engine":
		return a.slots.Engine
	}
	return ""
}
func (a *App) set(slot, path string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch slot {
	case "before":
		a.slots.Before = path
	case "after":
		a.slots.After = path
	case "engine":
		a.slots.Engine = path
	}
}
func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
func sendError(w http.ResponseWriter, err error, code int) {
	w.WriteHeader(code)
	jsonOut(w, responseError{err.Error()})
}
func (a *App) upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		sendError(w, errors.New("POST only"), 405)
		return
	}
	if r.Header.Get("X-Local-Token") != a.csrf {
		sendError(w, errors.New("invalid local session token"), 403)
		return
	}
	slot := r.URL.Query().Get("slot")
	if slot != "before" && slot != "after" && slot != "engine" {
		sendError(w, errors.New("slot must be before, after or engine"), 400)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.maxUpload)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		sendError(w, err, 400)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		sendError(w, err, 400)
		return
	}
	defer file.Close()
	raw, err := os.CreateTemp(a.path, "incoming-*.tmp")
	if err != nil {
		sendError(w, err, 500)
		return
	}
	defer os.Remove(raw.Name())
	defer raw.Close()
	if _, err = io.Copy(raw, file); err != nil {
		sendError(w, err, 400)
		return
	}
	if err = raw.Close(); err != nil {
		sendError(w, err, 500)
		return
	}
	var spec ImportSpec
	if err = json.Unmarshal([]byte(r.FormValue("spec")), &spec); err != nil {
		sendError(w, fmt.Errorf("invalid import spec: %w", err), 400)
		return
	}
	// Compile to a unique immutable staging path. Only a completely validated file becomes visible.
	stage, err := os.CreateTemp(a.path, "validated-*.csv")
	if err != nil {
		sendError(w, err, 500)
		return
	}
	stage.Close()
	defer os.Remove(stage.Name())
	result, err := Normalize(raw.Name(), stage.Name(), spec)
	if err != nil {
		sendError(w, err, 422)
		return
	}
	final := filepath.Join(a.path, slot+".csv")
	if err = os.Rename(stage.Name(), final); err != nil {
		sendError(w, err, 500)
		return
	}
	a.set(slot, final)
	a.indexes.mu.Lock()
	delete(a.indexes.m, final)
	a.indexes.mu.Unlock()
	jsonOut(w, result)
}
func headerAndIdx(path string) ([]string, map[string]int, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, nil, e
	}
	defer f.Close()
	r := csv.NewReader(f)
	h, e := r.Read()
	if e != nil {
		return nil, nil, e
	}
	idx := map[string]int{}
	for i, c := range h {
		if _, found := idx[c]; found {
			return nil, nil, fmt.Errorf("duplicate header %q", c)
		}
		idx[c] = i
	}
	if _, ok := idx["value"]; !ok {
		return nil, nil, errors.New("normalized CSV must have value column")
	}
	return h, idx, nil
}
func dataDimensions(h []string) []string {
	out := []string{}
	for _, d := range h {
		if d != "value" {
			out = append(out, d)
		}
	}
	return out
}
func sameDimensions(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := map[string]bool{}
	for _, v := range a {
		x[v] = true
	}
	for _, v := range b {
		if !x[v] {
			return false
		}
	}
	return true
}
func (a *App) schemas() ([]string, map[string]string, error) {
	paths := map[string]string{"before": a.get("before"), "after": a.get("after"), "engine": a.get("engine")}
	var dims []string
	for _, s := range []string{"before", "after", "engine"} {
		if p := paths[s]; p != "" {
			h, _, e := headerAndIdx(p)
			if e != nil {
				return nil, nil, e
			}
			newDims := dataDimensions(h)
			if dims == nil {
				dims = newDims
			} else if !sameDimensions(dims, newDims) {
				return nil, nil, fmt.Errorf("%s has different dimensions: map input columns first", s)
			}
		}
	}
	return dims, paths, nil
}
func (a *App) metadata(w http.ResponseWriter, r *http.Request) {
	dims, paths, e := a.schemas()
	if e != nil {
		sendError(w, e, 422)
		return
	}
	jsonOut(w, map[string]any{"dimensions": dims, "loaded": map[string]bool{"before": paths["before"] != "", "after": paths["after"] != "", "engine": paths["engine"] != ""}, "token": a.csrf})
}

type totals struct {
	Before, After, Engine          *big.Rat
	HasBefore, HasAfter, HasEngine bool
}

func newTotals() *totals {
	return &totals{new(big.Rat), new(big.Rat), new(big.Rat), false, false, false}
}
func (t *totals) add(which string, r *big.Rat) {
	switch which {
	case "before":
		t.Before.Add(t.Before, r)
		t.HasBefore = true
	case "after":
		t.After.Add(t.After, r)
		t.HasAfter = true
	case "engine":
		t.Engine.Add(t.Engine, r)
		t.HasEngine = true
	}
}
func (t *totals) json() map[string]any {
	get := func(v *big.Rat, ok bool) any {
		if !ok {
			return nil
		}
		return v.FloatString(6)
	}
	o := map[string]any{"before": get(t.Before, t.HasBefore), "after": get(t.After, t.HasAfter), "engine": get(t.Engine, t.HasEngine)}
	if t.HasBefore && t.HasAfter {
		o["after_minus_before"] = new(big.Rat).Sub(t.After, t.Before).FloatString(6)
	}
	if t.HasAfter && t.HasEngine {
		o["engine_minus_after"] = new(big.Rat).Sub(t.Engine, t.After).FloatString(6)
	}
	return o
}
func scanRecords(path string, dims []string, pick func(map[string]string, *big.Rat) error) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	r := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	r.FieldsPerRecord = -1
	h, e := r.Read()
	if e != nil {
		return e
	}
	idx := map[string]int{}
	for i, c := range h {
		idx[c] = i
	}
	for _, c := range append(append([]string{}, dims...), "value") {
		if _, ok := idx[c]; !ok {
			return fmt.Errorf("missing normalized field %q", c)
		}
	}
	for line := 2; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("row %d: %w", line, err)
		}
		if len(rec) != len(h) {
			return fmt.Errorf("row %d: field length mismatch", line)
		}
		v, err := parseNumber(rec[idx["value"]])
		if err != nil {
			return fmt.Errorf("row %d: %w", line, err)
		}
		coord := map[string]string{}
		for _, d := range dims {
			coord[d] = rec[idx[d]]
		}
		if err := pick(coord, v); err != nil {
			return err
		}
	}
}

// Members returns bounded prefix suggestions for a dimension. Manual exact-value input remains supported.
func (a *App) members(w http.ResponseWriter, r *http.Request) {
	dims, paths, e := a.schemas()
	if e != nil {
		sendError(w, e, 422)
		return
	}
	d := r.URL.Query().Get("dimension")
	valid := false
	for _, x := range dims {
		if x == d {
			valid = true
			break
		}
	}
	if !valid {
		sendError(w, fmt.Errorf("unknown dimension %q", d), 400)
		return
	}
	prefix := strings.TrimSpace(r.URL.Query().Get("q"))
	found := map[string]bool{}
	partial := false
	for _, slot := range []string{"before", "after", "engine"} {
		path := paths[slot]
		if path == "" {
			continue
		}
		idx, err := a.cacheIndex(path, dims)
		if err != nil {
			sendError(w, err, 422)
			return
		}
		if idx.partial {
			partial = true
		}
		for _, chunk := range idx.chunks {
			values := chunk.members[d]
			if values == nil {
				partial = true
				continue
			}
			for value := range values {
				if strings.Contains(strings.ToLower(value), strings.ToLower(prefix)) {
					found[value] = true
				}
				if len(found) >= 150 {
					partial = true
					break
				}
			}
			if len(found) >= 150 {
				break
			}
		}
		if len(found) >= 150 {
			break
		}
	}
	values := make([]string, 0, len(found))
	for v := range found {
		values = append(values, v)
	}
	sort.Strings(values)
	jsonOut(w, map[string]any{"members": values, "partial": partial})
}
func (a *App) view(w http.ResponseWriter, r *http.Request) {
	begin := time.Now()
	dims, paths, e := a.schemas()
	if e != nil {
		sendError(w, e, 422)
		return
	}
	if len(dims) == 0 {
		sendError(w, errors.New("upload at least one CSV"), 422)
		return
	}
	row := r.URL.Query().Get("row")
	col := r.URL.Query().Get("column")
	all := map[string]bool{}
	for _, d := range dims {
		all[d] = true
	}
	if !all[row] || !all[col] || row == col {
		sendError(w, errors.New("choose two distinct dimensions for row and column"), 422)
		return
	}
	filters := map[string]string{}
	if text := r.URL.Query().Get("filters"); text != "" {
		if e := json.Unmarshal([]byte(text), &filters); e != nil {
			sendError(w, e, 400)
			return
		}
	}
	for d := range filters {
		if !all[d] {
			sendError(w, fmt.Errorf("unknown filter dimension %q", d), 400)
			return
		}
	}
	cells := map[string]*totals{}
	rows := map[string]bool{}
	cols := map[string]bool{}
	grand := newTotals()
	count := map[string]int64{}
	skippedChunks := map[string]int64{}
	for _, slot := range []string{"before", "after", "engine"} {
		path := paths[slot]
		if path == "" {
			continue
		}
		scanned, skipped, scanErr := a.scanFiltered(path, dims, filters, func(coord map[string]string, v *big.Rat) error {
			key := coord[row] + "\x00" + coord[col]
			t := cells[key]
			if t == nil {
				t = newTotals()
				cells[key] = t
			}
			t.add(slot, v)
			rows[coord[row]] = true
			cols[coord[col]] = true
			grand.add(slot, v)
			if len(cells) > 100000 {
				return errors.New("view exceeds 100,000 distinct cells; add filters or use a smaller slice")
			}
			return nil
		})
		count[slot] = scanned
		skippedChunks[slot] = skipped
		e = scanErr
		if e != nil {
			sendError(w, e, 422)
			return
		}
	}
	rs := make([]string, 0, len(rows))
	cs := make([]string, 0, len(cols))
	for x := range rows {
		rs = append(rs, x)
	}
	for x := range cols {
		cs = append(cs, x)
	}
	sort.Strings(rs)
	sort.Strings(cs)
	if len(rs) > 300 || len(cs) > 80 {
		sendError(w, fmt.Errorf("pivot has %d rows × %d columns; add exact-match filters (limit 300 × 80)", len(rs), len(cs)), 422)
		return
	}
	type mismatch struct {
		Row        string   `json:"row"`
		Column     string   `json:"column"`
		Reference  string   `json:"reference"`
		Engine     string   `json:"engine"`
		Difference string   `json:"difference"`
		Abs        *big.Rat `json:"-"`
	}
	diffs := []mismatch{}
	var differenceCells int
	var unmatchedCells int
	absDifference := new(big.Rat)
	if paths["after"] != "" && paths["engine"] != "" {
		for key, t := range cells {
			if t.HasAfter != t.HasEngine || (t.HasAfter && t.After.Cmp(t.Engine) != 0) {
				differenceCells++
				if t.HasAfter != t.HasEngine {
					unmatchedCells++
				}
				d := new(big.Rat).Sub(t.Engine, t.After)
				abs := new(big.Rat).Abs(d)
				absDifference.Add(absDifference, abs)
				loc := strings.SplitN(key, "\x00", 2)
				ref, eng := "未找到 / Missing", "未找到 / Missing"
				if t.HasAfter {
					ref = t.After.FloatString(6)
				}
				if t.HasEngine {
					eng = t.Engine.FloatString(6)
				}
				if len(loc) == 2 {
					diffs = append(diffs, mismatch{Row: loc[0], Column: loc[1], Reference: ref, Engine: eng, Difference: d.FloatString(6), Abs: abs})
				}
			}
		}
	}
	sort.Slice(diffs, func(i, j int) bool {
		c := diffs[i].Abs.Cmp(diffs[j].Abs)
		if c != 0 {
			return c > 0
		}
		if diffs[i].Row != diffs[j].Row {
			return diffs[i].Row < diffs[j].Row
		}
		return diffs[i].Column < diffs[j].Column
	})
	if len(diffs) > 25 {
		diffs = diffs[:25]
	}
	verdict := "NOT_VERIFIED"
	if paths["after"] != "" && paths["engine"] != "" {
		if differenceCells == 0 {
			verdict = "MATCH_AT_PIVOT_GRAIN"
		} else {
			verdict = "DIFFERENCES"
		}
	}
	type record struct {
		Name  string `json:"name"`
		Cells []any  `json:"cells"`
		Total any    `json:"total"`
	}
	records := []record{}
	for _, rn := range rs {
		rec := record{Name: rn, Cells: []any{}}
		total := newTotals()
		for _, cn := range cs {
			t := cells[rn+"\x00"+cn]
			if t != nil {
				rec.Cells = append(rec.Cells, t.json())
				if t.HasBefore {
					total.add("before", t.Before)
				}
				if t.HasAfter {
					total.add("after", t.After)
				}
				if t.HasEngine {
					total.add("engine", t.Engine)
				}
			} else {
				rec.Cells = append(rec.Cells, nil)
			}
		}
		rec.Total = total.json()
		records = append(records, rec)
	}
	jsonOut(w, map[string]any{"row_axis": row, "column_axis": col, "columns": cs, "rows": records, "grand_total": grand.json(), "read_rows": count, "skipped_index_rows": skippedChunks, "elapsed_seconds": time.Since(begin).Seconds(), "matched_cells": len(cells), "difference_cells": differenceCells, "missing_cells": unmatchedCells, "absolute_difference": absDifference.FloatString(6), "engine_available": paths["engine"] != "" && paths["after"] != "", "verdict": verdict, "top_differences": diffs})
}
func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, e := assets.ReadFile("static/index.html")
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		t, e := template.New("index").Parse(string(b))
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		t.Execute(w, map[string]string{"Token": a.csrf})
	})
	mux.HandleFunc("/api/upload", a.upload)
	mux.HandleFunc("/api/meta", a.metadata)
	mux.HandleFunc("/api/members", a.members)
	mux.HandleFunc("/api/view", a.view)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; base-uri 'none'")
		if r.Method == "POST" && r.Header.Get("Origin") != "" {
			origin := r.Header.Get("Origin")
			if origin != "http://"+r.Host && origin != "https://"+r.Host {
				sendError(w, errors.New("cross-origin submission denied"), 403)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func CLI() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: cube_explorer serve [--dir local-data] [--port 8877] | normalize --input export.csv --output normalized.csv --config mapping.json")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "normalize":
		fs := flag.NewFlagSet("normalize", flag.ExitOnError)
		in := fs.String("input", "", "source file")
		out := fs.String("output", "", "normalized CSV")
		conf := fs.String("config", "", "mapping JSON")
		fs.Parse(os.Args[2:])
		if *in == "" || *out == "" || *conf == "" {
			fs.Usage()
			os.Exit(2)
		}
		b, e := os.ReadFile(*conf)
		if e != nil {
			panic(e)
		}
		var spec ImportSpec
		if e = json.Unmarshal(b, &spec); e != nil {
			panic(e)
		}
		r, e := Normalize(*in, *out, spec)
		if e != nil {
			panic(e)
		}
		j, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(j))
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		dir := fs.String("dir", "local-data", "private working directory")
		port := fs.Int("port", 8877, "loopback port")
		fs.Parse(os.Args[2:])
		if *port < 1024 || *port > 65535 {
			panic("invalid port")
		}
		a, e := newApp(*dir)
		if e != nil {
			panic(e)
		}
		addr := "127.0.0.1:" + strconv.Itoa(*port)
		l, e := net.Listen("tcp", addr)
		if e != nil {
			panic(e)
		}
		fmt.Println("Open http://" + addr + "/ in your browser. Private files stay in " + *dir)
		srv := &http.Server{Handler: a.handler(), ReadHeaderTimeout: 10 * time.Second}
		if e = srv.Serve(l); e != nil && e != http.ErrServerClosed {
			panic(e)
		}
	default:
		fmt.Fprintln(os.Stderr, "Unknown subcommand", os.Args[1])
		os.Exit(2)
	}
}

// Handler provides the same protected local Cube Explorer endpoints in-process.
func Handler(dir string) (http.Handler, error) {
	a, err := newApp(dir)
	if err != nil {
		return nil, err
	}
	return a.handler(), nil
}
