//go:build mysql && mysql_integration

package adapter

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
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
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		t.Fatal(e)
	}
	if e = checkInnoDB(ctx, db, tx, "allocation_ci", q.Table); e != nil {
		t.Fatal(e)
	}
	if e = checkUniqueBigintKey(ctx, tx, "allocation_ci", q); e != nil {
		t.Fatal(e)
	}
	rows, e := q.sqlSource(db, tx).Open(ctx)
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
	report, e := ImportCSV(ctx, q.sqlSource(db, nil), mapping, dest)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(dest)
	if report.Rows != 3 || !strings.Contains(string(b), "999.99") {
		t.Fatal("new snapshot not visible", fmt.Sprint(report), string(b))
	}
	// Bundle publishes exactly once for all tables.
	bundle := filepath.Join(t.TempDir(), "bundle")
	got, e := ImportSQLBundle(ctx, db, []BundleExport{{Name: "amounts.csv", Query: q, Mapping: mapping}, {Name: "drivers.csv", Query: q, Mapping: mapping}}, bundle)
	if e != nil || got.Status != "PASS" {
		t.Fatal(e, got)
	}
}
