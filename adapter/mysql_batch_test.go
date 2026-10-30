package adapter

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type snapshotDriver struct{ state *snapshotState }
type snapshotState struct {
	mu          sync.Mutex
	queries     int
	beginnings  int
	rollbacks   int
	injectError bool
	idRows      []driver.Value
	badValues   bool
}
type snapshotConn struct{ s *snapshotState }
type snapshotTx struct{ s *snapshotState }
type snapshotRows struct {
	cols  []string
	data  [][]driver.Value
	index int
}

func (d *snapshotDriver) Open(string) (driver.Conn, error) { return &snapshotConn{s: d.state}, nil }
func (c *snapshotConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *snapshotConn) Close() error              { return nil }
func (c *snapshotConn) Begin() (driver.Tx, error) { return nil, errors.New("must begin read-only RR") }
func (c *snapshotConn) BeginTx(_ context.Context, o driver.TxOptions) (driver.Tx, error) {
	if !o.ReadOnly || o.Isolation != driver.IsolationLevel(sql.LevelRepeatableRead) {
		return nil, errors.New("transaction must be read-only REPEATABLE READ")
	}
	c.s.mu.Lock()
	c.s.beginnings++
	c.s.mu.Unlock()
	return &snapshotTx{s: c.s}, nil
}
func (t *snapshotTx) Commit() error {
	return errors.New("read only transaction committed unexpectedly")
}
func (t *snapshotTx) Rollback() error { t.s.mu.Lock(); t.s.rollbacks++; t.s.mu.Unlock(); return nil }
func (c *snapshotConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	c.s.queries++
	if c.s.injectError && c.s.queries > 1 {
		return nil, errors.New("simulated network loss on later page")
	}
	if !strings.HasPrefix(q, "SELECT ") || !strings.Contains(q, "ORDER BY `id` ASC LIMIT ?") {
		return nil, errors.New("not safe keyset SELECT")
	}
	// Simulate a transaction snapshot: rows are fixed at BeginTx.
	last := int64(-1 << 63)
	limit := int64(100)
	if len(args) > 0 {
		if v, ok := args[len(args)-1].Value.(int64); ok {
			limit = v
		}
	}
	if strings.Contains(q, "`id` > ?") {
		if len(args) < 2 {
			return nil, errors.New("cursor argument missing")
		}
		last, _ = args[len(args)-2].Value.(int64)
	}
	out := [][]driver.Value{}
	for _, v := range c.s.idRows {
		id := v.(int64)
		if id > last && int64(len(out)) < limit {
			val := "123456789012345.67"
			if c.s.badValues && id == 3 {
				val = "invalid"
			}
			out = append(out, []driver.Value{id, "2026-01", "Site-A", []byte(val)})
		}
	}
	return &snapshotRows{cols: []string{"id", "period", "site", "amount"}, data: out}, nil
}
func (r *snapshotRows) Columns() []string { return r.cols }
func (r *snapshotRows) Close() error      { return nil }
func (r *snapshotRows) Next(v []driver.Value) error {
	if r.index == len(r.data) {
		return io.EOF
	}
	copy(v, r.data[r.index])
	r.index++
	return nil
}

var snapRegister sync.Once
var snapDriverInstance *snapshotDriver

