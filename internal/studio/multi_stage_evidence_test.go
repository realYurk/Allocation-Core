package studio

import (
	"allocationcore/internal/multi"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func stageEvidenceFixture(t *testing.T, multiStage ...bool) (*MultiApp, string, string) {
	t.Helper()
	base := t.TempDir()
	projectRoot := filepath.Join(base, "multi")
	if err := os.MkdirAll(projectRoot, 0700); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(projectRoot, "finance")
	if err := copyFixture(filepath.Join("..", "..", "testdata", "multi_sample"), project); err != nil {
		t.Fatal(err)
	}
	if len(multiStage) > 0 && multiStage[0] {
		// Create a genuine two-stage saved business rule, both stages with
		// nontrivial driver splits. The Core runs both stages; UI only reads.
		rulePath := filepath.Join(project, "templates", "dual.json")
		raw, e := os.ReadFile(rulePath)
		if e != nil {
			t.Fatal(e)
		}
		var rule map[string]any
		if e = json.Unmarshal(raw, &rule); e != nil {
			t.Fatal(e)
		}
		stages := rule["stages"].([]any)
		second := map[string]any{}
		copyRaw, _ := json.Marshal(stages[0])
		if e = json.Unmarshal(copyRaw, &second); e != nil {
			t.Fatal(e)
		}
		second["id"] = "第二阶段再分摊"
		second["inputs"] = []map[string]string{{"from": "双因子分摊", "share": "1"}}
		rule["stages"] = append(stages, second)
		updated, e := json.Marshal(rule)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(rulePath, updated, 0600); e != nil {
			t.Fatal(e)
		}
	}
	binary := filepath.Join(base, "core")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile test runner: %v %s", err, out)
	}
	id := strings.Repeat("a", 24)
	output := filepath.Join(base, "multi-jobs", id)
	r, err := multi.Run(project, output, binary)
	if err != nil || r.Status != "PASS" {
		t.Fatalf("real Multi run: %+v %v", r, err)
	}
	j := &Job{ID: id, Case: "finance", Status: "PASS", Start: "2026-10-31T10:00:00Z", Output: output}
	if err := atomicJobUpdate(j, true); err != nil {
		t.Fatal(err)
	}
	return &MultiApp{root: projectRoot, parent: &Application{data: base}}, id, output
}
func callStageEvidence(x *MultiApp, id, account, extra string) *httptest.ResponseRecorder {
	path := "/api/multi/stage-evidence?name=finance&job=" + id + "&account=" + account + extra
	w := httptest.NewRecorder()
	x.stageEvidence(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}
func TestStageEvidenceActualPublishedLedgerAndHistoricImmutability(t *testing.T) {
	x, id, output := stageEvidenceFixture(t)
	got := callStageEvidence(x, id, "Cost-C", "")
	if got.Code != 200 {
		t.Fatalf("failed %d: %s", got.Code, got.Body.String())
	}
	var report stageEvidenceReport
	if err := json.Unmarshal(got.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.ManifestVerified || report.Account != "Cost-C" || len(report.RuleSHA256) != 64 || len(report.LedgerSHA256) != 64 {
		t.Fatalf("missing verified persisted evidence: %+v", report)
	}
	if len(report.Lines) != 2 || report.TotalRows != 4 {
		t.Fatalf("expected 4 ledger rows across two factors: %+v", report)
	}
	amounts := map[string]string{}
	for _, l := range report.Lines {
		amounts[l.Allocation] = l.AllocatedAmount
		if l.OutputRows != 2 || len(l.SamplePools) != 1 {
			t.Fatalf("bad per-pool evidence: %+v", l)
		}
	}
	if amounts["material"] != "450.00" || amounts["revenue"] != "150.00" {
		t.Fatalf("ledger totals wrong: %v", amounts)
	}
	selected := callStageEvidence(x, id, "Cost-C", "&period=2026-01")
	if selected.Code != 200 || !bytes.Contains(selected.Body.Bytes(), []byte(`"period":"2026-01"`)) {
		t.Fatalf("period filter: %d %s", selected.Code, selected.Body.String())
	}
	missing := callStageEvidence(x, id, "Cost-C", "&period=2027-01")
	if missing.Code != 200 || !bytes.Contains(missing.Body.Bytes(), []byte(`"total_ledger_rows":0`)) {
		t.Fatal(missing.Body.String())
	}
	csvDownload := callStageEvidence(x, id, "Cost-C", "&download=csv")
	if csvDownload.Code != 200 {
		t.Fatalf("csv export: %d", csvDownload.Code)
	}
	cr := csv.NewReader(strings.NewReader(csvDownload.Body.String()))
	rows, err := cr.ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatalf("CSV rows: %v %v", rows, err)
	}
	// Later changes to the *current* project must not alter this historical run.
	if err = os.WriteFile(filepath.Join(x.root, "finance", "assignments.csv"), []byte("account,template\nCost-C,material.json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	again := callStageEvidence(x, id, "Cost-C", "")
	if again.Code != 200 || again.Body.String() != got.Body.String() {
		t.Fatal("historical evidence changed with current project")
	}
	// A byte modification to published ledger must block evidence claims.
	var group struct {
		Engines []struct {
			ID       string   `json:"group_id"`
			Accounts []string `json:"accounts"`
		} `json:"engines"`
	}
	raw, err := os.ReadFile(filepath.Join(output, "run_summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &group); err != nil {
		t.Fatal(err)
	}
	gid := ""
	for _, g := range group.Engines {
		for _, a := range g.Accounts {
			if a == "Cost-C" {
				gid = g.ID
			}
		}
	}
	if gid == "" {
		t.Fatal("missing saved group")
	}
	ledger := filepath.Join(output, "groups", gid, "output", "ledger.csv.gz")
	f, err := os.OpenFile(ledger, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write([]byte("tamper"))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	blocked := callStageEvidence(x, id, "Cost-C", "")
	if blocked.Code != 422 || !strings.Contains(blocked.Body.String(), "证据清单") {
		t.Fatalf("tampered ledger accepted: %d %s", blocked.Code, blocked.Body.String())
	}
}
func TestStageEvidenceRefusesWrongProjectOrUnpublishedJob(t *testing.T) {
	root := t.TempDir()
	id := strings.Repeat("b", 24)
	x := &MultiApp{root: filepath.Join(root, "multi"), parent: &Application{data: root}}
	output := filepath.Join(root, "multi-jobs", id)
	job := &Job{ID: id, Case: "finance", Status: "FAILED", Start: "2026-10-31T10:00:00Z", Output: output}
	if err := atomicJobUpdate(job, true); err != nil {
		t.Fatal(err)
	}
	if w := callStageEvidence(x, id, "Cost-C", ""); w.Code != 404 {
		t.Fatalf("failed job accessed: %d", w.Code)
	}
	job.Status = "PASS"
	if err := atomicJobUpdate(job, true); err != nil {
		t.Fatal(err)
	}
	if w := callStageEvidence(x, id, "Cost-C", ""); w.Code != 422 {
		t.Fatalf("missing published result accessed: %d", w.Code)
	}
	w := httptest.NewRecorder()
	x.stageEvidence(w, httptest.NewRequest(http.MethodGet, "/api/multi/stage-evidence?name=other&job="+id+"&account=Cost-C", nil))
	if w.Code != 404 {
		t.Fatalf("foreign project permitted: %d", w.Code)
	}
}
func TestStageEvidenceUIWired(t *testing.T) {
	page, err := publicFS.ReadFile("public/multi.html")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(page, []byte(`id="stageEvidence"`)) || !bytes.Contains(page, []byte(`/assets/stage_evidence.js`)) {
		t.Fatal("UI missing stage evidence")
	}
}

func TestStageEvidenceTwoStagesUsesActualCoreAndDoesNotAddStages(t *testing.T) {
	x, id, _ := stageEvidenceFixture(t, true)
	response := callStageEvidence(x, id, "Cost-C", "")
	if response.Code != 200 {
		t.Fatalf("multi stage GET %d: %s", response.Code, response.Body.String())
	}
	var report stageEvidenceReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.ManifestVerified || len(report.Lines) != 4 || report.TotalRows != 8 {
		t.Fatalf("multi-stage ledger not present: %+v", report)
	}
	subtotals := map[string]*big.Int{}
	for _, l := range report.Lines {
		if subtotals[l.Stage] == nil {
			subtotals[l.Stage] = new(big.Int)
		}
		n, ok := new(big.Int).SetString(strings.ReplaceAll(l.AllocatedAmount, ".", ""), 10)
		if !ok {
			t.Fatalf("invalid ledger decimal %s", l.AllocatedAmount)
		}
		subtotals[l.Stage].Add(subtotals[l.Stage], n)
	}
	if len(subtotals) != 2 {
		t.Fatalf("missing second stage: %v", subtotals)
	}
	for stage, total := range subtotals {
		if total.String() != "60000" {
			t.Fatalf("%s subtotal expected 600.00, got %s", stage, total)
		}
	}
	// 600.00 appears in EACH stage, not a 1200.00 final accounting result.
}
