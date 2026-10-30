package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"allocationcore/internal/acceptance"
)

func TestWorkbenchReconciliationAndIsolation(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "multi", "Example1")
	if e := os.MkdirAll(project, 0700); e != nil {
		t.Fatal(e)
	}
	data := filepath.Join(root, "multi-jobs")
	id := strings.Repeat("a", 24)
	dir := filepath.Join(data, id)
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	actual := "terminal_stage,row_id,account,period,sku,amount\nS,1,A,M1,P1,60.00\nS,2,A,M1,P2,40.00\n"
	ref := "account,period,sku,value\nA,M1,P1,70.00\nA,M1,P2,30.00\n"
	if e := os.WriteFile(filepath.Join(dir, "results.csv"), []byte(actual), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(project, "reference.csv"), []byte(ref), 0600); e != nil {
		t.Fatal(e)
	}
	x := &MultiApp{parent: &Application{data: root}, root: filepath.Join(root, "multi"), jobs: map[string]*Job{id: &Job{ID: id, Case: "Example1", Status: "PASS"}}}
	req := httptest.NewRequest("POST", "/api/multi/reconcile", strings.NewReader(`{"name":"Example1","job":"`+id+`","precision":2,"tolerance":"0"}`))
	w := httptest.NewRecorder()
	x.reconcile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d: %s", w.Code, w.Body.String())
	}
	var v struct {
		Report acceptance.Report `json:"report"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	if v.Report.Status != "DIFFERENCES" || v.Report.Different != 2 {
		t.Fatalf("%#v", v.Report)
	}
	req = httptest.NewRequest("POST", "/api/multi/reconcile", strings.NewReader(`{"name":"Other1","job":"`+id+`","precision":2}`))
	w = httptest.NewRecorder()
	x.reconcile(w, req)
	if w.Code == http.StatusOK {
		t.Fatal("cross-project access allowed")
	}
	// Never permit an arbitrary linked reference outside the private project.
	if e := os.Remove(filepath.Join(project, "reference.csv")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(dir, "results.csv"), filepath.Join(project, "reference.csv")); e == nil {
		req = httptest.NewRequest("POST", "/api/multi/reconcile", strings.NewReader(`{"name":"Example1","job":"`+id+`","precision":2}`))
		w = httptest.NewRecorder()
		x.reconcile(w, req)
		if w.Code == http.StatusOK {
			t.Fatal("symlink accepted")
		}
	}
}
