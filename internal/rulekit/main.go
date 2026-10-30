// Rule Kit / 通用规则转换器: no company-specific policy or identifier.
// Generates allocation-ir/2.0 from a generic metadata JSON and a stage table.
package rulekit

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Meta struct {
	Schema      string         `json:"schema"`
	Name        string         `json:"name"`
	Precision   int            `json:"precision"`
	Dimensions  []string       `json:"dimensions"`
	Immutable   []string       `json:"immutable_dimensions"`
	Hierarchies map[string]any `json:"hierarchies"`
	Datasets    struct {
		Amounts map[string]string `json:"amounts"`
		Drivers map[string]string `json:"drivers"`
	} `json:"datasets"`
}
type allocation struct {
	ID, Share, DriverDataset, WeightField, NegativeDriverPolicy string
	DriverFilters                                               json.RawMessage
}

func strict(b []byte, v any) error {
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var trailing any
	if e := d.Decode(&trailing); e != io.EOF {
		return errors.New("extra JSON after root")
	}
	return nil
}
func parseJSON(s string, out any) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("required JSON cell missing")
	}
	return json.Unmarshal([]byte(s), out)
}
func rational(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok || r.Sign() < 0 || r.Cmp(big.NewRat(1, 1)) > 0 {
		return nil, fmt.Errorf("share must be decimal fraction 0..1: %q", s)
	}
	return r, nil
}
func names(ds []string) error {
	if len(ds) == 0 || len(ds) > 40 {
		return errors.New("dimensions 1..40 required")
	}
	seen := map[string]bool{}
	for _, d := range ds {
		if strings.TrimSpace(d) == "" || seen[d] || d == "value" {
			return fmt.Errorf("duplicate/invalid dimension %q", d)
		}
		seen[d] = true
	}
	return nil
}
func cleanFile(s string) bool {
	return s != "" && s == filepath.Base(s) && s != "." && s != ".." && !strings.ContainsAny(s, "/\\:") && !strings.HasPrefix(s, ".")
}
func Convert(metaRaw, csvRaw []byte) ([]byte, error) {
	var m Meta
	if err := strict(metaRaw, &m); err != nil {
		return nil, fmt.Errorf("metadata: %w", err)
	}
	if m.Schema != "generic-allocation-table/1" {
		return nil, errors.New("schema must be generic-allocation-table/1")
	}
	if err := names(m.Dimensions); err != nil {
		return nil, err
	}
	if m.Precision < 0 || m.Precision > 6 {
		return nil, errors.New("precision must be 0..6")
	}
	dim := map[string]bool{}
	for _, d := range m.Dimensions {
		dim[d] = true
	}
	for _, d := range m.Immutable {
		if !dim[d] {
			return nil, fmt.Errorf("unknown immutable dimension %q", d)
		}
	}
	if len(m.Datasets.Amounts) == 0 {
		return nil, errors.New("at least one amount dataset required")
	}
	for _, set := range []map[string]string{m.Datasets.Amounts, m.Datasets.Drivers} {
		for k, v := range set {
			if strings.TrimSpace(k) == "" || !cleanFile(v) {
				return nil, fmt.Errorf("unsafe dataset path %s: %s", k, v)
			}
		}
	}
	reader := csv.NewReader(strings.NewReader(string(csvRaw)))
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, errors.New("stages CSV has no rows")
	}
	ix := map[string]int{}
	for i, n := range rows[0] {
		n = strings.TrimPrefix(strings.TrimSpace(n), "\ufeff")
		if _, ok := ix[n]; ok {
			return nil, fmt.Errorf("duplicate header %s", n)
		}
		ix[n] = i
	}
	// Lossless stage-json mode handles all Core operations (ALLOCATE/PASS/MERGE).
	if j, ok := ix["stage_json"]; ok {
		stages := []any{}
		seen := map[string]bool{}
		for line, rec := range rows[1:] {
			if j >= len(rec) {
				return nil, fmt.Errorf("row %d: stage_json missing", line+2)
			}
			var stage map[string]any
			if e := json.Unmarshal([]byte(rec[j]), &stage); e != nil {
				return nil, fmt.Errorf("row %d: %w", line+2, e)
			}
			id, ok := stage["id"].(string)
			if !ok || id == "" || seen[id] {
				return nil, fmt.Errorf("row %d: missing/duplicate stage id", line+2)
			}
			seen[id] = true
			stages = append(stages, stage)
		}
		if len(stages) == 0 || len(stages) > 100 {
			return nil, errors.New("stage count must be 1..100")
		}
		return marshalRule(m, stages)
	}
	needed := []string{"stage_id", "inputs_json", "grouping_json", "filters_json", "target_json", "conservation_keys_json", "allocation_id", "share", "driver_dataset", "weight_field", "negative_driver_policy", "driver_filters_json"}
	for _, n := range needed {
		if _, ok := ix[n]; !ok {
			return nil, fmt.Errorf("missing CSV column %s", n)
		}
	}
	get := func(rec []string, n string) string {
		j := ix[n]
		if j >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[j])
	}
	type stageBuild struct {
		def         map[string]any
		signature   string
		total       *big.Rat
		allocations []any
		ids         map[string]bool
	}
	order := []string{}
	stages := map[string]*stageBuild{}
	for i, rec := range rows[1:] {
		line := i + 2
		id := get(rec, "stage_id")
		aid := get(rec, "allocation_id")
		if id == "" || aid == "" {
			return nil, fmt.Errorf("row %d: missing stage/allocation", line)
		}
		var inputs []map[string]any
		var grouping map[string]string
		var filters map[string]any
		var target map[string]map[string]any
		var keys []string
		var driverFilters map[string]any
		for _, field := range []struct {
			s string
			v any
		}{{get(rec, "inputs_json"), &inputs}, {get(rec, "grouping_json"), &grouping}, {get(rec, "filters_json"), &filters}, {get(rec, "target_json"), &target}, {get(rec, "conservation_keys_json"), &keys}, {get(rec, "driver_filters_json"), &driverFilters}} {
			if err := parseJSON(field.s, field.v); err != nil {
				return nil, fmt.Errorf("row %d invalid JSON: %w", line, err)
			}
		}
		if len(inputs) == 0 || len(keys) == 0 {
			return nil, fmt.Errorf("row %d: inputs/conservation_keys required", line)
		}
		for d := range grouping {
			if !dim[d] {
				return nil, fmt.Errorf("row %d: unknown grouping %s", line, d)
			}
		}
		for d := range target {
			if !dim[d] {
				return nil, fmt.Errorf("row %d: unknown target %s", line, d)
			}
		}
		for _, d := range m.Dimensions {
			if grouping[d] == "" || target[d] == nil {
				return nil, fmt.Errorf("row %d: stage %s missing dimension %s", line, id, d)
			}
		}
		for _, d := range keys {
			if !dim[d] {
				return nil, fmt.Errorf("row %d: unknown conservation key %s", line, d)
			}
		}
		if _, ok := m.Datasets.Drivers[get(rec, "driver_dataset")]; !ok {
			return nil, fmt.Errorf("row %d: unknown driver %s", line, get(rec, "driver_dataset"))
		}
		share, err := rational(get(rec, "share"))
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", line, err)
		}
		structure := map[string]any{"id": id, "inputs": inputs, "source": map[string]any{"grouping": grouping, "filters": filters}, "target": target, "conservation_keys": keys}
		encoded, _ := json.Marshal(structure)
		sb := stages[id]
		if sb == nil {
			sb = &stageBuild{def: structure, signature: string(encoded), total: new(big.Rat), ids: map[string]bool{}}
			stages[id] = sb
			order = append(order, id)
		} else if sb.signature != string(encoded) {
			return nil, fmt.Errorf("row %d: stage %s has inconsistent source/target/inputs", line, id)
		}
		if sb.ids[aid] {
			return nil, fmt.Errorf("row %d: duplicate allocation %s", line, aid)
		}
		sb.ids[aid] = true
		sb.total.Add(sb.total, share)
		a := map[string]any{"id": aid, "share": get(rec, "share"), "driver_dataset": get(rec, "driver_dataset"), "weight_field": get(rec, "weight_field"), "negative_driver_policy": get(rec, "negative_driver_policy"), "driver_filters": driverFilters}
		if get(rec, "weight_field") == "" {
			return nil, fmt.Errorf("row %d: weight_field missing", line)
		}
		sb.allocations = append(sb.allocations, a)
	}
	if len(order) > 100 {
		return nil, errors.New("core supports at most 100 stages per rule; use Multi for many independent rules")
	}
	outs := []any{}
	for _, id := range order {
		sb := stages[id]
		if sb.total.Cmp(big.NewRat(1, 1)) != 0 {
			return nil, fmt.Errorf("stage %s shares total %s; expected 1", id, sb.total.RatString())
		}
		sb.def["allocations"] = sb.allocations
		outs = append(outs, sb.def)
	}
	return marshalRule(m, outs)
}

