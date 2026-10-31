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
	w = referenceTestPost(t, x, csv, false, "")
	if w.Code != 409 {
		t.Fatalf("overwrite without consent allowed: %d", w.Code)
	}
	w = referenceTestPost(t, x, strings.Replace(csv, "6.00", "7.00", 1), true, "stale")
	if w.Code != 409 {
		t.Fatalf("stale reference overwritten: %d", w.Code)
	}
	changed := strings.Replace(csv, "6.00", "7.00", 1)
	w = referenceTestPost(t, x, changed, true, result.SHA)
	if w.Code != 201 {
		t.Fatalf("explicit replacement failed: %d: %s", w.Code, w.Body.String())
	}
	actual, err := os.ReadFile(filepath.Join(dir, "reference.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != changed {
		t.Fatal("reference bytes changed or wrong replacement")
	}
	after, err := os.ReadFile(filepath.Join(dir, "amounts.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("reference upload changed input amounts")
	}
}

func TestReferenceUploadRejectsBadInputsWithoutPublishing(t *testing.T) {
	x, dir, _, _ := designerBridgeFixture(t)
	valid := "account,period,department,sku,currency,amount\nAccount-X,2026-01,Shared,Product-A,CUR,20.00\n"
	cases := map[string]string{
		"no_account":            "period,department,sku,currency,amount\n2026-01,Shared,Product-A,CUR,20.00\n",
		"rounded":               strings.Replace(valid, "20.00", "20.001", 1),
		"invalid_amount":        strings.Replace(valid, "20.00", "NaN", 1),
		"empty_coordinate":      strings.Replace(valid, "Product-A", "", 1),
		"unexpected_coordinate": strings.Replace(valid, "currency,amount", "currency,brand,amount", 1),
		"wrong_width":           strings.Replace(valid, ",CUR,20.00", ",20.00", 1),
		"broken_quotation":      valid + "\"bad\n",
		"header_duplicate":      strings.Replace(valid, "department,sku", "department,department", 1),
		"empty_csv":             "account,period,department,sku,currency,amount\n",
	}
	for name, csv := range cases {
		t.Run(name, func(t *testing.T) {
			w := referenceTestPost(t, x, csv, false, "")
			if w.Code < 400 {
				t.Fatalf("bad CSV accepted %d: %s", w.Code, w.Body.String())
			}
			if _, err := os.Stat(filepath.Join(dir, "reference.csv")); !os.IsNotExist(err) {
				t.Fatalf("invalid CSV published: %v", err)
			}
		})
	}
	w := referenceTestPost(t, x, valid, false, "")
	if w.Code != 201 {
		t.Fatalf("valid CSV rejected: %d %s", w.Code, w.Body.String())
	}
	var d struct {
		SHA string `json:"sha256"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	x.busy["financial"] = true
	w = referenceTestPost(t, x, valid, true, d.SHA)
	if w.Code != 409 {
		t.Fatalf("active job reference replaced %d", w.Code)
	}
	x.busy["financial"] = false
	matches, err := filepath.Glob(filepath.Join(dir, ".reference-import-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files leaked %v %v", matches, err)
	}
}

func TestAcceptanceHistoryAndDownloadIsolation(t *testing.T) {
	x, project, _, _ := designerBridgeFixture(t)
	jobID := strings.Repeat("b", 24)
	dir := filepath.Join(x.parent.data, "multi-jobs", jobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// The production result includes provenance columns excluded by the
	// authoritative coordinate comparator; reference columns may be reordered.
	actual := "terminal_stage,row_id,period,department,sku,account,currency,amount\nS,1,2026-01,Shared,Product-A,Account-X,CUR,6.00\nS,2,2026-01,Shared,Product-B,Account-X,CUR,14.00\n"
	ref := "account,sku,department,period,currency,value\nAccount-X,Product-A,Shared,2026-01,CUR,7.00\nAccount-X,Product-B,Shared,2026-01,CUR,13.00\n"
	if err := os.WriteFile(filepath.Join(dir, "results.csv"), []byte(actual), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "reference.csv"), []byte(ref), 0600); err != nil {
		t.Fatal(err)
	}
	x.jobs = map[string]*Job{jobID: {ID: jobID, Case: "financial", Status: "PASS"}}
	post := httptest.NewRecorder()
	x.reconcile(post, httptest.NewRequest(http.MethodPost, "/api/multi/reconcile", strings.NewReader(`{"name":"financial","job":"`+jobID+`","precision":2,"tolerance":"0"}`)))
	if post.Code != 200 {
		t.Fatalf("reconcile %d %s", post.Code, post.Body.String())
	}
	query := "/api/multi/acceptance?name=financial&job=" + jobID
	list := httptest.NewRecorder()
	x.acceptanceEvidence(list, httptest.NewRequest(http.MethodGet, query, nil))
	if list.Code != 200 {
		t.Fatalf("history %d %s", list.Code, list.Body.String())
	}
	var index struct {
		Reports []acceptanceSummaryItem `json:"reports"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &index); err != nil {
		t.Fatal(err)
	}
	if len(index.Reports) != 1 || index.Reports[0].Different != 2 || index.Reports[0].Status != "DIFFERENCES" {
		t.Fatalf("wrong report listing %+v", index)
	}
	folder := index.Reports[0].Name
	get := httptest.NewRecorder()
	x.acceptanceEvidence(get, httptest.NewRequest(http.MethodGet, query+"&report="+folder, nil))
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"account":"Account-X"`) {
		t.Fatalf("detail %d %s", get.Code, get.Body.String())
	}
	download := httptest.NewRecorder()
	x.acceptanceEvidence(download, httptest.NewRequest(http.MethodGet, query+"&report="+folder+"&download=differences", nil))
	if download.Code != 200 || !strings.Contains(download.Body.String(), "Account-X") || !strings.Contains(download.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download %d %s", download.Code, download.Body.String())
	}
	for _, bad := range []string{"&report=..", "&report=%2e%2e%2fprivate", "&report=" + folder + "&download=results"} {
		w := httptest.NewRecorder()
		x.acceptanceEvidence(w, httptest.NewRequest(http.MethodGet, query+bad, nil))
		if w.Code < 400 {
			t.Fatalf("invalid report path accepted: %s", bad)
		}
	}
	cross := httptest.NewRecorder()
	x.acceptanceEvidence(cross, httptest.NewRequest(http.MethodGet, "/api/multi/acceptance?name=other&job="+jobID, nil))
	if cross.Code < 400 {
		t.Fatal("cross-project evidence disclosure")
	}
	x.jobs[jobID].Status = "FAILED"
	blocked := httptest.NewRecorder()
	x.acceptanceEvidence(blocked, httptest.NewRequest(http.MethodGet, query+"&report="+folder, nil))
	if blocked.Code < 400 {
		t.Fatal("failed job evidence accepted")
	}
}

func TestReferenceUploadSupportsFilesLargerThanOldFiveMegabyteLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("large multipart payload")
	}
	x, _, _, _ := designerBridgeFixture(t)
	line := "Account-X,2026-01,Shared,Product-A,CUR,0.01\n"
	csv := "account,period,department,sku,currency,amount\n" + strings.Repeat(line, 125000)
	if len(csv) <= 5<<20 {
		t.Fatal("test must exceed the legacy 5 MiB limit")
	}
	w := referenceTestPost(t, x, csv, false, "")
	if w.Code != 201 {
		t.Fatalf("large reference rejected %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Rows  int64  `json:"rows"`
		Total string `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Rows != 125000 || result.Total != "1250.00" {
		t.Fatalf("bad financial sum %+v", result)
	}
}

func TestLegacyCSVUploadCannotBypassReferenceOrAssignmentChecks(t *testing.T) {
	x, dir, _, _ := designerBridgeFixture(t)
	for _, name := range []string{"reference.csv", "assignments.csv"} {
		t.Run(name, func(t *testing.T) {
			before, err := os.ReadFile(filepath.Join(dir, "assignments.csv"))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]string{"name": "financial", "file": name, "content": "account,template\nCost-X,sample.json\n"})
			w := httptest.NewRecorder()
			x.upload(w, httptest.NewRequest(http.MethodPost, "/api/multi/upload", bytes.NewReader(body)))
			if w.Code != 422 {
				t.Fatalf("legacy upload accepted protected file %s: %d %s", name, w.Code, w.Body.String())
			}
			after, err := os.ReadFile(filepath.Join(dir, "assignments.csv"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("legacy upload changed protected assignments")
			}
			if _, err := os.Stat(filepath.Join(dir, "reference.csv")); !os.IsNotExist(err) {
				t.Fatalf("legacy upload changed protected reference: %v", err)
			}
		})
	}
}
