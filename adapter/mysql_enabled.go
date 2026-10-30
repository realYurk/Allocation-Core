//go:build mysql

package adapter

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

var mysqlTLSRegistry sync.Mutex
var mysqlTLSSeen = map[string]bool{}

func openMySQL(ctx context.Context, c MySQLConnection) (*sql.DB, context.Context, context.CancelFunc, error) {
	if e := c.validate(); e != nil {
		return nil, nil, nil, e
	}
	user, uok := os.LookupEnv(c.UserEnv)
	pass, pok := os.LookupEnv(c.PasswordEnv)
	if !uok || !pok || user == "" {
		return nil, nil, nil, fmt.Errorf("database credential environment variables are missing")
	}
	ca, e := os.ReadFile(c.TLSCAFile)
	if e != nil {
		return nil, nil, nil, fmt.Errorf("could not read TLS certificate authority: %w", e)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, nil, nil, fmt.Errorf("no valid PEM root CAs")
	}
	h := sha256.Sum256(append(append([]byte(c.TLSServerName+"\x00"), ca...), 0))
	tlsName := "allocation-verified-" + hex.EncodeToString(h[:])
	mysqlTLSRegistry.Lock()
	if !mysqlTLSSeen[tlsName] {
		e = mysql.RegisterTLSConfig(tlsName, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: c.TLSServerName})
		if e == nil {
			mysqlTLSSeen[tlsName] = true
		}
	}
	mysqlTLSRegistry.Unlock()
	if e != nil {
		return nil, nil, nil, fmt.Errorf("TLS registration failed: %w", e)
	}
	host, port, e := net.SplitHostPort(c.Address)
	if e != nil || host == "" || port == "" {
		return nil, nil, nil, fmt.Errorf("invalid MySQL address: expected host:port")
	}
	if strings.ContainsAny(c.Database, "\n\r") {
		return nil, nil, nil, fmt.Errorf("invalid database")
	}
	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = pass
	cfg.Net = "tcp"
	cfg.Addr = c.Address
	cfg.DBName = c.Database
	cfg.TLSConfig = tlsName
	cfg.ParseTime = false
	cfg.InterpolateParams = false
	cfg.Timeout = time.Duration(defaultInt(c.ConnectTimeoutSeconds, 10)) * time.Second
	cfg.ReadTimeout = time.Duration(defaultInt(c.ReadTimeoutSeconds, 1800)) * time.Second
	cfg.WriteTimeout = 30 * time.Second
	// No password is ever returned in an error or written to a trace.
	db, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		return nil, nil, nil, fmt.Errorf("cannot initialize MySQL driver")
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	jobCtx, cancel := context.WithTimeout(ctx, time.Duration(defaultInt(c.TotalTimeoutSeconds, 3600))*time.Second)
	if e = db.PingContext(jobCtx); e != nil {
		cancel()
		db.Close()
		return nil, nil, nil, fmt.Errorf("MySQL connection or TLS verification failed: %T", e)
	}
	return db, jobCtx, cancel, nil
}
func defaultInt(n, def int) int {
	if n == 0 {
		return def
	}
	return n
}
func checkInnoDB(ctx context.Context, db *sql.DB, tx *sql.Tx, database, table string) error {
	parts := strings.Split(table, ".")
	schema := database
	name := parts[0]
	if len(parts) == 2 {
		schema = parts[0]
		name = parts[1]
	}
	// INFORMATION_SCHEMA metadata verification runs in the same snapshot.
	var engine sql.NullString
	e := tx.QueryRowContext(ctx, "SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?", schema, name).Scan(&engine)
	if e != nil || !engine.Valid || !strings.EqualFold(engine.String, "InnoDB") {
		return fmt.Errorf("source table %s must be an InnoDB BASE TABLE for snapshot guarantees; view/non-InnoDB/unknown table rejected", table)
	}
	return nil
}

