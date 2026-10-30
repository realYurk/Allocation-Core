package adapter

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func sampleMapping() Mapping {
	return Mapping{Dimensions: []string{"period", "site", "item"}, Columns: map[string]string{"period": "Month", "site": "Factory", "item": "SKU"}, ValueSource: "Cost", ValueName: "amount", Precision: 2}
}
func TestCSVStreamNormalizeAndGranularity(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "raw.csv")
	dst := filepath.Join(dir, "normalized.csv")
	content := "Month,Factory,SKU,Cost\n2026-01,A,No Product,100.00\n2026-01,A,X1,-4.01\n2026-01,A,X1,1.00\n"
	if e := os.WriteFile(src, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	r, e := ImportCSV(context.Background(), CSVSource{Path: src}, sampleMapping(), dst)
	if e != nil {
		t.Fatal(e)
	}
	if r.Rows != 3 || r.Status != "PASS" {
		t.Fatalf("%+v", r)
	}
	f, e := os.Open(dst)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	rr, e := csv.NewReader(f).ReadAll()
	if e != nil {
		t.Fatal(e)
	}
	want := [][]string{{"period", "site", "item", "amount"}, {"2026-01", "A", "No Product", "100.00"}, {"2026-01", "A", "X1", "-4.01"}, {"2026-01", "A", "X1", "1.00"}}
	if !reflect.DeepEqual(rr, want) {
		t.Fatalf("got %v", rr)
	}
	if _, e = ImportCSV(context.Background(), CSVSource{Path: src}, sampleMapping(), dst); e == nil {
		t.Fatal("should not overwrite")
	}
}
func TestImportFailsClosedAndNoPartialOutput(t *testing.T) {
	for _, raw := range []string{"Month,Factory,SKU,Cost\n2026-01,A,X,1.00\n2026-01,A,Y,bad\n", "Month,Factory,SKU,Cost\n2026-01,A,,1\n", "Month,Factory,SKU,Cost\n2026-01,A,X,1.000\n", "Month,Month,SKU,Cost\n2026-01,A,X,1\n"} {
		dir := t.TempDir()
		src := filepath.Join(dir, "bad.csv")
		dst := filepath.Join(dir, "output.csv")
		os.WriteFile(src, []byte(raw), 0600)
		if _, e := ImportCSV(context.Background(), CSVSource{Path: src}, sampleMapping(), dst); e == nil {
			t.Errorf("expected error for %q", raw)
		}
		if _, e := os.Stat(dst); !os.IsNotExist(e) {
			t.Fatalf("published invalid output")
		}
	}
}
func TestSQLGeneratedQueryReadOnlyAndExact(t *testing.T) {
	s := SQLSource{Table: "finance.expenses", ColumnsList: []string{"period", "site", "amount"}, Equal: map[string]string{"site": "Factory-A", "period": "2026-01"}}
	got, args, e := s.Statement()
	if e != nil {
		t.Fatal(e)
	}
	want := "SELECT `period`, `site`, `amount` FROM `finance`.`expenses` WHERE `period` = ? AND `site` = ?"
	if got != want || !reflect.DeepEqual(args, []any{"2026-01", "Factory-A"}) {
		t.Fatalf("query %s args %v", got, args)
	}
	for _, table := range []string{"expenses;DROP TABLE x", "finance.table ORDER BY amount", "finance..expenses"} {
		s.Table = table
		if _, _, e := s.Statement(); e == nil {
			t.Fatalf("accepted injection %s", table)
		}
	}
	// Functional mock database/sql driver: verifies transaction is marked read only
	// and that DECIMAL arrives as exact bytes, without a live MySQL server.
	name := "clean_core_sql_mock"
	registerMockSQL.Do(func() { sql.Register(name, &mockDriver{}) })
	db, e := sql.Open(name, "")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s = SQLSource{DB: db, Table: "expenses", ColumnsList: []string{"period", "site", "amount"}}
	mapping := Mapping{Dimensions: []string{"period", "site"}, Columns: map[string]string{"period": "period", "site": "site"}, ValueSource: "amount", ValueName: "amount", Precision: 2}
	dst := filepath.Join(t.TempDir(), "normalized.csv")
	r, e := ImportCSV(context.Background(), s, mapping, dst)
	if e != nil {
		t.Fatal(e)
	}
	if r.Rows != 1 {
		t.Fatal(r)
	}
	b, _ := os.ReadFile(dst)
	if !strings.Contains(string(b), "2026-01,Factory-A,123456789012345.67") {
		t.Fatalf("decimal corrupted %s", b)
	}
}
func TestCubeSourceKeepsSparseCoordinates(t *testing.T) {
	cursor := &fakeCube{cells: []Cell{{Members: map[string]string{"period": "2026-01", "site": "A", "sku": "No Model"}, Value: "9.99"}}}
	s := CubeSource{Cursor: cursor, Dimensions: []string{"period", "site", "sku"}, ValueColumn: "cost"}
	m := Mapping{Dimensions: []string{"period", "site", "sku"}, Columns: map[string]string{"period": "period", "site": "site", "sku": "sku"}, ValueSource: "cost", ValueName: "amount", Precision: 2}
	dst := filepath.Join(t.TempDir(), "out.csv")
	if _, e := ImportCSV(context.Background(), s, m, dst); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(dst)
	if !strings.Contains(string(b), "No Model,9.99") {
		t.Fatalf("cube granularity changed: %s", b)
	}
	if !cursor.closed {
		t.Fatal("cursor leak")
	}
}

type fakeCube struct {
	cells  []Cell
	i      int
	closed bool
}

func (f *fakeCube) Next(ctx context.Context) (Cell, error) {
	if e := ctx.Err(); e != nil {
		return Cell{}, e
	}
	if f.i == len(f.cells) {
		return Cell{}, io.EOF
	}
	v := f.cells[f.i]
	f.i++
	return v, nil
}
func (f *fakeCube) Close() error { f.closed = true; return nil }

var registerMockSQL sync.Once

type mockDriver struct{}

func (*mockDriver) Open(string) (driver.Conn, error) { return &mockConn{}, nil }

type mockConn struct{}

func (*mockConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements not allowed")
}
func (*mockConn) Close() error              { return nil }
func (*mockConn) Begin() (driver.Tx, error) { return nil, errors.New("must request read-only tx") }
func (*mockConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if !opts.ReadOnly {
		return nil, errors.New("not read-only")
	}
	return mockTx{}, nil
}
func (*mockConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if !strings.HasPrefix(q, "SELECT ") {
		return nil, errors.New("not SELECT")
	}
	return &mockRows{}, nil
}

type mockTx struct{}

func (mockTx) Commit() error   { return nil }
func (mockTx) Rollback() error { return nil }

type mockRows struct{ n int }

func (*mockRows) Columns() []string { return []string{"period", "site", "amount"} }
func (*mockRows) Close() error      { return nil }
func (r *mockRows) Next(dest []driver.Value) error {
	if r.n > 0 {
		return io.EOF
	}
	r.n++
	dest[0] = "2026-01"
	dest[1] = "Factory-A"
	dest[2] = []byte("123456789012345.67")
	return nil
}
