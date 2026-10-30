package adapter

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SQLSource uses a caller-supplied, ALREADY OPEN, read-only-capable database/sql
// handle. It does not register drivers or store database credentials. It never
// accepts SQL expressions from input: SELECT and WHERE are generated here.
// A read-only DB user is still required: driver enforcement varies.
type SQLSource struct {
	DB          *sql.DB
	Table       string // `table` or `schema.table`, restricted identifiers
	ColumnsList []string
	Equal       map[string]string
}

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func sqlIdent(v string) (string, error) {
	parts := strings.Split(v, ".")
	if len(parts) < 1 || len(parts) > 2 {
		return "", fmt.Errorf("invalid table identifier %q", v)
	}
	for _, p := range parts {
		if !identifier.MatchString(p) {
			return "", fmt.Errorf("invalid SQL identifier %q", p)
		}
	}
	quoted := make([]string, len(parts))
	for i, p := range parts {
		quoted[i] = "`" + p + "`"
	}
	return strings.Join(quoted, "."), nil
}
func (s SQLSource) Statement() (string, []any, error) {
	if len(s.ColumnsList) == 0 {
		return "", nil, fmt.Errorf("columns required")
	}
	table, e := sqlIdent(s.Table)
	if e != nil {
		return "", nil, e
	}
	cols := make([]string, len(s.ColumnsList))
	seen := map[string]bool{}
	for i, c := range s.ColumnsList {
		if seen[c] {
			return "", nil, fmt.Errorf("duplicate column %q", c)
		}
		seen[c] = true
		cols[i], e = sqlIdent(c)
		if e != nil || strings.Contains(c, ".") {
			return "", nil, fmt.Errorf("invalid column %q", c)
		}
	}
	q := "SELECT " + strings.Join(cols, ", ") + " FROM " + table
	keys := make([]string, 0, len(s.Equal))
	for k := range s.Equal {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]any, 0, len(keys))
	clauses := make([]string, 0, len(keys))
	for _, k := range keys {
		if !seen[k] {
			return "", nil, fmt.Errorf("filter column %q must be selected", k)
		}
		c, _ := sqlIdent(k)
		clauses = append(clauses, c+" = ?")
		args = append(args, s.Equal[k])
	}
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	return q, args, nil
}
func (s SQLSource) Open(ctx context.Context) (Rows, error) {
	if s.DB == nil {
		return nil, fmt.Errorf("nil SQL handle")
	}
	stmt, args, e := s.Statement()
	if e != nil {
		return nil, e
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, fmt.Errorf("read-only transaction rejected: %w", e)
	}
	rows, e := tx.QueryContext(ctx, stmt, args...)
	if e != nil {
		tx.Rollback()
		return nil, e
	}
	return &sqlRows{rows: rows, tx: tx, cols: append([]string(nil), s.ColumnsList...)}, nil
}

type sqlRows struct {
	rows *sql.Rows
	tx   *sql.Tx
	cols []string
}

func (r *sqlRows) Columns() []string { return append([]string(nil), r.cols...) }
func (r *sqlRows) Next() ([]string, error) {
	if !r.rows.Next() {
		if e := r.rows.Err(); e != nil {
			return nil, e
		}
		return nil, io.EOF
	}
	values := make([]any, len(r.cols))
	dest := make([]any, len(r.cols))
	for i := range values {
		dest[i] = &values[i]
	}
	if e := r.rows.Scan(dest...); e != nil {
		return nil, e
	}
	result := make([]string, len(values))
	for i, v := range values {
		switch v := v.(type) {
		case nil:
			return nil, fmt.Errorf("SQL NULL in selected field %q", r.cols[i])
		case string:
			result[i] = v
		case []byte:
			result[i] = string(v) // exact MySQL DECIMAL text
		case int64:
			result[i] = fmt.Sprint(v)
		case bool:
			result[i] = fmt.Sprint(v)
		case time.Time:
			result[i] = v.Format("2006-01-02 15:04:05.999999999")
		case float64:
			return nil, fmt.Errorf("SQL float field %q cannot guarantee exact financial decimals; cast to DECIMAL/text", r.cols[i])
		default:
			return nil, fmt.Errorf("unsupported SQL type %T for %q", v, r.cols[i])
		}
	}
	return result, nil
}
func (r *sqlRows) Close() error {
	e := r.rows.Close()
	rollback := r.tx.Rollback()
	if e != nil {
		return e
	}
	if rollback != nil && rollback != sql.ErrTxDone {
		return rollback
	}
	return nil
}
