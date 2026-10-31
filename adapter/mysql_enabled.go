//go:build mysql

package adapter

import (
	"context"
	"crypto/sha256"
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
	tlsConfig, e := verifiedMySQLTLS(c.TLSServerName, ca)
	if e != nil {
		return nil, nil, nil, e
	}
	h := sha256.Sum256(append(append([]byte(c.TLSServerName+"\x00"), ca...), 0))
	tlsName := "allocation-verified-" + hex.EncodeToString(h[:])
	mysqlTLSRegistry.Lock()
	if !mysqlTLSSeen[tlsName] {
		e = mysql.RegisterTLSConfig(tlsName, tlsConfig)
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
	var engine, tableType sql.NullString
	e := tx.QueryRowContext(ctx, "SELECT ENGINE, TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?", schema, name).Scan(&engine, &tableType)
	if e != nil || !engine.Valid || !strings.EqualFold(engine.String, "InnoDB") || !tableType.Valid || !strings.EqualFold(tableType.String, "BASE TABLE") {
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

// checkExactMeasureType rejects binary floating-point SQL columns, even if
// the driver happens to return their formatted bytes as strings. Financial
// accuracy must not depend on driver-specific scan representation.
func checkExactMeasureType(ctx context.Context, tx *sql.Tx, database, table, valueColumn string, precision int) error {
	parts := strings.Split(table, ".")
	schema, name := database, parts[0]
	if len(parts) == 2 {
		schema, name = parts[0], parts[1]
	}
	var dataType string
	var scale sql.NullInt64
	e := tx.QueryRowContext(ctx, "SELECT DATA_TYPE, NUMERIC_SCALE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = ?", schema, name, valueColumn).Scan(&dataType, &scale)
	if e != nil {
		return fmt.Errorf("value column %s must exist and use an exact SQL numeric type: %w", valueColumn, e)
	}
	switch strings.ToLower(dataType) {
	case "decimal", "numeric", "bigint", "int", "integer", "mediumint", "smallint", "tinyint":
		if scale.Valid && scale.Int64 > int64(precision) {
			return fmt.Errorf("%s scale %d exceeds configured precision %d", valueColumn, scale.Int64, precision)
		}
		return nil
	default:
		return fmt.Errorf("value column %s uses %s; only exact DECIMAL or integer SQL types are supported", valueColumn, dataType)
	}
}

func ImportMySQLConfig(ctx context.Context, configPath, destination string) (Report, error) {
	cfg, e := readMySQLFileConfig(configPath)
	if e != nil {
		return Report{}, e
	}
	db, jobCtx, cancel, e := openMySQL(ctx, cfg.Source.MySQLConnection)
	if e != nil {
		return Report{}, e
	}
	defer cancel()
	defer db.Close()
	return importMySQLSingleFromDB(jobCtx, db, cfg.Source.Database, cfg.Source.MySQLQuery, cfg.Mapping, destination, func(ctx context.Context, tx *sql.Tx) error {
		if e := checkInnoDB(ctx, db, tx, cfg.Source.Database, cfg.Source.Table); e != nil {
			return e
		}
		if e := checkUniqueBigintKey(ctx, tx, cfg.Source.Database, cfg.Source.MySQLQuery); e != nil {
			return e
		}
		return checkExactMeasureType(ctx, tx, cfg.Source.Database, cfg.Source.Table, cfg.Mapping.ValueSource, cfg.Mapping.Precision)
	})
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
	jobs := make([]BundleExport, 0, len(cfg.Exports))
	for _, v := range cfg.Exports {
		jobs = append(jobs, BundleExport{Name: v.Name, Query: v.MySQLQuery, Mapping: v.Mapping})
	}
	// Metadata and data reads are now in ONE read-only REPEATABLE READ tx.
	return importSQLBundleWithPreflight(jobCtx, db, jobs, destination, func(ctx context.Context, tx *sql.Tx) error {
		for _, job := range cfg.Exports {
			if e := checkInnoDB(ctx, db, tx, cfg.Source.Database, job.Table); e != nil {
				return e
			}
			if e := checkUniqueBigintKey(ctx, tx, cfg.Source.Database, job.MySQLQuery); e != nil {
				return e
			}
			if e := checkExactMeasureType(ctx, tx, cfg.Source.Database, job.Table, job.Mapping.ValueSource, job.Mapping.Precision); e != nil {
				return e
			}
		}
		return nil
	})
}
