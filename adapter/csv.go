package adapter

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
)

type CSVSource struct{ Path string }
type csvRows struct {
	file *os.File
	r    *csv.Reader
	cols []string
}

func (s CSVSource) Open(ctx context.Context) (Rows, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	f, e := os.Open(s.Path)
	if e != nil {
		return nil, e
	}
	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	header, e := reader.Read()
	if e != nil {
		f.Close()
		if e == io.EOF {
			e = fmt.Errorf("CSV missing header")
		}
		return nil, e
	}
	if len(header) > 0 {
		header[0] = trimBOM(header[0])
	}
	return &csvRows{file: f, r: reader, cols: header}, nil
}
func trimBOM(v string) string {
	if len(v) >= 3 && v[:3] == "\xef\xbb\xbf" {
		return v[3:]
	}
	return v
}
func (c *csvRows) Columns() []string       { return append([]string(nil), c.cols...) }
func (c *csvRows) Next() ([]string, error) { return c.r.Read() }
func (c *csvRows) Close() error            { return c.file.Close() }
