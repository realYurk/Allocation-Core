package studio

import (
	"allocationcore/internal/multi"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func onboardingApp(t *testing.T) (*MultiApp, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "multi")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	src := filepath.Join(root, "original")
	if e := copyFixture(filepath.Join("..", "..", "testdata", "multi_sample"), src); e != nil {
		t.Fatal(e)
	}
	return &MultiApp{root: root, mu: sync.Mutex{}, busy: map[string]bool{}}, src
}

func onboardRequest(t *testing.T, x *MultiApp, name, source, tpl, filename, content string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	for _, field := range [][2]string{{"name", name}, {"source", source}, {"template", tpl}} {
		if e := form.WriteField(field[0], field[1]); e != nil {
			t.Fatal(e)
		}
	}
	part, e := form.CreateFormFile("csv", filename)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = part.Write([]byte(content)); e != nil {
		t.Fatal(e)
	}
	if e = form.Close(); e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/multi/onboard", &buf)
	req.Header.Set("Content-Type", form.FormDataContentType())
	rec := httptest.NewRecorder()
	x.createFromAmountsCSV(rec, req)
	return rec
}

func TestOnboardingCreatesIndependentBusinessProject(t *testing.T) {
	x, source := onboardingApp(t)
	initial, e := os.ReadFile(filepath.Join(source, "assignments.csv"))
	if e != nil {
		t.Fatal(e)
	}
	input := "\ufeffperiod,department,sku,account,currency,amount,row_id\n" +
		"2026-01,SharedPool,Unassigned,New-A,CUR,12.25,one\n" +
		"2026-01,SharedPool,Unassigned,New-B,CUR,13.75,two\n" +
		"2026-01,SharedPool,Unassigned,New-A,CUR,-2.00,three\n"
	rec := onboardRequest(t, x, "new_business", "original", "material.json", "source.csv", input)
	if rec.Code != 201 {
		t.Fatalf("expected 201 got %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Project string         `json:"project"`
		Import  onboardPreview `json:"import"`
	}
	if e := json.Unmarshal(rec.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if out.Project != "new_business" || out.Import.Accounts != 2 || out.Import.Rows != 3 || out.Import.Total != "24.00" || out.Import.Bytes != int64(len(input)) {
		t.Fatalf("bad summary: %+v", out)
	}
	dest := filepath.Join(x.root, "new_business")
	amounts, e := os.ReadFile(filepath.Join(dest, "amounts.csv"))
	if e != nil {
		t.Fatal(e)
	}
	if string(amounts) != input {
		t.Fatal("amount data altered")
	}
	assignments, e := readAssignments(filepath.Join(dest, "assignments.csv"))
	if e != nil {
		t.Fatal(e)
	}
	if len(assignments) != 2 || assignments[0]["account"] != "New-A" || assignments[1]["account"] != "New-B" {
		t.Fatalf("wrong assignments: %v", assignments)
	}
	after, e := os.ReadFile(filepath.Join(source, "assignments.csv"))
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(initial, after) {
		t.Fatal("source project was changed")
	}
	check := multi.InspectProject(context.Background(), dest)
	if check.Status != "PASS" || check.Accounts != 2 || check.TotalRows != 3 {
		t.Fatalf("imported project not operable: %+v", check)
	}
	if !strings.Contains(rec.Body.String(), "must be checked") {
		t.Fatal("response must warn input != financial correctness")
	}
}

func TestOnboardingRejectsInvalidDataAndDoesNotPublish(t *testing.T) {
	for name, input := range map[string]string{
		"no_amount":        "period,department,sku,account,currency\n2026-01,SharedPool,Unassigned,X,CUR\n",
		"wrong_decimal":    "period,department,sku,account,currency,amount\n2026-01,SharedPool,Unassigned,X,CUR,12.345\n",
		"empty_coordinate": "period,department,sku,account,currency,amount\n2026-01,SharedPool,,X,CUR,12.00\n",
		"broken_csv":       "period,department,sku,account,currency,amount\n\"unterminated",
	} {
		t.Run(name, func(t *testing.T) {
			x, _ := onboardingApp(t)
			rec := onboardRequest(t, x, "fresh", "original", "dual.json", "source.csv", input)
			if rec.Code != 422 {
				t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
			}
			if _, err := os.Lstat(filepath.Join(x.root, "fresh")); !os.IsNotExist(err) {
				t.Fatal("rejected data created a project")
			}
		})
	}
}
func TestOnboardingDoesNotOverwriteExistingProject(t *testing.T) {
	x, src := onboardingApp(t)
	before, e := os.ReadFile(filepath.Join(src, "amounts.csv"))
	if e != nil {
		t.Fatal(e)
	}
	input := "period,department,sku,account,currency,amount\n2026-01,SharedPool,Unassigned,A,CUR,1.00\n"
	rec := onboardRequest(t, x, "original", "original", "material.json", "a.csv", input)
	if rec.Code != 422 {
		t.Fatalf("expected 422 got %d: %s", rec.Code, rec.Body.String())
	}
	after, e := os.ReadFile(filepath.Join(src, "amounts.csv"))
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("source amounts were overwritten")
	}
	rec = onboardRequest(t, x, "fresh", "original", "material.json", "a.csv", input)
	if rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = onboardRequest(t, x, "fresh", "original", "material.json", "a.csv", input)
	if rec.Code != 409 {
		t.Fatalf("existing destination was overwritten: %d", rec.Code)
	}
}
func TestOnboardingStreamsBeyondLegacyFiveMiBLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("moderately large streaming input")
	}
	x, _ := onboardingApp(t)
	var b strings.Builder
	b.WriteString("period,department,sku,account,currency,amount,row_id\n")
	for i := 0; i < 105000; i++ {
		fmt.Fprintf(&b, "2026-01,SharedPool,Unassigned,Large,CUR,0.01,row%06d-paddingdata\n", i)
	}
	if b.Len() <= 5<<20 {
		t.Fatalf("fixture too small: %d", b.Len())
	}
	rec := onboardRequest(t, x, "large_import", "original", "revenue.json", "large.csv", b.String())
	if rec.Code != 201 {
		t.Fatalf("large stream failed: %d %s", rec.Code, rec.Body.String())
	}
	var v struct {
		Import onboardPreview `json:"import"`
	}
	if e := json.Unmarshal(rec.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	if v.Import.Rows != 105000 || v.Import.Accounts != 1 || v.Import.Total != "1050.00" {
		t.Fatalf("wrong large import: %+v", v)
	}
}
