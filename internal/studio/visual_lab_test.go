package studio

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func labDefault(example string) labRequest {
	return labRequest{Example: example, Amount: "1000000.00", Amount2: "500000.00", Weights: []string{"3", "2", "1", "3", "2", "3"}, Targets: []bool{true, true}}
}
func callLabDemo(t *testing.T, req labRequest) (int, labReport, string) {
	t.Helper()
	b, _ := json.Marshal(req)
	w := httptest.NewRecorder()
	new(MultiApp).labDemo(w, httptest.NewRequest(http.MethodPost, "/api/lab/demo", bytes.NewReader(b)))
	var report labReport
	_ = json.Unmarshal(w.Body.Bytes(), &report)
	return w.Code, report, w.Body.String()
}

func TestVisualLabCoreHandCalculatedExamples(t *testing.T) {
	for _, example := range []string{"A", "B", "C"} {
		t.Run(example, func(t *testing.T) {
			req := labDefault(example)
			if example == "B" {
				req.Amount = "1200000.00"
			}
			if example == "C" {
				req.Weights[3] = "4"
			}
			code, r, body := callLabDemo(t, req)
			if code != 200 {
				t.Fatalf("%d %s", code, body)
			}
			expected := map[string]string{}
			switch example {
			case "A":
				expected = map[string]string{"E1/车型 A/未分配": "600000.00", "E1/车型 B/未分配": "400000.00"}
			case "B":
				expected = map[string]string{"E1/车型 A/A1": "180000.00", "E1/车型 A/A2": "540000.00", "E1/车型 B/B1": "192000.00", "E1/车型 B/B2": "288000.00"}
			case "C":
				expected = map[string]string{"E1/车型 A/未分配": "600000.00", "E1/车型 B/未分配": "400000.00", "E2/车型 A/未分配": "100000.00", "E2/车型 B/未分配": "400000.00"}
			}
			if len(r.After.Rows) != len(expected) {
				t.Fatalf("wrong count: %s", body)
			}
			for _, row := range r.After.Rows {
				k := row["account"] + "/" + row["model"] + "/" + row["sku"]
				if row["amount"] != expected[k] {
					t.Fatalf("%s: %s != %s", k, row["amount"], expected[k])
				}
			}
			if r.Difference != "0.00" || r.Input != r.Output || r.Checks["reference"] == "PASS" || !strings.HasPrefix(r.Checks["publication"], "UNPUBLISHED") {
				t.Fatal(body)
			}
			if example == "B" && (r.Output != "1200000.00" || len(r.Stages) != 2) {
				t.Fatal("intermediate money counted twice")
			}
		})
	}
}
func TestVisualLabErrorsPrecisionNegativeAndTargets(t *testing.T) {
	for _, fault := range []string{"zero", "target"} {
		req := labDefault("D")
		req.Fault = fault
		code, _, body := callLabDemo(t, req)
		if code != 422 {
			t.Fatalf("%s should BLOCK: %d %s", fault, code, body)
		}
	}
	scopeReq := labDefault("D")
	scopeReq.Fault = "scope"
	scopeCode, scopeReport, scopeBody := callLabDemo(t, scopeReq)
	if scopeCode != 200 || scopeReport.Status != "PARTIAL" || scopeReport.After.Rows[0]["model"] != "未分配" {
		t.Fatal(scopeBody)
	}
	req := labDefault("A")
	req.Amount = "1.001"
	if code, _, _ := callLabDemo(t, req); code != 422 {
		t.Fatal("implicit rounding")
	}
	req = labDefault("A")
	req.Amount = "-100.01"
	code, r, body := callLabDemo(t, req)
	if code != 200 || r.Output != "-100.01" || r.After.Count != 2 {
		t.Fatal(body)
	}
	req = labDefault("A")
	req.Targets = []bool{true, false}
	code, r, body = callLabDemo(t, req)
	if code != 200 || len(r.After.Rows) != 1 || r.After.Rows[0]["amount"] != "1000000.00" {
		t.Fatal(body)
	}
	req.Targets = []bool{false, false}
	if code, _, _ := callLabDemo(t, req); code != 422 {
		t.Fatal("empty targets accepted")
	}
}
func TestVisualLabBoundedPaginationAndExactAggregation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.csv")
	rows := [][]string{{"account", "model", "amount"}}
	for i := 0; i < 10003; i++ {
		rows = append(rows, []string{"E1", fmt.Sprintf("M%d", i%20), fmt.Sprintf("%d.01", i)})
	}
	if e := labWriteCSV(path, rows); e != nil {
		t.Fatal(e)
	}
	q := labQuery{Group: "model", Filters: map[string]string{"account": "E1"}, Offset: 50, Limit: 50}
	r, e := scanLabCSV(httptest.NewRequest("GET", "/", nil), path, 2, []string{"account", "model"}, q)
	if e != nil {
		t.Fatal(e)
	}
	if r.Count != 10003 || len(r.Rows) != 50 || r.Rows[0]["amount"] != "9952.01" || len(r.Groups) != 13 || r.Total != "50025103.03" {
		t.Fatalf("bad page or aggregation: %+v", r)
	}
	q.Sort = "asc"
	r, e = scanLabCSV(httptest.NewRequest("GET", "/", nil), path, 2, []string{"account", "model"}, q)
	if e != nil || r.Rows[0]["amount"] != "50.01" {
		t.Fatal(r, e)
	}
}
func TestVisualLabHistoricalSnapshotAndIntegrity(t *testing.T) {
	x, id, output := stageEvidenceFixture(t, true)
	path := "/api/lab/run?name=finance&job=" + id + "&account=Cost-C&group=sku"
	call := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		x.labRun(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	w := call(path)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var report labReport
	if e := json.Unmarshal(w.Body.Bytes(), &report); e != nil {
		t.Fatal(e)
	}
	if report.Input != "600.00" || report.Output != "600.00" || len(report.Stages) != 2 || report.Mode != "HISTORICAL" || !strings.HasPrefix(report.Checks["scope"], "NOT_TESTED") {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), output) {
		t.Fatal("private path exposed")
	}
	if e := os.WriteFile(filepath.Join(x.root, "finance", "amounts.csv"), []byte("unused changed current input"), 0600); e != nil {
		t.Fatal(e)
	}
	if w2 := call(path); w2.Body.String() != w.Body.String() {
		t.Fatal("mutable project changed historical view")
	}
	if got := call(path + "&filters=%7B%22missing%22%3A%22x%22%7D"); got.Code != 400 {
		t.Fatal("unknown filter accepted")
	}
	if got := call(path + "&offset=10001"); got.Code != 400 {
		t.Fatal("unbounded page accepted")
	}
	dirs, e := os.ReadDir(filepath.Join(output, "groups"))
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range dirs {
		p := filepath.Join(output, "groups", d.Name(), "output", "results.csv")
		f, e := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = f.WriteString("\n")
		f.Close()
	}
	if got := call(path); got.Code != 422 || !strings.Contains(got.Body.String(), "清单") {
		t.Fatal("tampered result accepted", got.Code, got.Body.String())
	}
}
func TestVisualLabRoutesAndEmbeddedAssets(t *testing.T) {
	mux := http.NewServeMux()
	a := &Application{data: t.TempDir()}
	a.registerMulti(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/lab", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/assets/lab.js") {
		t.Fatal(w.Code)
	}
	for _, name := range []string{"public/lab.js", "public/lab.css"} {
		if _, e := publicFS.ReadFile(name); e != nil {
			t.Fatal(e)
		}
	}
}

