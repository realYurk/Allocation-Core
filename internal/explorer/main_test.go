package explorer

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func fixture(t *testing.T, name, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if e := os.WriteFile(p, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestBedrockPostingGrainAndIgnoreDimension(t *testing.T) {
	input := fixture(t, "source.txt", "Example,2026-01,Team-A,No Model,No Line,C01,Base,20.10\nExample,2026-01,Team-A,No Model,No Line,C01,Other,-2.10\nExample,2026-01,Team-A,M1,Item-1,C01,Base,0.00\n")
	output := filepath.Join(t.TempDir(), "normalized.csv")
	spec := ImportSpec{Format: "bedrock", CubeColumn: true, Dimensions: []string{"period", "site", "model", "item", "metric", "source_layer"}, IgnoreDimensions: []string{"source_layer"}}
	result, err := Normalize(input, output, spec)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 3 {
		t.Fatal(result)
	}
	b, _ := os.ReadFile(output)
	if strings.Contains(string(b), "Example") || strings.Contains(string(b), "source_layer") {
		t.Fatalf("mapping leaked excluded columns: %s", b)
	}
	if !strings.Contains(string(b), "No Model,No Line") || !strings.Contains(string(b), "-2.10") {
		t.Fatalf("posting-grain member lost: %s", b)
	}
}
func TestBedrockTitleAndTargetFilter(t *testing.T) {
	input := fixture(t, "export.csv", "cube,period,model,item,source_layer,amount\nExample,2026-01,M1,I1,Publish,3.25\nExample,2026-01,M1,I1,Unpublished,900.00\n")
	output := filepath.Join(t.TempDir(), "normal.csv")
	spec := ImportSpec{Format: "bedrock", HeaderRows: 1, CubeColumn: true, Dimensions: []string{"period", "model", "item", "source_layer"}, IgnoreDimensions: []string{"source_layer"}, FilterDimension: "source_layer", FilterValue: "Publish"}
	r, e := Normalize(input, output, spec)
	if e != nil {
		t.Fatal(e)
	}
	if r.Rows != 1 || r.Skipped != 1 {
		t.Fatalf("unexpected %v", r)
	}
	b, _ := os.ReadFile(output)
	if strings.Contains(string(b), "900") || !strings.Contains(string(b), "3.25") {
		t.Fatal(string(b))
	}
}
func TestInvalidDataNoPublishedFile(t *testing.T) {
	input := fixture(t, "bad.csv", "x,y,value\na,b,invalid\n")
	out := filepath.Join(t.TempDir(), "out.csv")
	_, e := Normalize(input, out, ImportSpec{Format: "header"})
	if e == nil {
		t.Fatal("expected invalid decimal rejected")
	}
	if _, e := os.Stat(out); !os.IsNotExist(e) {
		t.Fatal("invalid file became visible")
	}
}
func TestAggregatedPivotAndWrongDestination(t *testing.T) {
	dir := t.TempDir()
	a, e := newApp(dir)
	if e != nil {
		t.Fatal(e)
	}
	before := "period,metric,model,item,value\n2026-01,C01,No Model,No Line,6.25\n2026-01,C01,No Model,No Line,3.75\n"
	ref := "period,metric,model,item,value\n2026-01,C01,M1,I1,6.00\n2026-01,C01,M1,I2,4.00\n"
	eng := "period,metric,model,item,value\n2026-01,C01,M1,I1,7.00\n2026-01,C01,M1,I2,3.00\n"
	for slot, body := range map[string]string{"before": before, "after": ref, "engine": eng} {
		p := filepath.Join(dir, slot+".csv")
		if e = os.WriteFile(p, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		a.set(slot, p)
	}
	req := httptest.NewRequest("GET", "/api/view?row=model&column=item&filters=%7B%22period%22%3A%222026-01%22%7D", nil)
	w := httptest.NewRecorder()
	a.view(w, req)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var j struct {
		GrandTotal map[string]any `json:"grand_total"`
		Rows       []struct {
			Name  string           `json:"name"`
			Cells []map[string]any `json:"cells"`
		} `json:"rows"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &j); e != nil {
		t.Fatal(e)
	}
	if j.GrandTotal["before"] != "10.000000" || j.GrandTotal["after"] != "10.000000" || j.GrandTotal["engine"] != "10.000000" {
		t.Fatal(j.GrandTotal)
	}
	if len(j.Rows) != 2 || j.Rows[1].Cells[0]["engine_minus_after"] == "0.000000" {
		t.Fatal("wrong destination not detected")
	}
}
func TestUploadCSRFAndBadDataNotPublished(t *testing.T) {
	a, e := newApp(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	makeReq := func(src string) *http.Request {
		body := new(bytes.Buffer)
		w := multipart.NewWriter(body)
		w.WriteField("spec", `{"format":"header","value_column":"value"}`)
		p, _ := w.CreateFormFile("file", "data.csv")
		io.Copy(p, strings.NewReader(src))
		w.Close()
		req := httptest.NewRequest("POST", "/api/upload?slot=before", body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		return req
	}
	r := makeReq("a,value\nx,1\n")
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("expected csrf denied: %d", w.Code)
	}
	r = makeReq("a,value\nx,1\n")
	r.Header.Set("X-Local-Token", a.csrf)
	w = httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("upload error %d %s", w.Code, w.Body.String())
	}
	previous, _ := os.ReadFile(a.get("before"))
	r = makeReq("a,value\nx,NaN\n")
	r.Header.Set("X-Local-Token", a.csrf)
	w = httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 422 {
		t.Fatalf("expected validation failure %d %s", w.Code, w.Body.String())
	}
	now, _ := os.ReadFile(a.get("before"))
	if !bytes.Equal(previous, now) {
		t.Fatal("failure overwrote published input")
	}
}
func TestTitleHeadersDuplicateReject(t *testing.T) {
	p := fixture(t, "bad.csv", "a,a,value\na,b,1.00\n")
	_, e := Normalize(p, filepath.Join(t.TempDir(), "out.csv"), ImportSpec{Format: "header"})
	if e == nil {
		t.Fatal("duplicate header accepted")
	}
}
func TestCSVRead(t *testing.T) {
	p := fixture(t, "t.csv", "period,model,value\n2026,M1,1.5\n")
	f, _ := os.Open(p)
	defer f.Close()
	r := csv.NewReader(f)
	h, _ := r.Read()
	if len(h) != 3 {
		t.Fatal(h)
	}
}

func TestPageHasUsableSessionToken(t *testing.T) {
	a, e := newApp(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("page status %d", w.Code)
	}
	m := regexp.MustCompile(`const token=(.*?);`).FindStringSubmatch(w.Body.String())
	if len(m) != 2 {
		t.Fatal("missing client token")
	}
	token, e := strconv.Unquote(m[1])
	if e != nil {
		t.Fatalf("token not JavaScript JSON string: %q: %v", m[1], e)
	}
	if token != a.csrf {
		t.Fatal("browser receives different CSRF token")
	}
}
func TestBedrockMultiCubeReject(t *testing.T) {
	src := fixture(t, "two.csv", "Cube-1,2026,A,1.00\nCube-2,2026,A,3.00\n")
	_, e := Normalize(src, filepath.Join(t.TempDir(), "out.csv"), ImportSpec{Format: "bedrock", CubeColumn: true, Dimensions: []string{"period", "item"}})
	if e == nil {
		t.Fatal("mixed source cubes must not be silently merged")
	}
}
func TestEqualGrandTotalWrongDistributionFlag(t *testing.T) {
	a, e := newApp(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	for slot, body := range map[string]string{"after": "dimension,member,value\nX,A,6\nX,B,4\n", "engine": "dimension,member,value\nX,A,7\nX,B,3\n"} {
		p := filepath.Join(a.path, slot+".csv")
		if e = os.WriteFile(p, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		a.set(slot, p)
	}
	w := httptest.NewRecorder()
	a.view(w, httptest.NewRequest("GET", "/api/view?row=dimension&column=member", nil))
	if w.Code != 200 {
		t.Fatalf("error %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		DifferenceCells int            `json:"difference_cells"`
		GrandTotal      map[string]any `json:"grand_total"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if out.GrandTotal["engine_minus_after"] != "0.000000" || out.DifferenceCells != 2 {
		t.Fatalf("did not flag offsetting misallocation: %+v", out)
	}
}
