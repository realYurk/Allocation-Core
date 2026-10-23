package service

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func initFixture(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "case")
	if e := os.Mkdir(dir, 0755); e != nil {
		t.Fatal(e)
	}
	// Exact same public JSON schema as CLI.
	rule := `{"schema":"allocation-ir/2.0","name":"service","precision":2,"dimensions":["period","sku"],"immutable_dimensions":["period"],"hierarchies":{},"datasets":{"amounts":{"expenses":"amounts.csv"},"drivers":{"sales":"drivers.csv"}},"stages":[{"id":"step1","inputs":[{"from":"expenses","share":"1"}],"source":{"grouping":{"period":"AS_IS","sku":"TOTAL"}},"target":{"period":{"mode":"SOURCE"},"sku":{"mode":"INCLUDE","include":["商品A","商品B"]}},"conservation_keys":["period"],"allocations":[{"id":"sales","share":"1","driver_dataset":"sales","weight_field":"units","negative_driver_policy":"REJECT"}]}]}`
	files := map[string]string{"rule.json": rule, "amounts.csv": "period,sku,amount\n01,待分摊,1000.00\n", "drivers.csv": "period,sku,units\n01,商品A,3\n01,商品B,1\n"}
	for n, v := range files {
		if e := os.WriteFile(filepath.Join(dir, n), []byte(v), 0644); e != nil {
			t.Fatal(e)
		}
	}
	server, e := New(Config{Root: root, Store: t.TempDir(), Token: "secret", Concurrent: 1, Queue: 2})
	if e != nil {
		t.Fatal(e)
	}
	return server, root
}
func req(s *Server, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestAPIQueueOutputsAndAuthorization(t *testing.T) {
	s, _ := initFixture(t)
	if w := req(s, "GET", "/v1/jobs", "", "bad"); w.Code != 401 {
		t.Fatalf("expected 401 got %d", w.Code)
	}
	if w := req(s, "POST", "/v1/validate", `{"rule":"case/rule.json"}`, "secret"); w.Code != 200 {
		t.Fatalf("validate %d %s", w.Code, w.Body)
	}
	w := req(s, "POST", "/v1/jobs", `{"rule":"case/rule.json","workers":2,"disk_partitions":4,"driver_disk_index":true}`, "secret")
	if w.Code != 202 {
		t.Fatalf("post %d %s", w.Code, w.Body)
	}
	var job Job
	if e := json.Unmarshal(w.Body.Bytes(), &job); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		w = req(s, "GET", "/v1/jobs/"+job.ID, "", "secret")
		if e := json.Unmarshal(w.Body.Bytes(), &job); e != nil {
			t.Fatal(e)
		}
		if job.Status == "PASS" {
			break
		}
		if job.Status == "FAILED" {
			t.Fatal(job.Error)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if job.Status != "PASS" {
		t.Fatalf("job status %s", job.Status)
	}
	w = req(s, "GET", "/v1/jobs/"+job.ID+"/results.csv", "", "secret")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("商品A")) {
		t.Fatalf("results %d %s", w.Code, w.Body)
	}
	w = req(s, "GET", "/v1/jobs/"+job.ID+"/ledger.csv", "", "secret")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("driver_denominator")) {
		t.Fatal("ledger not returned")
	}
	w = req(s, "GET", "/v1/jobs/"+job.ID+"/run_summary.json", "", "secret")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"status": "PASS"`)) {
		t.Fatal("summary not returned")
	}
}
func TestAPIRejectsTraversalAndSymlinkEscape(t *testing.T) {
	s, root := initFixture(t)
	for _, v := range []string{`{"rule":"../outside.json"}`, `{"rule":"/etc/passwd"}`} {
		w := req(s, "POST", "/v1/jobs", v, "secret")
		if w.Code != 400 {
			t.Fatalf("expected 400: %s", w.Body)
		}
	}
	external := filepath.Join(t.TempDir(), "outside.json")
	os.WriteFile(external, []byte(`{}`), 0600)
	if e := os.Symlink(external, filepath.Join(root, "escape.json")); e == nil {
		w := req(s, "POST", "/v1/jobs", `{"rule":"escape.json"}`, "secret")
		if w.Code != 400 {
			t.Fatalf("symlink escape code %d", w.Code)
		}
	}
	w := req(s, "GET", "/v1/jobs/nothere/../../secret", "", "secret")
	if w.Code == 200 {
		t.Fatal("traversal via path")
	}
}
