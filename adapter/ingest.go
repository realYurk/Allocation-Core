// Package ingest provides a source-agnostic, read-only streaming interface for
// sparse allocation inputs. Nothing in the financial engine knows SQL/Cube.
package adapter

import (
	"allocationcore/internal/engine"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Rows is consumed once in order. Every row contains source columns in the
// order reported by Columns. An implementation must never write to its source.
type Rows interface {
	Columns() []string
	Next() ([]string, error) // io.EOF at end
	Close() error
}
type Source interface {
	Open(context.Context) (Rows, error)
}

type Mapping struct {
	Dimensions  []string          `json:"dimensions"`
	Columns     map[string]string `json:"columns"` // canonical dimension -> source column
	ValueSource string            `json:"value_source"`
	ValueName   string            `json:"value_name"` // amount or exact driver column name
	Precision   int               `json:"precision"`  // amount 0..6; driver usually 6
}
type Report struct {
	Status     string   `json:"status"`
	Rows       int64    `json:"rows"`
	Fields     []string `json:"fields"`
	Output     string   `json:"output"`
	SourceKind string   `json:"source_kind,omitempty"`
	SHA256     string   `json:"output_sha256"`
	Bytes      int64    `json:"output_bytes"`
	Note       string   `json:"note"`
}

// ImportCSV streams all rows through strict mapping and decimal validation,
// commits only after EOF and successful close. No implicit aggregation,
// dimension inference, zero filtering or member substitution takes place.
func ImportCSV(ctx context.Context, src Source, m Mapping, dst string) (report Report, err error) {
	if ctx == nil {
		return report, fmt.Errorf("nil context")
	}
	if src == nil {
		return report, fmt.Errorf("nil source")
	}
	if m.Precision < 0 || m.Precision > 6 {
		return report, fmt.Errorf("precision must be 0..6")
	}
	if m.ValueName == "" || m.ValueSource == "" || len(m.Dimensions) == 0 {
		return report, fmt.Errorf("dimensions, value_source and value_name are required")
	}
	used := map[string]bool{m.ValueName: true}
	for _, d := range m.Dimensions {
		if d == "" || used[d] || m.Columns[d] == "" {
			return report, fmt.Errorf("invalid/duplicate dimension mapping %q", d)
		}
		used[d] = true
	}
	if _, e := os.Stat(dst); e == nil {
		return report, fmt.Errorf("output already exists: %s", dst)
	} else if !os.IsNotExist(e) {
		return report, e
	}
	if e := os.MkdirAll(filepath.Dir(dst), 0750); e != nil {
		return report, e
	}
	rows, e := src.Open(ctx)
	if e != nil {
		return report, e
	}
	defer rows.Close()
	columns := rows.Columns()
	pos := map[string]int{}
	for i, c := range columns {
		if _, ok := pos[c]; ok {
			return report, fmt.Errorf("duplicate source column %s", c)
		}
		pos[c] = i
	}
	dimsPos := make([]int, len(m.Dimensions))
	for i, d := range m.Dimensions {
		n, ok := pos[m.Columns[d]]
		if !ok {
			return report, fmt.Errorf("missing source column %q for dimension %q", m.Columns[d], d)
		}
		dimsPos[i] = n
	}
	valuePos, ok := pos[m.ValueSource]
	if !ok {
		return report, fmt.Errorf("missing value source column %q", m.ValueSource)
	}
	tmp, e := os.CreateTemp(filepath.Dir(dst), ".allocation-import-*.tmp")
	if e != nil {
		return report, e
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	checksum := sha256.New()
	w := csv.NewWriter(io.MultiWriter(tmp, checksum))
	fields := append(append([]string(nil), m.Dimensions...), m.ValueName)
	if e = w.Write(fields); e != nil {
		tmp.Close()
		return report, e
	}
	var count int64
	for {
		if e := ctx.Err(); e != nil {
			tmp.Close()
			return report, e
		}
		r, e := rows.Next()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			tmp.Close()
			return report, fmt.Errorf("source row %d: %w", count+1, e)
		}
		if len(r) != len(columns) {
			tmp.Close()
			return report, fmt.Errorf("source row %d: expected %d columns, got %d", count+1, len(columns), len(r))
		}
		out := make([]string, 0, len(fields))
		for i, pos := range dimsPos {
			v := r[pos]
			if strings.TrimSpace(v) == "" {
				tmp.Close()
				return report, fmt.Errorf("source row %d missing dimension %s", count+1, m.Dimensions[i])
			}
			out = append(out, v)
		}
		n, e := engine.ParseScaled(r[valuePos], m.Precision)
		if e != nil {
			tmp.Close()
			return report, fmt.Errorf("source row %d invalid %s: %w", count+1, m.ValueSource, e)
		}
		out = append(out, engine.Money(n, m.Precision))
		if e = w.Write(out); e != nil {
			tmp.Close()
			return report, e
		}
		count++
	}
	w.Flush()
	if e := w.Error(); e != nil {
		tmp.Close()
		return report, e
	}
	if e := tmp.Sync(); e != nil {
		tmp.Close()
		return report, e
	}
	info, e := tmp.Stat()
	if e != nil {
		tmp.Close()
		return report, e
	}
	if e := tmp.Close(); e != nil {
		return report, e
	}
	if e := rows.Close(); e != nil {
		return report, fmt.Errorf("closing source snapshot failed: %w", e)
	}
	if e := os.Rename(tmpPath, dst); e != nil {
		return report, e
	}
	return Report{Status: "PASS", Rows: count, Fields: fields, Output: dst, SHA256: hex.EncodeToString(checksum.Sum(nil)), Bytes: info.Size(), Note: "Validated source data only; financial allocation and reconciliation not performed"}, nil
}

// CSVConfig is a portable, no-credentials config. Other implementations may
// inject SQL or Cube Source without altering Mapping or financial engine.
type CSVConfig struct {
	Source struct {
		Type string `json:"type"`
		Path string `json:"path"`
	} `json:"source"`
	Mapping Mapping `json:"mapping"`
}

func ImportCSVConfig(ctx context.Context, configPath, destination string) (Report, error) {
	var cfg CSVConfig
	raw, e := os.ReadFile(configPath)
	if e != nil {
		return Report{}, e
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&cfg); e != nil {
		return Report{}, e
	}
	var extra any
	if e = dec.Decode(&extra); !errors.Is(e, io.EOF) {
		return Report{}, fmt.Errorf("configuration contains trailing content: %v", e)
	}
	if cfg.Source.Type != "csv" || cfg.Source.Path == "" {
		return Report{}, fmt.Errorf("this CLI supports CSV sources only; inject an SQL/Cube Source through the Go integration API")
	}
	path := cfg.Source.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(configPath), path)
	}
	r, e := ImportCSV(ctx, CSVSource{Path: path}, cfg.Mapping, destination)
	r.SourceKind = "csv"
	return r, e
}
