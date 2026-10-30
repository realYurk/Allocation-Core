package adapter

import (
	"context"
	"fmt"
)

// Cell is a generic sparse coordinate and an exact decimal amount/weight.
// It has NO concept of a particular TM1/SAP/OLAP vendor or a total placeholder.
type Cell struct {
	Members map[string]string
	Value   string
}

// CellCursor is implemented by a private vendor connector. End with io.EOF.
type CellCursor interface {
	Next(context.Context) (Cell, error)
	Close() error
}
type CubeSource struct {
	Cursor      CellCursor
	Dimensions  []string
	ValueColumn string
}

func (s CubeSource) Open(ctx context.Context) (Rows, error) {
	if s.Cursor == nil || len(s.Dimensions) == 0 || s.ValueColumn == "" {
		return nil, fmt.Errorf("cube cursor, dimensions and value column required")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	cols := append(append([]string(nil), s.Dimensions...), s.ValueColumn)
	return &cubeRows{cursor: s.Cursor, cols: cols, ctx: ctx}, nil
}

type cubeRows struct {
	cursor CellCursor
	cols   []string
	ctx    context.Context
}

func (c *cubeRows) Columns() []string { return append([]string(nil), c.cols...) }
func (c *cubeRows) Next() ([]string, error) {
	cell, e := c.cursor.Next(c.ctx)
	if e != nil {
		return nil, e
	}
	row := make([]string, 0, len(c.cols))
	for _, d := range c.cols[:len(c.cols)-1] {
		v, ok := cell.Members[d]
		if !ok {
			return nil, fmt.Errorf("cube cell missing dimension %q", d)
		}
		row = append(row, v)
	}
	row = append(row, cell.Value)
	return row, nil
}
func (c *cubeRows) Close() error { return c.cursor.Close() }
