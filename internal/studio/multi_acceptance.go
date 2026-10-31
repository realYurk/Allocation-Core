package studio

// Business acceptance workbench: upload a private reference CSV, then reopen
// prior reconciliation evidence and download full coordinate differences.
// Allocation Core arithmetic and the acceptance comparator are unchanged.
import (
	"allocationcore/internal/acceptance"
	"allocationcore/internal/engine"
	"bufio"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxReferenceUpload int64 = 256 << 20

var acceptanceDirectoryPattern = regexp.MustCompile(`^acceptance-[0-9]{8}T[0-9]{6}\.[0-9]{9}$`)
var multiJobIDPattern = regexp.MustCompile(`^[0-9a-f]{24}$`)

type referenceInfo struct {
	Exists bool   `json:"exists"`
	Bytes  int64  `json:"bytes,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

func referenceFingerprint(dir string) (referenceInfo, error) {
	path := filepath.Join(dir, "reference.csv")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return referenceInfo{}, nil
	}
	if err != nil {
		return referenceInfo{}, err
	}
	if !info.Mode().IsRegular() {
		return referenceInfo{}, errors.New("reference.csv must be an ordinary file (no links)")
	}
	f, err := os.Open(path)
	if err != nil {
		return referenceInfo{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return referenceInfo{}, err
	}
	return referenceInfo{Exists: true, Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func (x *MultiApp) referenceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	dir, err := x.projectDir(r.URL.Query().Get("name"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	info, err := referenceFingerprint(dir)
	if err != nil {
		fail(w, 422, err)
		return
	}
	jsonOut(w, 200, info)
}

func projectReferenceContract(dir string) (int, []string, error) {
	assignments, err := readAssignments(filepath.Join(dir, "assignments.csv"))
	if err != nil {
		return 0, nil, err
	}
	if len(assignments) == 0 {
		return 0, nil, errors.New("project has no assigned accounts")
	}
	return readOnboardingTemplate(dir, assignments[0]["template"])
}

func writeReferenceCSV(ctxDone <-chan struct{}, source io.Reader, dest *os.File, expected []string, precision int) (int64, int64, string, string, error) {
	// Write bytes exactly as provided: the independent reference must not be
	// silently normalized, rounded or re-aggregated by the upload endpoint.
	h := sha256.New()
	counting := &referenceCountingWriter{w: io.MultiWriter(dest, h)}
	reader := csv.NewReader(bufio.NewReaderSize(io.TeeReader(source, counting), 256<<10))
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("reference header: %w", err)
	}
	indices := map[string]int{}
	for i, k := range header {
		if i == 0 {
			k = strings.TrimPrefix(k, "\ufeff")
		}
		if k == "" || !utf8.ValidString(k) {
			return 0, 0, "", "", errors.New("empty or non-UTF8 reference header")
		}
		if _, ok := indices[k]; ok {
			return 0, 0, "", "", fmt.Errorf("duplicate reference column %s", k)
		}
		indices[k] = i
	}
	amountCol := -1
	if i, ok := indices["amount"]; ok {
		amountCol = i
	}
	if i, ok := indices["value"]; ok {
		if amountCol >= 0 {
			return 0, 0, "", "", errors.New("use one amount column: amount OR value")
		}
		amountCol = i
	}
	if amountCol < 0 {
		return 0, 0, "", "", errors.New("reference needs an amount or value column")
	}
	required := map[string]bool{}
	for _, d := range expected {
		if _, ok := indices[d]; !ok {
			return 0, 0, "", "", fmt.Errorf("reference missing project dimension %s", d)
		}
		required[d] = true
	}
	for k := range indices {
		if k != "amount" && k != "value" && k != "terminal_stage" && k != "row_id" && !required[k] {
			return 0, 0, "", "", fmt.Errorf("reference contains unexpected business coordinate %s; map it to the project contract first", k)
		}
	}
	var rows int64
	total := new(big.Int)
	for {
		select {
		case <-ctxDone:
			return 0, 0, "", "", errors.New("reference upload canceled")
		default:
		}
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, 0, "", "", fmt.Errorf("reference row %d: %w", rows+2, err)
		}
		if len(row) != len(header) {
			return 0, 0, "", "", fmt.Errorf("reference row %d has %d columns, expected %d", rows+2, len(row), len(header))
		}
		for _, d := range expected {
			value := row[indices[d]]
			if strings.TrimSpace(value) == "" || !utf8.ValidString(value) {
				return 0, 0, "", "", fmt.Errorf("reference row %d empty/invalid coordinate %s", rows+2, d)
			}
		}
		amount, err := engine.ParseScaled(strings.TrimSpace(row[amountCol]), precision)
		if err != nil {
			return 0, 0, "", "", fmt.Errorf("reference row %d invalid decimal: %w", rows+2, err)
		}
		total.Add(total, big.NewInt(amount))
		rows++
	}
	if rows == 0 {
		return 0, 0, "", "", errors.New("reference CSV has no amount rows")
	}
	// Format the sum using exact fixed-point arithmetic, not float64.
	factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
	abs := new(big.Int).Abs(total)
	whole, frac := new(big.Int).QuoRem(abs, factor, new(big.Int))
	text := whole.String()
	if precision > 0 {
		text += "." + fmt.Sprintf("%0*s", precision, frac.String())
	}
	if total.Sign() < 0 {
		text = "-" + text
	}
	return rows, counting.n, hex.EncodeToString(h.Sum(nil)), text, nil
}

type referenceCountingWriter struct {
	w io.Writer
	n int64
}

func (w *referenceCountingWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}

func (x *MultiApp) uploadReference(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxReferenceUpload+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		fail(w, 400, err)
		return
	}
	fields := map[string]string{}
	for _, key := range []string{"name", "replace", "base_sha256"} {
		part, e := reader.NextPart()
		if e != nil || part.FormName() != key || part.FileName() != "" {
			fail(w, 400, fmt.Errorf("expected form field %s", key))
			return
		}
		raw, e := io.ReadAll(io.LimitReader(part, 8193))
		_ = part.Close()
		if e != nil || len(raw) > 8192 {
			fail(w, 400, fmt.Errorf("invalid field %s", key))
			return
		}
		fields[key] = string(raw)
	}
	if fields["replace"] != "true" && fields["replace"] != "false" {
		fail(w, 422, errors.New("replace must be true or false"))
		return
	}
	dir, err := x.projectDir(fields["name"])
	if err != nil {
		fail(w, 404, err)
		return
	}
	precision, dims, err := projectReferenceContract(dir)
	if err != nil {
		fail(w, 422, err)
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "csv" || part.FileName() == "" || !strings.EqualFold(filepath.Ext(part.FileName()), ".csv") {
		fail(w, 400, errors.New("expected a reference .csv file"))
		return
	}
	tmp, err := os.CreateTemp(dir, ".reference-import-")
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer os.Remove(tmp.Name())
	rows, bytes, sha, total, err := writeReferenceCSV(r.Context().Done(), part, tmp, dims, precision)
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		fail(w, 422, err)
		return
	}
	extra, err := reader.NextPart()
	if err != io.EOF {
		if extra != nil {
			_ = extra.Close()
		}
		fail(w, 400, errors.New("unexpected extra form field"))
		return
	}
	if bytes > maxReferenceUpload {
		fail(w, 413, errors.New("reference file exceeds upload limit"))
		return
	}
	// Only the final replacement is synchronized; long browser uploads must
	// not block Studio job polling or other project operations.
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.busy[fields["name"]] {
		fail(w, 409, errors.New("project is executing; reference cannot be replaced now"))
		return
	}
	current, err := referenceFingerprint(dir)
	if err != nil {
		fail(w, 422, err)
		return
	}
	if current.Exists {
		if fields["replace"] != "true" || current.SHA256 != fields["base_sha256"] {
			fail(w, 409, errors.New("reference already exists or changed; confirm the latest version before replacing"))
			return
		}
	} else if fields["base_sha256"] != "" {
		fail(w, 409, errors.New("reference disappeared since preview; reload reference status"))
		return
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, "reference.csv")); err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 201, map[string]any{"saved": "reference.csv", "rows": rows, "bytes": bytes, "sha256": sha, "total": total, "precision": precision, "dimensions": dims})
}

type acceptanceSummaryItem struct {
	Name            string `json:"name"`
	Status          string `json:"status"`
	Matched         int64  `json:"matched"`
	Different       int64  `json:"different"`
	Missing         int64  `json:"missing"`
	Extra           int64  `json:"extra"`
	ReferenceSHA256 string `json:"reference_sha256"`
	ActualSHA256    string `json:"actual_sha256"`
}

func (x *MultiApp) acceptanceEvidence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	name, job := r.URL.Query().Get("name"), r.URL.Query().Get("job")
	if !multiSafe.MatchString(name) || !multiJobIDPattern.MatchString(job) {
		fail(w, 422, errors.New("invalid project/job"))
		return
	}
	if _, err := x.projectDir(name); err != nil {
		fail(w, 404, err)
		return
	}
	x.mu.Lock()
	cp, ok := x.jobs[job]
	var snapshot Job
	if ok {
		snapshot = *cp
	}
	x.mu.Unlock()
	if !ok {
		var err error
		snapshot, err = readJournal(filepath.Join(x.parent.data, "multi-jobs"), job, true)
		if err != nil {
			fail(w, 404, errors.New("job not found"))
			return
		}
	}
	if snapshot.Status != "PASS" || snapshot.Case != name {
		fail(w, 409, errors.New("not a successful job for this project"))
		return
	}
	jobDir := filepath.Join(x.parent.data, "multi-jobs", job)
	folder := r.URL.Query().Get("report")
	if folder == "" {
		if r.URL.Query().Get("download") != "" {
			fail(w, 422, errors.New("choose a reconciliation report first"))
			return
		}
		entries, err := os.ReadDir(jobDir)
		if err != nil {
			fail(w, 500, err)
			return
		}
		list := []acceptanceSummaryItem{}
		for _, entry := range entries {
			if !acceptanceDirectoryPattern.MatchString(entry.Name()) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			report, err := readAcceptanceReport(filepath.Join(jobDir, entry.Name()))
			if err != nil {
				continue
			}
			list = append(list, acceptanceSummaryItem{Name: entry.Name(), Status: report.Status, Matched: report.Matched, Different: report.Different, Missing: report.Missing, Extra: report.Extra, ActualSHA256: report.ActualSHA256, ReferenceSHA256: report.ReferenceSHA256})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Name > list[j].Name })
		if len(list) > 100 {
			list = list[:100]
		}
		jsonOut(w, 200, map[string]any{"reports": list, "note": "Historical snapshot reports, not validation of the current reference file"})
		return
	}
	if !acceptanceDirectoryPattern.MatchString(folder) {
		fail(w, 422, errors.New("invalid acceptance report identifier"))
		return
	}
	full := filepath.Join(jobDir, folder)
	st, err := os.Lstat(full)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		fail(w, 404, errors.New("acceptance report not found"))
		return
	}
	if r.URL.Query().Get("download") != "" {
		if r.URL.Query().Get("download") != "differences" {
			fail(w, 422, errors.New("only differences.csv is downloadable"))
			return
		}
		file := filepath.Join(full, "differences.csv")
		st, err := os.Lstat(file)
		if err != nil || !st.Mode().IsRegular() {
			fail(w, 404, errors.New("difference report is unavailable"))
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="differences.csv"`)
		http.ServeFile(w, r, file)
		return
	}
	report, err := readAcceptanceReport(full)
	if err != nil {
		fail(w, 422, err)
		return
	}
	jsonOut(w, 200, map[string]any{"report": report, "name": folder, "historical": true})
}

func readAcceptanceReport(dir string) (acceptance.Report, error) {
	var result acceptance.Report
	path := filepath.Join(dir, "summary.json")
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 4<<20 {
		return result, errors.New("invalid or missing report summary")
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(bytes, &result); err != nil {
		return result, err
	}
	if result.Status != "MATCH" && result.Status != "DIFFERENCES" {
		return result, errors.New("invalid report status")
	}
	return result, nil
}