// checkUniqueBigintKey prevents VARCHAR collation drift, duplicate cursor
// values and truncated unsigned ranges during keyset pagination.
func checkUniqueBigintKey(ctx context.Context, tx *sql.Tx, database string, q MySQLQuery) error {
	parts := strings.Split(q.Table, ".")
	schema := database
	table := parts[0]
	if len(parts) == 2 {
		schema = parts[0]
		table = parts[1]
	}
	var typ, nullable, columnType string
	e := tx.QueryRowContext(ctx, "SELECT DATA_TYPE, IS_NULLABLE, COLUMN_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = ?", schema, table, q.KeyColumn).Scan(&typ, &nullable, &columnType)
	if e != nil || !strings.EqualFold(typ, "bigint") || !strings.EqualFold(nullable, "NO") || strings.Contains(strings.ToLower(columnType), "unsigned") {
		return fmt.Errorf("key_column %s must be NOT NULL signed BIGINT", q.KeyColumn)
	}
	var indexes int
	e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? GROUP BY INDEX_NAME HAVING MAX(NON_UNIQUE)=0 AND COUNT(*)=1 AND MAX(COLUMN_NAME)=?) AS unique_keys`, schema, table, q.KeyColumn).Scan(&indexes)
	if e != nil || indexes < 1 {
		return fmt.Errorf("key_column %s must be the sole column of a UNIQUE/PRIMARY index", q.KeyColumn)
	}
	return nil
}
func ImportMySQLConfig(ctx context.Context, configPath, destination string) (r Report, err error) {
	cfg, e := readMySQLFileConfig(configPath)
	if e != nil {
		return r, e
	}
	db, jobCtx, cancel, e := openMySQL(ctx, cfg.Source.MySQLConnection)
	if e != nil {
		return r, e
	}
	defer cancel()
	defer db.Close()
	tx, e := db.BeginTx(jobCtx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return r, fmt.Errorf("snapshot transaction rejected: %w", e)
	}
	defer tx.Rollback()
	if e = checkInnoDB(jobCtx, db, tx, cfg.Source.Database, cfg.Source.Table); e != nil {
		return r, e
	}
	if e = checkUniqueBigintKey(jobCtx, tx, cfg.Source.Database, cfg.Source.MySQLQuery); e != nil {
		return r, e
	}
	r, e = ImportCSV(jobCtx, cfg.Source.MySQLQuery.sqlSource(db, tx), cfg.Mapping, destination)
	if e != nil {
		return r, e
	}
	if e = tx.Rollback(); e != nil {
		os.Remove(destination)
		return Report{}, fmt.Errorf("snapshot release failed: %w", e)
	}
	r.SourceKind = "mysql"
	return r, nil
}
func ImportMySQLBundleConfig(ctx context.Context, configPath, destination string) (BundleReport, error) {
	cfg, e := readMySQLBundleConfig(configPath)
	if e != nil {
		return BundleReport{}, e
	}
	db, jobCtx, cancel, e := openMySQL(ctx, cfg.Source.MySQLConnection)
	if e != nil {
		return BundleReport{}, e
	}
	defer cancel()
	defer db.Close()
	// Verify all source tables before starting the one-snapshot import.
	tx, e := db.BeginTx(jobCtx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return BundleReport{}, e
	}
	for _, job := range cfg.Exports {
		if e = checkInnoDB(jobCtx, db, tx, cfg.Source.Database, job.Table); e != nil {
			_ = tx.Rollback()
			return BundleReport{}, e
		}
		if e = checkUniqueBigintKey(jobCtx, tx, cfg.Source.Database, job.MySQLQuery); e != nil {
			_ = tx.Rollback()
			return BundleReport{}, e
		}
	}
	if e = tx.Rollback(); e != nil {
		return BundleReport{}, e
	}
	jobs := make([]BundleExport, 0, len(cfg.Exports))
	for _, v := range cfg.Exports {
		jobs = append(jobs, BundleExport{Name: v.Name, Query: v.MySQLQuery, Mapping: v.Mapping})
	}
	return ImportSQLBundle(jobCtx, db, jobs, destination)
}