func TestVisualLabMillionRowsBoundedResponse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "million-synthetic.csv")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	w := bufio.NewWriter(f)
	_, _ = w.WriteString("account,model,currency,amount\n")
	for i := 0; i < 1000000; i++ {
		if _, e = w.WriteString("E1,车型A,CNY,1.01\n"); e != nil {
			t.Fatal(e)
		}
	}
	if e = w.Flush(); e != nil {
		t.Fatal(e)
	}
	f.Close()
	start := time.Now()
	r, e := scanLabCSV(httptest.NewRequest("GET", "/", nil), path, 2, []string{"account", "model", "currency"}, labQuery{Group: "model", Limit: 50, Filters: map[string]string{}})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(r)
	if r.Count != 1000000 || r.Total != "1010000.00" || len(r.Rows) != 50 || len(r.Groups) != 1 || len(raw) > 16384 {
		t.Fatalf("unbounded or incorrect response: rows %d bytes %d total %s", r.Count, len(raw), r.Total)
	}
	t.Logf("1,000,000 synthetic rows: %s; response %d bytes, %d detail rows", time.Since(start), len(raw), len(r.Rows))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = scanLabCSV(httptest.NewRequest("GET", "/", nil).WithContext(ctx), path, 2, []string{"account", "model", "currency"}, labQuery{Group: "model", Limit: 50})
	if e != context.Canceled {
		t.Fatal("canceled scan continued", e)
	}
}

func TestVisualLabCurrencyScopeAndTargetSamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed.csv")
	if e := labWriteCSV(path, [][]string{{"account", "currency", "amount"}, {"E1", "CNY", "1.00"}, {"E1", "USD", "1.00"}}); e != nil {
		t.Fatal(e)
	}
	q := labQuery{Group: "currency", Limit: 50}
	r, e := scanLabCSV(httptest.NewRequest("GET", "/", nil), path, 2, []string{"account", "currency"}, q)
	if e != nil || !r.MixedCurrency || !r.FileMixedCurrency {
		t.Fatal(r, e)
	}
	q.Filters = map[string]string{"currency": "CNY"}
	r, e = scanLabCSV(httptest.NewRequest("GET", "/", nil), path, 2, []string{"account", "currency"}, q)
	if e != nil || r.MixedCurrency || !r.FileMixedCurrency || r.Total != "1.00" {
		t.Fatal(r, e)
	}
}
