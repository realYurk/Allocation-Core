package studio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func referenceTestPost(t *testing.T, x *MultiApp, reference string, replace bool, prior string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	for _, field := range [][2]string{{"name", "financial"}, {"replace", fmt.Sprint(replace)}, {"base_sha256", prior}} {
		if err := form.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	part, err := form.CreateFormFile("csv", "independent_reference.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte(reference)); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/multi/reference-upload", &buf)
	req.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	x.uploadReference(w, req)
	return w
}

func TestReferenceUploadValidationAndSafeReplacement(t *testing.T) {
	x, dir, _, _ := designerBridgeFixture(t)
	csv := "sku,account,currency,period,department,value\nProduct-A,Account-X,CUR,2026-01,Shared,6.00\nProduct-B,Account-X,CUR,2026-01,Shared,14.00\n"
	before, err := os.ReadFile(filepath.Join(dir, "amounts.csv"))
	if err != nil {
		t.Fatal(err)
	}
	w := referenceTestPost(t, x, csv, false, "")
	if w.Code != 201 {
		t.Fatalf("upload failed %d: %s", w.Code, w.Body.String())
	}
	var result struct {
		Rows  int64  `json:"rows"`
		Total string `json:"total"`
		SHA   string `json:"sha256"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Rows != 2 || result.Total != "20.00" || len(result.SHA) != 64 {
		t.Fatalf("wrong summary: %+v", result)
	}
	info := httptest.NewRecorder()
	x.referenceStatus(info, httptest.NewRequest(http.MethodGet, "/api/multi/reference?name=financial", nil))
	if info.Code != 200 || !strings.Contains(info.Body.String(), result.SHA) {
		t.Fatal(info.Body.String())
	}