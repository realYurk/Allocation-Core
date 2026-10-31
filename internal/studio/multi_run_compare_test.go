package studio

import (
	"allocationcore/internal/multi"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func callRunCompare(x *MultiApp, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	x.compareRuns(w, req)
	return w
}
func TestRunComparePublishedJobsSourceRulesAndCoordinateDelta(t *testing.T) {
	x, first, _ := stageEvidenceFixture(t)
	project := filepath.Join(x.root, "finance")
	// The reference job remains immutable; change both amount and driver inputs.
	amounts := filepath.Join(project, "amounts.csv")
	data, e := os.ReadFile(amounts)
	if e != nil {
		t.Fatal(e)
	}
	changed := bytes.Replace(data, []byte("Cost-C,CUR,600.00"), []byte("Cost-C,CUR,601.00"), 1)
	if bytes.Equal(changed, data) {
		t.Fatal("fixture amount not replaced")
	}
	if e = os.WriteFile(amounts, changed, 0600); e != nil {
		t.Fatal(e)
	}
	material := filepath.Join(project, "drivers", "material.csv")
	db, e := os.ReadFile(material)
	if e != nil {
		t.Fatal(e)
	}
	newDriver := bytes.Replace(db, []byte("Item-A,8"), []byte("Item-A,7"), 1)
	if bytes.Equal(newDriver, db) {
		t.Fatal("fixture driver not replaced")
	}
	if e = os.WriteFile(material, newDriver, 0600); e != nil {
		t.Fatal(e)
	}
	// Make a legitimate effective-rule override change as well.
	overrides := filepath.Join(project, "overrides.csv")
	raw, e := os.ReadFile(overrides)
	if e != nil {
		t.Fatal(e)
	}
	raw = bytes.ReplaceAll(raw, []byte("0.50"), []byte("0.40"))
	// 40%+40% is invalid; keep conservative 40%+60%.
	raw = bytes.Replace(raw, []byte("revenue,,,0.40"), []byte("revenue,,,0.60"), 1)
	if e = os.WriteFile(overrides, raw, 0600); e != nil {
		t.Fatal(e)
	}
	second := strings.Repeat("b", 24)
	path := filepath.Join(x.parent.data, "multi-jobs", second)
	engineBinary := filepath.Join(x.parent.data, "core")
	// stageEvidenceFixture's generated authoritative Core binary is shared by
	// its first run but only its on-disk result is returned. Build a new one.
	engineBinary = buildRunCompareEngine(t, x.parent.data)
	result, e := multi.Run(project, path, engineBinary)
	if e != nil || result.Status != "PASS" {
		t.Fatalf("second run: %+v %v", result, e)
	}
	j := &Job{ID: second, Case: "finance", Status: "PASS", Start: "2026-10-31T10:01:00Z", Output: path}
	if e = atomicJobUpdate(j, true); e != nil {
		t.Fatal(e)
	}
	request := `{"name":"finance","before":"` + first + `","after":"` + second + `"}`
	resp := callRunCompare(x, http.MethodPost, "/api/multi/compare-runs", request)
	if resp.Code != 201 {
		t.Fatalf("comparison failed: %d %s", resp.Code, resp.Body.String())
	}
	var report runComparison
	if e = json.Unmarshal(resp.Body.Bytes(), &report); e != nil {
		t.Fatal(e)
	}
	if report.CoordinateComparison.Status != "DIFFERENCES" || len(report.RuleChanges) == 0 || len(report.InputChanges) < 3 {
		t.Fatalf("missing impact evidence: %+v", report)
	}
	if report.CoordinateComparison.ReferenceTotal != "3000.00" || report.CoordinateComparison.ActualTotal != "3001.00" {
		t.Fatalf("wrong totals: %+v", report.CoordinateComparison)
	}
	if report.CoordinateComparison.Different+report.CoordinateComparison.Missing+report.CoordinateComparison.Extra == 0 {
		t.Fatal("no coordinate deltas")
	}
	csv := callRunCompare(x, http.MethodGet, "/api/multi/compare-runs?name=finance&id="+report.ID+"&download=csv", "")
	if csv.Code != 200 || !strings.Contains(csv.Body.String(), "kind,account,coordinate") {
		t.Fatalf("download failed: %d %s", csv.Code, csv.Body.String())
	}
	again := callRunCompare(x, http.MethodGet, "/api/multi/compare-runs?name=finance&id="+report.ID, "")
	if again.Code != 200 {
		t.Fatal("historical comparison not readable")
	}
	// Current project changes must not affect the comparison report.
	if e = os.WriteFile(amounts, []byte("another version"), 0600); e != nil {
		t.Fatal(e)
	}
	next := callRunCompare(x, http.MethodGet, "/api/multi/compare-runs?name=finance&id="+report.ID, "")
	if next.Code != 200 || next.Body.String() != again.Body.String() {
		t.Fatal("historical comparison recomputed from mutable project")
	}
	// A mutated published comparison must not be downloadable as trusted.
	diff := filepath.Join(x.parent.data, "multi-run-comparisons", report.ID, "differences.csv")
	f, e := os.OpenFile(diff, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.WriteString("tampered\n")
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	tampered := callRunCompare(x, http.MethodGet, "/api/multi/compare-runs?name=finance&id="+report.ID+"&download=csv", "")
	if tampered.Code != 422 {
		t.Fatalf("changed comparison exported: %d", tampered.Code)
	}
}
func buildRunCompareEngine(t *testing.T, base string) string {
	t.Helper()
	binary := filepath.Join(base, "core")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = filepath.Join("..", "..")
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build core: %v %s", e, output)
	}
	return binary
}
func TestRunCompareRejectsWrongJobAndInvalidMetadata(t *testing.T) {
	x, id, _ := stageEvidenceFixture(t)
	same := `{"name":"finance","before":"` + id + `","after":"` + id + `"}`
	if a := callRunCompare(x, "POST", "/api/multi/compare-runs", same); a.Code != 422 {
		t.Fatalf("same job accepted: %d", a.Code)
	}
	different := `{"name":"finance","before":"` + id + `","after":"` + strings.Repeat("f", 24) + `"}`
	if a := callRunCompare(x, "POST", "/api/multi/compare-runs", different); a.Code != 409 {
		t.Fatalf("unpublished job accepted: %d", a.Code)
	}
	if a := callRunCompare(x, "GET", "/api/multi/compare-runs?name=finance&id=../bad", ""); a.Code != 400 {
		t.Fatalf("unsafe report accepted: %d", a.Code)
	}
	if a := callRunCompare(x, "GET", "/api/multi/compare-runs?name=other&id="+id, ""); a.Code != 404 {
		t.Fatalf("foreign comparison accepted: %d", a.Code)
	}
}
func TestRunCompareUIWired(t *testing.T) {
	page, e := publicFS.ReadFile("public/multi.html")
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(page, []byte(`id="runComparison"`)) || !bytes.Contains(page, []byte(`/assets/run_compare.js`)) {
		t.Fatal("comparison UI missing")
	}
}

func TestRunCompareIdenticalFinancialResultsDoesNotInventCauses(t *testing.T) {
	x, first, _ := stageEvidenceFixture(t)
	project := filepath.Join(x.root, "finance")
	// A refreshed source-file representation differs in SHA but not in the
	// interpreted business weights; the final coordinate ledger should match.
	driverPath := filepath.Join(project, "drivers", "material.csv")
	data, e := os.ReadFile(driverPath)
	if e != nil {
		t.Fatal(e)
	}
	revised := bytes.Replace(data, []byte("Item-A,8"), []byte("Item-A,08"), 1)
	if bytes.Equal(data, revised) {
		t.Fatal("driver fixture changed unexpectedly")
	}
	if e = os.WriteFile(driverPath, revised, 0600); e != nil {
		t.Fatal(e)
	}
	second := strings.Repeat("c", 24)
	result, e := multi.Run(project, filepath.Join(x.parent.data, "multi-jobs", second), buildRunCompareEngine(t, x.parent.data))
	if e != nil || result.Status != "PASS" {
		t.Fatalf("run same financial value: %+v %v", result, e)
	}
	j := &Job{ID: second, Case: "finance", Status: "PASS", Start: "2026-10-31T10:01:00Z", Output: filepath.Join(x.parent.data, "multi-jobs", second)}
	if e = atomicJobUpdate(j, true); e != nil {
		t.Fatal(e)
	}
	response := callRunCompare(x, "POST", "/api/multi/compare-runs", `{"name":"finance","before":"`+first+`","after":"`+second+`"}`)
	if response.Code != 201 {
		t.Fatalf("comparison rejected: %d %s", response.Code, response.Body.String())
	}
	var report runComparison
	if e = json.Unmarshal(response.Body.Bytes(), &report); e != nil {
		t.Fatal(e)
	}
	if report.CoordinateComparison.Status != "MATCH" || len(report.InputChanges) == 0 || len(report.RuleChanges) != 0 {
		t.Fatalf("changed driver encoding should not invent a business difference: %+v", report)
	}
}

// A finance operator repeatedly clicking "compare" for the same immutable
// pair must not create a second large difference archive. Changed or tampered
// stored evidence must never be silently adopted as an intact comparison.
func TestRunCompareIdenticalSnapshotsReusePublishedReport(t *testing.T) {
	x, first, _ := stageEvidenceFixture(t)
	second := strings.Repeat("d", 24)
	project := filepath.Join(x.root, "finance")
	out := filepath.Join(x.parent.data, "multi-jobs", second)
	core := buildRunCompareEngine(t, x.parent.data)
	result, err := multi.Run(project, out, core)
	if err != nil || result.Status != "PASS" {
		t.Fatalf("run second complete job: %+v %v", result, err)
	}
	if err = atomicJobUpdate(&Job{ID: second, Case: "finance", Status: "PASS", Start: "2026-10-31T11:00:00Z", Output: out}, true); err != nil {
		t.Fatal(err)
	}
	request := `{"name":"finance","before":"` + first + `","after":"` + second + `"}`
	fresh := callRunCompare(x, http.MethodPost, "/api/multi/compare-runs", request)
	if fresh.Code != http.StatusCreated {
		t.Fatalf("first compare %d %s", fresh.Code, fresh.Body.String())
	}
	var a runComparison
	if err = json.Unmarshal(fresh.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	cached := callRunCompare(x, http.MethodPost, "/api/multi/compare-runs", request)
	if cached.Code != http.StatusOK {
		t.Fatalf("reuse compare %d %s", cached.Code, cached.Body.String())
	}
	var b runComparison
	if err = json.Unmarshal(cached.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if b.ID != a.ID || a.ID == "" {
		t.Fatalf("same snapshots produced different IDs: %q vs %q", a.ID, b.ID)
	}
	dirs, err := os.ReadDir(filepath.Join(x.parent.data, "multi-run-comparisons"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 || dirs[0].Name() != a.ID {
		t.Fatalf("duplicate published comparison directories: %+v", dirs)
	}
	// Strictly validate stored evidence before reusing. No automatic repair or
	// overwritten audit history if any saved CSV byte was modified.
	diff := filepath.Join(x.parent.data, "multi-run-comparisons", a.ID, "differences.csv")
	f, err := os.OpenFile(diff, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("tampered\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	refused := callRunCompare(x, http.MethodPost, "/api/multi/compare-runs", request)
	if refused.Code != http.StatusUnprocessableEntity {
		t.Fatalf("changed difference archive silently reused: %d %s", refused.Code, refused.Body.String())
	}
	dirs, err = os.ReadDir(filepath.Join(x.parent.data, "multi-run-comparisons"))
	if err != nil || len(dirs) != 1 {
		t.Fatal("tampering produced a second comparison archive", err)
	}
}