func marshalRule(m Meta, stages []any) ([]byte, error) {
	obj := map[string]any{"schema": "allocation-ir/2.0", "name": m.Name, "precision": m.Precision, "dimensions": m.Dimensions, "immutable_dimensions": m.Immutable, "hierarchies": m.Hierarchies, "datasets": m.Datasets, "stages": stages}
	if obj["hierarchies"] == nil {
		obj["hierarchies"] = map[string]any{}
	}
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Extract reverses an existing generic Core rule into a table + metadata.
// It supports external adapters and round-trip without embedding private rules in source.
func Extract(raw []byte) ([]byte, []byte, error) {
	var original struct {
		Schema      string         `json:"schema"`
		Name        string         `json:"name"`
		Precision   int            `json:"precision"`
		Dimensions  []string       `json:"dimensions"`
		Immutable   []string       `json:"immutable_dimensions"`
		Hierarchies map[string]any `json:"hierarchies"`
		Datasets    struct {
			Amounts map[string]string `json:"amounts"`
			Drivers map[string]string `json:"drivers"`
		} `json:"datasets"`
		Stages []struct {
			ID     string          `json:"id"`
			Inputs json.RawMessage `json:"inputs"`
			Source struct {
				Grouping json.RawMessage `json:"grouping"`
				Filters  json.RawMessage `json:"filters"`
			} `json:"source"`
			Target           json.RawMessage `json:"target"`
			ConservationKeys json.RawMessage `json:"conservation_keys"`
			Allocations      []struct {
				ID                   string          `json:"id"`
				Share                string          `json:"share"`
				DriverDataset        string          `json:"driver_dataset"`
				WeightField          string          `json:"weight_field"`
				NegativeDriverPolicy string          `json:"negative_driver_policy"`
				DriverFilters        json.RawMessage `json:"driver_filters"`
			} `json:"allocations"`
		} `json:"stages"`
	}
	if e := json.Unmarshal(raw, &original); e != nil {
		return nil, nil, e
	}
	if original.Schema != "allocation-ir/2.0" {
		return nil, nil, errors.New("Core rule must be allocation-ir/2.0")
	}
	meta := Meta{Schema: "generic-allocation-table/1", Name: original.Name, Precision: original.Precision, Dimensions: original.Dimensions, Immutable: original.Immutable, Hierarchies: original.Hierarchies}
	meta.Datasets.Amounts = original.Datasets.Amounts
	meta.Datasets.Drivers = original.Datasets.Drivers
	m, _ := json.MarshalIndent(meta, "", "  ")
	var rawStages struct {
		Stages []json.RawMessage `json:"stages"`
	}
	if e := json.Unmarshal(raw, &rawStages); e != nil {
		return nil, nil, e
	}
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	w.Write([]string{"stage_json"})
	for _, stage := range rawStages.Stages {
		var v map[string]any
		if e := json.Unmarshal(stage, &v); e != nil {
			return nil, nil, e
		}
		canonical, e := json.Marshal(v)
		if e != nil {
			return nil, nil, e
		}
		w.Write([]string{string(canonical)})
	}
	w.Flush()
	if e := w.Error(); e != nil {
		return nil, nil, e
	}
	return append(m, '\n'), []byte(buf.String()), nil
}
func CLI() {
	meta := flag.String("meta", "", "generic metadata JSON")
	fromRule := flag.String("extract", "", "export existing generic Core rule to table")
	dir := flag.String("dir", "", "output folder for extracted source")
	core := flag.String("core", "", "Core binary for mandatory semantic validation")
	stages := flag.String("stages", "", "generic stage allocation CSV")
	out := flag.String("out", "", "output Core rule.json")
	flag.Parse()
	if *fromRule != "" {
		if *dir == "" {
			fatal(errors.New("--extract requires --dir"))
		}
		b, e := os.ReadFile(*fromRule)
		if e != nil {
			fatal(e)
		}
		m, c, e := Extract(b)
		if e != nil {
			fatal(e)
		}
		if e = os.MkdirAll(*dir, 0700); e != nil {
			fatal(e)
		}
		if e = os.WriteFile(filepath.Join(*dir, "rule_meta.json"), m, 0600); e != nil {
			fatal(e)
		}
		if e = os.WriteFile(filepath.Join(*dir, "stages.csv"), c, 0600); e != nil {
			fatal(e)
		}
		fmt.Println("Extracted generic table / 已提取通用规则表:", *dir)
		return
	}
	if *meta == "" || *stages == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "Usage / 用法: rule_kit --meta rule_meta.json --stages stages.csv --out rule.json")
		os.Exit(2)
	}
	m, e := os.ReadFile(*meta)
	if e != nil {
		fatal(e)
	}
	s, e := os.ReadFile(*stages)
	if e != nil {
		fatal(e)
	}
	b, e := Convert(m, s)
	if e != nil {
		fatal(e)
	}
	if e = os.MkdirAll(filepath.Dir(*out), 0700); e != nil {
		fatal(e)
	}
	tmp := *out + ".part"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		fatal(e)
	}
	if *core != "" {
		output, e := exec.Command(*core, "validate", tmp).CombinedOutput()
		if e != nil {
			os.Remove(tmp)
			fatal(fmt.Errorf("Core semantic validation failed / Core 语义验证失败: %v: %s", e, output))
		}
	}
	if e = os.Rename(tmp, *out); e != nil {
		os.Remove(tmp)
		fatal(e)
	}
	var result map[string]any
	_ = json.Unmarshal(b, &result)
	fmt.Printf("VALID / 验证通过: %d stage(s), output: %s\n", len(result["stages"].([]any)), *out)
}
func fatal(e error) { fmt.Fprintln(os.Stderr, "ERROR / 错误:", e); os.Exit(1) }
