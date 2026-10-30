package adapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MySQL credentials are resolved from environment variables at runtime.
// Configuration and audit reports never contain a password or a raw DSN.
type MySQLConnection struct {
	Address               string `json:"address"`
	Database              string `json:"database"`
	UserEnv               string `json:"user_env"`
	PasswordEnv           string `json:"password_env"`
	TLSCAFile             string `json:"tls_ca_file"`
	TLSServerName         string `json:"tls_server_name"`
	ConnectTimeoutSeconds int    `json:"connect_timeout_seconds"`
	ReadTimeoutSeconds    int    `json:"read_timeout_seconds"`
	TotalTimeoutSeconds   int    `json:"total_timeout_seconds"`
}
type MySQLQuery struct {
	Table     string            `json:"table"`
	Columns   []string          `json:"columns"`
	Equal     map[string]string `json:"equal,omitempty"`
	KeyColumn string            `json:"key_column"`
	BatchSize int               `json:"batch_size"`
}
type MySQLFileConfig struct {
	Source struct {
		Type string `json:"type"`
		MySQLConnection
		MySQLQuery
	} `json:"source"`
	Mapping Mapping `json:"mapping"`
}
type MySQLBundleConfig struct {
	Source struct {
		Type string `json:"type"`
		MySQLConnection
	} `json:"source"`
	Exports []struct {
		Name string `json:"name"`
		MySQLQuery
		Mapping Mapping `json:"mapping"`
	} `json:"exports"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func readStrictConfig(path string, into any) error {
	info, e := os.Stat(path)
	if e != nil {
		return e
	}
	if info.Size() > 1024*1024 {
		return fmt.Errorf("configuration exceeds 1 MiB")
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1024*1024+1))
	dec.DisallowUnknownFields()
	if e = dec.Decode(into); e != nil {
		return e
	}
	var extra any
	if e = dec.Decode(&extra); !errors.Is(e, io.EOF) {
		return fmt.Errorf("config trailing content or >1 MiB")
	}
	return nil
}
func (c MySQLConnection) validate() error {
	if c.Address == "" || c.Database == "" || c.UserEnv == "" || c.PasswordEnv == "" || c.TLSCAFile == "" || c.TLSServerName == "" {
		return fmt.Errorf("address, database, environment credential names and verified TLS are mandatory")
	}
	if !envName.MatchString(c.UserEnv) || !envName.MatchString(c.PasswordEnv) {
		return fmt.Errorf("invalid credential environment variable name")
	}
	if _, e := sqlIdent(c.Database); e != nil || strings.Contains(c.Database, ".") {
		return fmt.Errorf("invalid database identifier")
	}
	if c.ConnectTimeoutSeconds < 0 || c.ConnectTimeoutSeconds > 120 || c.ReadTimeoutSeconds < 0 || c.ReadTimeoutSeconds > 3600 || c.TotalTimeoutSeconds < 0 || c.TotalTimeoutSeconds > 86400 {
		return fmt.Errorf("invalid timeout bounds")
	}
	return nil
}
func (q MySQLQuery) sqlSource(db *sql.DB, tx *sql.Tx) SQLSource {
	return SQLSource{DB: db, Tx: tx, Table: q.Table, ColumnsList: q.Columns, Equal: q.Equal, KeyColumn: q.KeyColumn, BatchSize: q.BatchSize}
}
func (q MySQLQuery) validate() error {
	if q.BatchSize < 1 || q.BatchSize > 100000 || q.KeyColumn == "" {
		return fmt.Errorf("MySQL imports require batch_size 1..100000 and unique signed BIGINT key_column")
	}
	_, _, e := q.sqlSource(nil, nil).Statement()
	if e != nil {
		return e
	}
	found := false
	for _, v := range q.Columns {
		if v == q.KeyColumn {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("key_column must appear in selected columns")
	}
	_, e = sqlIdent(q.KeyColumn)
	return e
}
func readMySQLFileConfig(path string) (MySQLFileConfig, error) {
	var cfg MySQLFileConfig
	if e := readStrictConfig(path, &cfg); e != nil {
		return cfg, e
	}
	if cfg.Source.Type != "mysql" {
		return cfg, fmt.Errorf("source.type must be mysql")
	}
	if e := cfg.Source.MySQLConnection.validate(); e != nil {
		return cfg, e
	}
	if e := cfg.Source.MySQLQuery.validate(); e != nil {
		return cfg, e
	}
	if !filepath.IsAbs(cfg.Source.TLSCAFile) {
		cfg.Source.TLSCAFile = filepath.Join(filepath.Dir(path), cfg.Source.TLSCAFile)
	}
	return cfg, nil
}
func readMySQLBundleConfig(path string) (MySQLBundleConfig, error) {
	var cfg MySQLBundleConfig
	if e := readStrictConfig(path, &cfg); e != nil {
		return cfg, e
	}
	if cfg.Source.Type != "mysql" {
		return cfg, fmt.Errorf("source.type must be mysql")
	}
	if e := cfg.Source.MySQLConnection.validate(); e != nil {
		return cfg, e
	}
	if len(cfg.Exports) == 0 || len(cfg.Exports) > 100 {
		return cfg, fmt.Errorf("exports count must be 1..100")
	}
	seen := make(map[string]bool)
	for _, v := range cfg.Exports {
		if !regexp.MustCompile(`^[A-Za-z0-9_-]+\.csv$`).MatchString(v.Name) || seen[strings.ToLower(v.Name)] {
			return cfg, fmt.Errorf("invalid or duplicate output file name %q", v.Name)
		}
		seen[strings.ToLower(v.Name)] = true
		if e := v.MySQLQuery.validate(); e != nil {
			return cfg, e
		}
	}
	if !filepath.IsAbs(cfg.Source.TLSCAFile) {
		cfg.Source.TLSCAFile = filepath.Join(filepath.Dir(path), cfg.Source.TLSCAFile)
	}
	return cfg, nil
}

// ImportSQLBundle reads all tables through ONE REPEATABLE READ read-only tx,
// releases the transaction, and publishes the entire output directory once.
// This is the source contract that a private Cube snapshot provider can also
// implement. No database credentials or source SQL is included in the report.
type BundleExport struct {
	Name    string
	Query   MySQLQuery
	Mapping Mapping
}
type BundleReport struct {
	Status   string   `json:"status"`
	Snapshot string   `json:"snapshot"`
	Files    []Report `json:"files"`
	Output   string   `json:"output"`
}

func ImportSQLBundle(ctx context.Context, db *sql.DB, jobs []BundleExport, destinationDir string) (r BundleReport, err error) {
	if ctx == nil || db == nil || len(jobs) == 0 {
		return r, fmt.Errorf("context, database and exports required")
	}
	if _, e := os.Lstat(destinationDir); e == nil {
		return r, fmt.Errorf("destination already exists")
	} else if !os.IsNotExist(e) {
		return r, e
	}
	parent := filepath.Dir(destinationDir)
	if e := os.MkdirAll(parent, 0700); e != nil {
		return r, e
	}
	stage, e := os.MkdirTemp(parent, ".allocation-snapshot-*")
	if e != nil {
		return r, e
	}
	defer os.RemoveAll(stage)
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return r, fmt.Errorf("snapshot rejected: %w", e)
	}
	released := false
	defer func() {
		if !released {
			_ = tx.Rollback()
		}
	}()
	seen := map[string]bool{}
	for _, job := range jobs {
		if !regexp.MustCompile(`^[A-Za-z0-9_-]+\.csv$`).MatchString(job.Name) || seen[strings.ToLower(job.Name)] {
			return r, fmt.Errorf("invalid/duplicate snapshot file name: %q", job.Name)
		}
		seen[strings.ToLower(job.Name)] = true
		if e := job.Query.validate(); e != nil {
			return r, e
		}
		out, e := ImportCSV(ctx, job.Query.sqlSource(db, tx), job.Mapping, filepath.Join(stage, job.Name))
		if e != nil {
			return r, fmt.Errorf("export %s: %w", job.Name, e)
		}
		out.Output = job.Name
		out.SourceKind = "mysql"
		r.Files = append(r.Files, out)
	}
	if e := tx.Rollback(); e != nil {
		return r, fmt.Errorf("releasing snapshot failed: %w", e)
	}
	released = true
	r.Status = "PASS"
	r.Output = destinationDir
	r.Snapshot = "single REPEATABLE READ read-only transaction, one database connection"
	manifest, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return r, e
	}
	if e := os.WriteFile(filepath.Join(stage, "manifest.json"), manifest, 0600); e != nil {
		return r, e
	}
	if e := os.Rename(stage, destinationDir); e != nil {
		return r, e
	}
	return r, nil
}
