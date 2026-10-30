package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
	rule := `{"schema":"allocation-ir/2.0","name":"service","precision":2,"dimensions":["period","sku"],"immutable_dimensions":["period"],"hierarchies":{},"datasets":{"amounts":{"expenses":"amounts.csv"},"drivers":{"sales":"drivers.csv"}},"stages":[{"id":"step1","inputs":[{"from":"expenses","share":"1"}],"source":{"grouping":{"period":"AS_IS","sku":"TOTAL"}},"target":{"period":{"mode":"SOURCE"},"sku":{"mode":"INCLUDE","include":["Item-A","Item-B"]}},"conservation_keys":["period"],"allocations":[{"id":"sales","share":"1","driver_dataset":"sales","weight_field":"units","negative_driver_policy":"REJECT"}]}]}`
	files := map[string]string{"rule.json": rule, "amounts.csv": "period,sku,amount\n01,待分摊,1000.00\n", "drivers.csv": "period,sku,units\n01,Item-A,3\n01,Item-B,1\n"}
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
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("Item-A")) {
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

func TestHTTPStreamingCompressedEvidence(t *testing.T) {
	s, _ := initFixture(t)
	w := req(s, "POST", "/v1/jobs", `{"rule":"case/rule.json","workers":2,"disk_partitions":4,"evidence_gzip":true}`, "secret")
	if w.Code != 202 {
		t.Fatalf("submit %d %s", w.Code, w.Body)
	}
	var job Job
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		w = req(s, "GET", "/v1/jobs/"+job.ID, "", "secret")
		if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		if job.Status == "PASS" {
			break
		}
		if job.Status == "FAILED" {
			t.Fatal(job.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != "PASS" {
		t.Fatal("incomplete job")
	}
	for _, name := range []string{"results.csv", "ledger.csv.gz", "source_to_pool.csv.gz", "lineage_links.csv.gz", "evidence_stream_manifest.json"} {
		w = req(s, "GET", "/v1/jobs/"+job.ID+"/"+name, "", "secret")
		if w.Code != 200 || len(w.Body.Bytes()) == 0 {
			t.Fatalf("bad %s: %d", name, w.Code)
		}
	}
	if w = req(s, "GET", "/v1/jobs/"+job.ID+"/ledger.csv", "", "secret"); w.Code == 200 {
		t.Fatal("phantom raw ledger served")
	}
}

func TestHTTPProductionCapacityAdmission(t *testing.T) {
	s, root := initFixture(t)
	policy := filepath.Join(root, "cap.json")
	blob := `{"pools":3600,"targets_per_pool":500000,"factor_paths":1,"result_bytes_per_row":144,"evidence_bytes_per_row":300,"overhead_percent":150,"max_rows":50000000,"max_peak_bytes":107374182400,"reserve_free_bytes":0}`
	if e := os.WriteFile(policy, []byte(blob), 0600); e != nil {
		t.Fatal(e)
	}
	s.cfg.BudgetFile = policy
	w := req(s, "POST", "/v1/jobs", `{"rule":"case/rule.json"}`, "secret")
	if w.Code != 507 || !strings.Contains(w.Body.String(), "capacity budget blocked job") {
		t.Fatalf("expected blocked: %d %s", w.Code, w.Body)
	}
	if len(s.jobs) != 0 {
		t.Fatal("blocked submit created a job")
	}
}

func TestSnapshotHashesAndIndependentInputs(t *testing.T) {
	s, root := initFixture(t)
	frozen, hashes, err := s.snapshot("hash-fixture", "case/rule.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 3 {
		t.Fatalf("expected hashes for three unique inputs, got %+v", hashes)
	}
	before, err := os.ReadFile(frozen)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "case", "rule.json")
	if err = os.WriteFile(source, []byte(`{"invalid":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(frozen)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("snapshot input changed when source edited")
	}
	if _, _, err = s.snapshot("other", "case/rule.json"); err == nil {
		t.Fatal("invalid modified rule accepted")
	}
	for rel, hash := range hashes {
		data, err := os.ReadFile(filepath.Join(s.store, "snapshots", "hash-fixture", rel))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != hash {
			t.Fatalf("wrong inline copy hash for %s", rel)
		}
	}
}
