package vault

import (
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ExportDB is a lossless-in-values, streaming *projection* of final outputs
// intended for DB bulk COPY. It never replays allocation calculations and
// does not attempt to load or overwrite an application's SQL data itself.
// Detailed ledger is kept in the vault, not duplicated in the OLTP database.
func ExportDB(root, id, destination string) (int64, error) {
	if _, e := os.Stat(destination); e == nil {
		return 0, fmt.Errorf("DB export destination exists")
	}
	r, e := getRecord(root, id)
	if e != nil {
		return 0, e
	}
	if e = Verify(root, id); e != nil {
		return 0, fmt.Errorf("vault verification required before DB export: %w", e)
	}
	ref, ok := r.Files["results.csv"]
	if !ok {
		return 0, fmt.Errorf("missing results.csv")
	}
	f, e := os.Open(objectPath(root, ref))
	if e != nil {
		return 0, e
	}
	defer f.Close()
	var reader io.Reader = f
	if ref.Encoding == "gzip" {
		gz, ee := gzip.NewReader(f)
		if ee != nil {
			return 0, ee
		}
		defer gz.Close()
		reader = gz
	}
	csvr := csv.NewReader(reader)
	csvr.FieldsPerRecord = -1
	header, e := csvr.Read()
	if e != nil {
		return 0, e
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	seen := map[string]bool{}
	idx := map[string]int{}
	for i, k := range header {
		if k == "" || seen[k] {
			return 0, fmt.Errorf("invalid duplicate/empty CSV header: %s", k)
		}
		seen[k] = true
		idx[k] = i
	}
	for _, required := range []string{"row_id", "terminal_stage", "amount"} {
		if _, ok := idx[required]; !ok {
			return 0, fmt.Errorf("missing results field %s", required)
		}
	}
	if e = os.MkdirAll(filepath.Dir(destination), 0700); e != nil {
		return 0, e
	}
	tmp, e := os.CreateTemp(filepath.Dir(destination), ".dbexport-")
	if e != nil {
		return 0, e
	}
	defer os.Remove(tmp.Name())
	writer := csv.NewWriter(tmp)
	if _, e = tmp.Write([]byte{0xef, 0xbb, 0xbf}); e != nil {
		tmp.Close()
		return 0, e
	}
	if e = writer.Write([]string{"run_id", "row_id", "terminal_stage", "amount", "dimensions_json"}); e != nil {
		tmp.Close()
		return 0, e
	}
	var rows int64
	for {
		line, ee := csvr.Read()
		if ee == io.EOF {
			break
		}
		if ee != nil {
			tmp.Close()
			return rows, fmt.Errorf("source CSV row %d: %w", rows+2, ee)
		}
		if len(line) != len(header) {
			tmp.Close()
			return rows, fmt.Errorf("malformed results row %d", rows+2)
		}
		coords := map[string]string{}
		for k, i := range idx {
			if k != "row_id" && k != "terminal_stage" && k != "amount" {
				coords[k] = line[i]
			}
		}
		if !utf8.ValidString(line[idx["row_id"]]) || line[idx["row_id"]] == "" {
			tmp.Close()
			return rows, fmt.Errorf("bad row ID")
		}
		b, ee := json.Marshal(coords)
		if ee != nil {
			tmp.Close()
			return rows, ee
		}
		if e = writer.Write([]string{id, line[idx["row_id"]], line[idx["terminal_stage"]], line[idx["amount"]], string(b)}); e != nil {
			tmp.Close()
			return rows, e
		}
		rows++
	}
	writer.Flush()
	if e = writer.Error(); e == nil {
		e = tmp.Sync()
	}
	if ce := tmp.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return rows, e
	}
	if e = os.Rename(tmp.Name(), destination); e != nil {
		return rows, e
	}
	return rows, nil
}
