//go:build mysql && mysql_integration

package adapter

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

// Verifies the InnoDB metadata-lock property with the same read-only
// REPEATABLE READ transaction used by the import. DDL must not silently
// replace a source table while the transaction is reading its snapshot.
// Only run against an ephemeral local MySQL database created by CI.
func TestV8LiveConcurrentDDLIsBlockedUntilSnapshotReleased(t *testing.T) {
	dsn := os.Getenv("ALLOCATION_TEST_MYSQL_DSN")
	if os.Getenv("ALLOCATION_MYSQL_TEST_EPHEMERAL") != "1" || !strings.Contains(dsn, "@tcp(127.0.0.1:") {
		t.Skip("requires explicitly authorized ephemeral loopback MySQL")
	}
	parsed, e := mysql.ParseDSN(dsn)
	if e != nil {
		t.Fatal(e)
	}
	if parsed.DBName != "allocation_ci" {
		t.Skip("requires isolated allocation_ci database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root, e := sql.Open("mysql", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	const table = "clean_connector_ddl_v8"
	_, _ = root.ExecContext(ctx, "DROP TABLE IF EXISTS "+table)
	_, e = root.ExecContext(ctx, "CREATE TABLE "+table+" (id BIGINT NOT NULL PRIMARY KEY, amount DECIMAL(20,2) NOT NULL) ENGINE=InnoDB")
	if e != nil {
		t.Fatal(e)
	}
	defer root.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table)
	if _, e = root.ExecContext(ctx, "INSERT INTO "+table+" VALUES (1,10.00),(2,20.00)"); e != nil {
		t.Fatal(e)
	}
	if _, e = root.ExecContext(ctx, "CREATE USER IF NOT EXISTS 'allocation_ddl_ci'@'%' IDENTIFIED BY 'isolated_ddl_test_only'"); e != nil {
		t.Fatal(e)
	}
	if _, e = root.ExecContext(ctx, "GRANT SELECT ON allocation_ci."+table+" TO 'allocation_ddl_ci'@'%'"); e != nil {
		t.Fatal(e)
	}
	readerDSN := fmt.Sprintf("allocation_ddl_ci:isolated_ddl_test_only@tcp(%s)/allocation_ci", parsed.Addr)
	reader, e := sql.Open("mysql", readerDSN)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	tx, e := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	q := MySQLQuery{Table: table, Columns: []string{"id", "amount"}, KeyColumn: "id", BatchSize: 1}
	if e = checkInnoDB(ctx, reader, tx, "allocation_ci", table); e != nil {
		t.Fatal(e)
	}
	if e = checkUniqueBigintKey(ctx, tx, "allocation_ci", q); e != nil {
		t.Fatal(e)
	}
	rows, e := q.sqlSource(reader, tx).Open(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = rows.Next(); e != nil {
		t.Fatal(e)
	}
	if e = rows.Close(); e != nil {
		t.Fatal(e)
	}

	writerConn, e := root.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer writerConn.Close()
	if _, e = writerConn.ExecContext(ctx, "SET SESSION lock_wait_timeout=10"); e != nil {
		t.Fatal(e)
	}
	ddlResult := make(chan error, 1)
	go func() {
		_, err := writerConn.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN ddl_marker INT NULL")
		ddlResult <- err
	}()
	select {
	case e = <-ddlResult:
		t.Fatalf("DDL was not protected by the active read transaction (err=%v)", e)
	case <-time.After(250 * time.Millisecond):
		// DDL is waiting for the read-only transaction's metadata lock.
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-ddlResult:
		if e != nil {
			t.Fatalf("DDL did not resume after read snapshot release: %v", e)
		}
	case <-ctx.Done():
		t.Fatal("DDL remained blocked after snapshot release")
	}
}