func newSnapshotDB(t *testing.T, state *snapshotState) *sql.DB {
	t.Helper()
	// driver instance contains state: serialize access by creating a new driver name per test is not needed
	// in normal tests, but use a single DB with controllable state under the global registration.
	snapRegister.Do(func() {
		snapDriverInstance = &snapshotDriver{}
		sql.Register("allocation_snap_fake", snapDriverInstance)
	})
	snapDriverInstance.state = state
	db, e := sql.Open("allocation_snap_fake", "")
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}
func sampleSQLBatch() (MySQLQuery, Mapping) {
	q := MySQLQuery{Table: "expenses", Columns: []string{"id", "period", "site", "amount"}, KeyColumn: "id", BatchSize: 2}
	m := Mapping{Dimensions: []string{"period", "site"}, Columns: map[string]string{"period": "period", "site": "site"}, ValueSource: "amount", ValueName: "amount", Precision: 2}
	return q, m
}
func TestMySQLKeysetBatchSingleSnapshot(t *testing.T) {
	state := &snapshotState{idRows: []driver.Value{int64(1), int64(2), int64(3), int64(4), int64(5)}}
	db := newSnapshotDB(t, state)
	q, m := sampleSQLBatch()
	dst := filepath.Join(t.TempDir(), "out.csv")
	report, e := ImportCSV(context.Background(), q.sqlSource(db, nil), m, dst)
	if e != nil {
		t.Fatal(e)
	}
	if report.Rows != 5 {
		t.Fatalf("rows = %d", report.Rows)
	}
	b, e := os.ReadFile(dst)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Count(string(b), "2026-01,Site-A,123456789012345.67") != 5 {
		t.Fatalf("lost DECIMAL or data: %s", b)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.queries != 3 || state.beginnings != 1 || state.rollbacks != 1 {
		t.Fatalf("bad transaction/pages: queries=%d tx=%d rollbacks=%d", state.queries, state.beginnings, state.rollbacks)
	}
}
func TestMySQLBundleAtomicPublishAndFailure(t *testing.T) {
	// Two exports from one transaction share the same logical source snapshot.
	state := &snapshotState{idRows: []driver.Value{int64(1), int64(2), int64(3)}}
	db := newSnapshotDB(t, state)
	q, m := sampleSQLBatch()
	out := filepath.Join(t.TempDir(), "snapshot")
	jobs := []BundleExport{{Name: "amounts.csv", Query: q, Mapping: m}, {Name: "driver.csv", Query: q, Mapping: m}}
	report, e := ImportSQLBundle(context.Background(), db, jobs, out)
	if e != nil {
		t.Fatal(e)
	}
	if report.Status != "PASS" || len(report.Files) != 2 {
		t.Fatal(report)
	}
	for _, name := range []string{"amounts.csv", "driver.csv", "manifest.json"} {
		if _, e = os.Stat(filepath.Join(out, name)); e != nil {
			t.Fatal(e)
		}
	}
	state.mu.Lock()
	b, r := state.beginnings, state.rollbacks
	state.mu.Unlock()
	if b != 1 || r != 1 {
		t.Fatalf("bundle used %d tx, rollback=%d", b, r)
	}
	state.mu.Lock()
	state.queries = 0
	state.injectError = true
	state.mu.Unlock()
	bad := filepath.Join(t.TempDir(), "failed")
	if _, e := ImportSQLBundle(context.Background(), db, jobs, bad); e == nil {
		t.Fatal("expected simulated lost connection")
	}
	if _, e := os.Stat(bad); !os.IsNotExist(e) {
		t.Fatal("published incomplete bundle")
	}
	state.mu.Lock()
	state.queries = 0
	state.injectError = false
	state.badValues = true
	state.mu.Unlock()
	invalid := filepath.Join(t.TempDir(), "invalid")
	if _, e := ImportSQLBundle(context.Background(), db, jobs, invalid); e == nil {
		t.Fatal("invalid DECIMAL accepted")
	}
	if _, e := os.Stat(invalid); !os.IsNotExist(e) {
		t.Fatal("published invalid decimal bundle")
	}

}
func TestMySQLStrictConfigAndUnsafeSQLBlocked(t *testing.T) {
	q, m := sampleSQLBatch()
	_ = m
	cases := []MySQLQuery{
		{Table: "expenses;DROP TABLE x", Columns: q.Columns, KeyColumn: "id", BatchSize: 2},
		{Table: "expenses", Columns: q.Columns, KeyColumn: "id", BatchSize: 0},
		{Table: "expenses", Columns: []string{"amount"}, KeyColumn: "id", BatchSize: 2},
		{Table: "expenses", Columns: q.Columns, KeyColumn: "id DESC", BatchSize: 2},
	}
	for _, c := range cases {
		if e := c.validate(); e == nil {
			t.Errorf("accepted unsafe config %+v", c)
		}
	}
	if e := q.validate(); e != nil {
		t.Fatal(e)
	}
	// Required secrets are only environment variable names, never literals.
	c := MySQLConnection{Address: "127.0.0.1:3306", Database: "finance", UserEnv: "DB_USER", PasswordEnv: "DB_PASSWORD", TLSCAFile: "root.pem", TLSServerName: "mysql.internal"}
	if e := c.validate(); e != nil {
		t.Fatal(e)
	}
	c.UserEnv = "$(echo password)"
	if e := c.validate(); e == nil {
		t.Fatal("accepted invalid env name")
	}
	if !reflect.DeepEqual([]string{"1", "2"}, []string{strconv.Itoa(1), strconv.Itoa(2)}) {
		t.Fatal("sanity")
	}
}
