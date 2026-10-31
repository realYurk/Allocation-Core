//go:build mysql && mysql_integration

package adapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

// TestLiveMySQLSnapshot uses an actual ephemeral InnoDB server. This test is
// intentionally opt-in and must NEVER target the user's production database.
func TestLiveMySQLSnapshot(t *testing.T) {
	dsn := os.Getenv("ALLOCATION_TEST_MYSQL_DSN")
	if os.Getenv("ALLOCATION_MYSQL_TEST_EPHEMERAL") != "1" || !strings.Contains(dsn, "@tcp(127.0.0.1:") {
		t.Skip("integration modifies ephemeral localhost database only")
	}
	if dsn == "" {
		t.Skip("set ALLOCATION_TEST_MYSQL_DSN for isolated test database")
	}
	db, e := sql.Open("mysql", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if e = db.PingContext(ctx); e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{
		"CREATE TABLE IF NOT EXISTS clean_connector_test (id BIGINT NOT NULL PRIMARY KEY, period VARCHAR(30) NOT NULL, site VARCHAR(30) NOT NULL, amount DECIMAL(24,2) NOT NULL) ENGINE=InnoDB",
		"DELETE FROM clean_connector_test",
		"INSERT INTO clean_connector_test VALUES (1,'2026-01','A',10.00),(2,'2026-01','A',20.00),(3,'2026-01','A',30.00)",
	} {
		if _, e = db.ExecContext(ctx, q); e != nil {
			t.Fatal(e)
		}
	}
	defer db.ExecContext(context.Background(), "DROP TABLE clean_connector_test")
	_, e = db.ExecContext(ctx, "CREATE USER IF NOT EXISTS 'allocation_read_ci'@'%' IDENTIFIED BY 'temporary_ci_only'")
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.ExecContext(ctx, "GRANT SELECT ON allocation_ci.* TO 'allocation_read_ci'@'%'")
	if e != nil {
		t.Fatal(e)
	}
	reader, e := sql.Open("mysql", "allocation_read_ci:temporary_ci_only@tcp(127.0.0.1:3306)/allocation_ci")
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	if e = reader.PingContext(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = reader.ExecContext(ctx, "UPDATE clean_connector_test SET amount=0 WHERE id=1"); e == nil {
		t.Fatal("read-only account unexpectedly allowed UPDATE")
	}

	q := MySQLQuery{Table: "clean_connector_test", Columns: []string{"id", "period", "site", "amount"}, KeyColumn: "id", BatchSize: 2}
	tx, e := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		t.Fatal(e)
	}
	if e = checkInnoDB(ctx, reader, tx, "allocation_ci", q.Table); e != nil {
		t.Fatal(e)
	}
	if e = checkUniqueBigintKey(ctx, tx, "allocation_ci", q); e != nil {
		t.Fatal(e)
	}
	if e = checkExactMeasureType(ctx, tx, "allocation_ci", q.Table, "amount", 2); e != nil {
		t.Fatal(e)
	}
	if e = checkExactMeasureType(ctx, tx, "allocation_ci", q.Table, "amount", 1); e == nil {
		t.Fatal("underprecise mapping accepted")
	}
	rows, e := q.sqlSource(reader, tx).Open(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if _, e = rows.Next(); e != nil {
			t.Fatal(e)
		}
	}
	// A concurrent writer changes an unread row. The next keyset page must
	// still see the prior value from the same REPEATABLE READ snapshot.
	if _, e = db.ExecContext(ctx, "UPDATE clean_connector_test SET amount=999.99 WHERE id=3"); e != nil {
		t.Fatal(e)
	}
	r, e := rows.Next()
	if e != nil {
		t.Fatal(e)
	}
	if r[3] != "30.00" {
		t.Fatalf("snapshot changed between batches: %v", r)
	}
	if e = rows.Close(); e != nil {
		t.Fatal(e)
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	mapping := Mapping{Dimensions: []string{"period", "site"}, Columns: map[string]string{"period": "period", "site": "site"}, ValueSource: "amount", ValueName: "amount", Precision: 2}
	// The subsequent independent transaction must see the newer value.
	dest := filepath.Join(t.TempDir(), "out.csv")
	report, e := ImportCSV(ctx, q.sqlSource(reader, nil), mapping, dest)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(dest)
	if report.Rows != 3 || !strings.Contains(string(b), "999.99") {
		t.Fatal("new snapshot not visible", fmt.Sprint(report), string(b))
	}
	// Bundle publishes exactly once for all tables.
	bundle := filepath.Join(t.TempDir(), "bundle")
	got, e := ImportSQLBundle(ctx, reader, []BundleExport{{Name: "amounts.csv", Query: q, Mapping: mapping}, {Name: "drivers.csv", Query: q, Mapping: mapping}}, bundle)
	if e != nil || got.Status != "PASS" {
		t.Fatal(e, got)
	}
	// Import through the same least-privilege connection and protect from
	// silently accepting float columns when a driver returns textual values.
	if _, e = db.ExecContext(ctx, "CREATE TABLE clean_connector_float (id BIGINT NOT NULL PRIMARY KEY, amount DOUBLE NOT NULL) ENGINE=InnoDB"); e != nil {
		t.Fatal(e)
	}
	defer db.ExecContext(context.Background(), "DROP TABLE clean_connector_float")
	txFloat, e := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		t.Fatal(e)
	}
	if e = checkExactMeasureType(ctx, txFloat, "allocation_ci", "clean_connector_float", "amount", 2); e == nil {
		t.Fatal("FLOAT measure allowed")
	}
	_ = txFloat.Rollback()
}

// TestLiveMySQLNativeTLSImport is opt-in because the generic MySQL 8.4 service
// uses an auto-generated certificate without the expected SAN. Provide a
// purpose-issued CA and server cert for the ephemeral test server. This test
// verifies the actual native connector (not only an injected database/sql).
func TestLiveMySQLNativeTLSImport(t *testing.T) {
	if os.Getenv("ALLOCATION_MYSQL_TEST_EPHEMERAL") != "1" {
		t.Skip("isolated test server only")
	}
	ca := os.Getenv("ALLOCATION_TEST_MYSQL_TLS_CA_FILE")
	serverName := os.Getenv("ALLOCATION_TEST_MYSQL_TLS_SERVER_NAME")
	if ca == "" || serverName == "" {
		t.Skip("requires SAN-verified ephemeral server certificate")
	}
	rootDSN := os.Getenv("ALLOCATION_TEST_MYSQL_DSN")
	if !strings.Contains(rootDSN, "@tcp(127.0.0.1:") {
		t.Skip("must be isolated localhost test")
	}
	// Even the test administrator uses a verified SAN/CA TLS connection.
	// A REQUIRE_SECURE_TRANSPORT test server rejects plain-text fallback.
	admin, e := mysql.ParseDSN(rootDSN)
	if e != nil {
		t.Fatal(e)
	}
	caPEM, e := os.ReadFile(ca)
	if e != nil {
		t.Fatal(e)
	}
	strictTLS, e := verifiedMySQLTLS(serverName, caPEM)
	if e != nil {
		t.Fatal(e)
	}
	if e = mysql.RegisterTLSConfig("allocation-ci-native-tls-admin", strictTLS); e != nil {
		t.Fatal(e)
	}
	admin.TLSConfig = "allocation-ci-native-tls-admin"
	db, e := sql.Open("mysql", admin.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, statement := range []string{
		"CREATE TABLE clean_connector_tls_v7 (id BIGINT PRIMARY KEY, period VARCHAR(20) NOT NULL, amount DECIMAL(20,2) NOT NULL) ENGINE=InnoDB",
		"INSERT INTO clean_connector_tls_v7 VALUES (1,'2026-01',12.34),(2,'2026-01',56.78)",
		"CREATE USER IF NOT EXISTS 'allocation_tls_ci'@'%' IDENTIFIED BY 'isolated-test-password'",
		"GRANT SELECT ON allocation_ci.clean_connector_tls_v7 TO 'allocation_tls_ci'@'%'",
	} {
		if _, e = db.ExecContext(ctx, statement); e != nil {
			t.Fatal(e)
		}
	}
	defer db.ExecContext(context.Background(), "DROP TABLE clean_connector_tls_v7")
	t.Setenv("ALLOCATION_CI_TLS_USER", "allocation_tls_ci")
	t.Setenv("ALLOCATION_CI_TLS_PASSWORD", "isolated-test-password")
	// No password is serialized. The reader can only SELECT this test table.
	address := "127.0.0.1:3306"
	if v := os.Getenv("ALLOCATION_TEST_MYSQL_TLS_ADDRESS"); v != "" {
		address = v
	}
	cfg := MySQLFileConfig{}
	cfg.Source.Type = "mysql"
	cfg.Source.MySQLConnection = MySQLConnection{Address: address, Database: "allocation_ci", UserEnv: "ALLOCATION_CI_TLS_USER", PasswordEnv: "ALLOCATION_CI_TLS_PASSWORD", TLSCAFile: ca, TLSServerName: serverName}
	cfg.Source.MySQLQuery = MySQLQuery{Table: "clean_connector_tls_v7", Columns: []string{"id", "period", "amount"}, KeyColumn: "id", BatchSize: 1}
	cfg.Mapping = Mapping{Dimensions: []string{"period"}, Columns: map[string]string{"period": "period"}, ValueSource: "amount", ValueName: "amount", Precision: 2}
	bytes, e := json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	config := filepath.Join(t.TempDir(), "mysql-test.json")
	if e = os.WriteFile(config, bytes, 0600); e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(t.TempDir(), "strictly-read-only.csv")
	rep, e := ImportMySQLConfig(ctx, config, dest)
	if e != nil || rep.Rows != 2 || rep.SHA256 == "" {
		t.Fatalf("TLS native connector: %+v err=%v", rep, e)
	}
	raw, e := os.ReadFile(dest)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(raw), "12.34") || !strings.Contains(string(raw), "56.78") {
		t.Fatalf("wrong DECIMAL output: %s", raw)
	}
	// The actual MySQL connection must NOT silently fall back if the SAN
	// does not match, and a rejected connection must not publish any CSV.
	cfg.Source.TLSServerName = "wrong.mysql.test.internal"
	invalidBytes, e := json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	invalidCfg := filepath.Join(t.TempDir(), "invalid-san.json")
	if e = os.WriteFile(invalidCfg, invalidBytes, 0600); e != nil {
		t.Fatal(e)
	}
	invalidOutput := filepath.Join(t.TempDir(), "should-not-exist.csv")
	if _, e = ImportMySQLConfig(ctx, invalidCfg, invalidOutput); e == nil {
		t.Fatal("wrong SAN was accepted by native driver")
	}
	if _, e = os.Stat(invalidOutput); !os.IsNotExist(e) {
		t.Fatal("failed TLS verification published output")
	}
}
