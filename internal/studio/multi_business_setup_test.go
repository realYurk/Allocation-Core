package studio

import (
	"allocationcore/internal/engine"
	"allocationcore/internal/multi"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func postVisualDraft(t *testing.T, x *MultiApp, name, template, draft string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"name": name, "template": template, "draft": draft})
	w := httptest.NewRecorder()
	x.createVisualDraft(w, httptest.NewRequest(http.MethodPost, "/api/multi/visual-draft", bytes.NewReader(raw)))
	return w
}

func driverRequirementsForTest(t *testing.T, x *MultiApp) driverRequirements {
	t.Helper()
	w := httptest.NewRecorder()
	x.driverNeeds(w, httptest.NewRequest(http.MethodGet, "/api/multi/driver-needs?name=financial&template=base.json", nil))
	if w.Code != 200 {
		t.Fatalf("driver needs: %d %s", w.Code, w.Body.String())
	}
	var req driverRequirements
	if err := json.Unmarshal(w.Body.Bytes(), &req); err != nil {
		t.Fatal(err)
	}
	if req.SHA256 == "" || len(req.Drivers) != 1 || req.Drivers[0].Alias != "units" || len(req.Drivers[0].WeightFields) != 1 {
		t.Fatalf("bad requirements: %+v", req)
	}
	return req
}

func postDriverCSV(t *testing.T, x *MultiApp, filename, sha, source string, mapping map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	jsonMap, _ := json.Marshal(mapping)
	for _, field := range [][2]string{{"name", "financial"}, {"template", "base.json"}, {"alias", "units"}, {"file", filename}, {"base_sha256", sha}, {"mapping", string(jsonMap)}} {
		if e := form.WriteField(field[0], field[1]); e != nil {
			t.Fatal(e)
		}
	}
	part, e := form.CreateFormFile("csv", "source.csv")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = part.Write([]byte(source)); e != nil {
		t.Fatal(e)
	}
	if e = form.Close(); e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/multi/driver-import", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	rec := httptest.NewRecorder()
	x.importMappedDriver(rec, req)
	return rec
}

func TestVisualDraftIsSchemaOnlyAndCanBeEdited(t *testing.T) {
	x, project, _, _ := designerBridgeFixture(t)
	originalAmount, err := os.ReadFile(filepath.Join(project, "amounts.csv"))
	if err != nil {
		t.Fatal(err)
	}
	rec := postVisualDraft(t, x, "financial", "base.json", "financial_draft")
	if rec.Code != 201 {
		t.Fatalf("draft create: %d %s", rec.Code, rec.Body.String())
	}
	directory := filepath.Join(x.parent.custom, "financial_draft")
	meta, ok, err := readVisualDraft(directory)
	if err != nil || !ok || meta.Kind != visualDraftMarker || meta.Precision != 2 {
		t.Fatalf("draft metadata: %+v %t %v", meta, ok, err)
	}
	_, gotDir, kind, err := x.parent.readCase("user_financial_draft")
	if err != nil || kind != "custom" || gotDir != directory {
		t.Fatalf("designer cannot open schema-only draft %q %q: %v", gotDir, kind, err)
	}
	for _, name := range []string{"amounts.csv", "units.csv"} {
		f, err := os.Open(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		rd := csv.NewReader(f)
		headers, err := rd.Read()
		if err != nil {
			t.Fatal(err)
		}
		if len(headers) == 0 {
			t.Fatal("missing schema header")
		}
		_, e := rd.Read()
		_ = f.Close()
		if e == nil {
			t.Fatalf("business rows were copied into draft %s", name)
		}
	}
	if duplicate := postVisualDraft(t, x, "financial", "base.json", "financial_draft"); duplicate.Code != 409 {
		t.Fatalf("existing draft overwritten %d: %s", duplicate.Code, duplicate.Body.String())
	}
	rule, err := os.ReadFile(filepath.Join(directory, "rule.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec engine.Spec
	if err = json.Unmarshal(rule, &spec); err != nil {
		t.Fatal(err)
	}
	spec.Name = "财务规则草稿已修改"
	newRule, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(rule)
	body, _ := json.Marshal(map[string]any{"case": "user_financial_draft", "rule": json.RawMessage(newRule), "base_sha": hex.EncodeToString(sum[:])})
	w := httptest.NewRecorder()
	x.parent.designerRule(w, httptest.NewRequest(http.MethodPost, "/api/designer/rule", bytes.NewReader(body)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "STRUCTURE_ONLY") {
		t.Fatalf("draft schema-only save: %d %s", w.Code, w.Body.String())
	}
	check := httptest.NewRecorder()
	checkReq, _ := json.Marshal(map[string]any{"case": "user_financial_draft", "rule": json.RawMessage(newRule)})
	x.parent.designerCheck(check, httptest.NewRequest(http.MethodPost, "/api/designer/check", bytes.NewReader(checkReq)))
	if check.Code != 200 || !strings.Contains(check.Body.String(), "STRUCTURE_ONLY") {
		t.Fatalf("draft check should not claim DOCTOR PASS: %d %s", check.Code, check.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(project, "amounts.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(originalAmount, got) {
		t.Fatal("creating/editing visual draft changed source amounts")
	}
	// A draft cannot change the amount precision, dimensions, or account invariants.
	spec.Precision = 3
	invalid, _ := json.Marshal(spec)
	if err = validateStructuralDraft(invalid, meta); err == nil {
		t.Fatal("precision change was accepted")
	}
	spec.Precision = 2
	spec.Immutable = []string{"period"}
	invalid, _ = json.Marshal(spec)
	if err = validateStructuralDraft(invalid, meta); err == nil {
		t.Fatal("account conservation was not enforced")
	}
}

func TestDriverMappingImportRoundTripAndNonOverwrite(t *testing.T) {
	x, project, _, _ := designerBridgeFixture(t)
	needs := driverRequirementsForTest(t, x)
	mapping := map[string]string{"period": "年月", "department": "成本部门", "sku": "产品", "units": "数量"}
	data := "\ufeff年月,成本部门,产品,数量,无关列\r\n2026-01,Shared,Product-A,3.000000,keep\r\n2026-01,Shared,Product-B,7.000000,keep\r\n"
	old, err := os.ReadFile(filepath.Join(project, "drivers", "units.csv"))
	if err != nil {
		t.Fatal(err)
	}
	rec := postDriverCSV(t, x, "mapped_units.csv", needs.SHA256, data, mapping)
	if rec.Code != 201 {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(project, "drivers", "mapped_units.csv"))
	if err != nil {
		t.Fatal(err)
	}
	expected := "period,department,sku,units\n2026-01,Shared,Product-A,3.000000\n2026-01,Shared,Product-B,7.000000\n"
	if string(got) != expected {
		t.Fatalf("incorrect Driver mapping:\n%s", got)
	}
	duplicate := postDriverCSV(t, x, "mapped_units.csv", needs.SHA256, data, mapping)
	if duplicate.Code != 409 {
		t.Fatalf("duplicate Driver overwrote: %d %s", duplicate.Code, duplicate.Body.String())
	}
	still, err := os.ReadFile(filepath.Join(project, "drivers", "mapped_units.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, still) {
		t.Fatal("duplicate attempt modified destination")
	}
	prev, err := os.ReadFile(filepath.Join(project, "drivers", "units.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(old, prev) {
		t.Fatal("original Driver was modified")
	}
	for key, invalid := range map[string]string{
		"wrong_precision": "年月,成本部门,产品,数量\n2026-01,Shared,Product-A,1.0000009\n",
		"broken_csv":      "年月,成本部门,产品,数量\n\"unclosed\n",
		"empty_dimension": "年月,成本部门,产品,数量\n2026-01,,Product-A,12\n",
		"empty_driver":    "年月,成本部门,产品,数量\n",
	} {
		t.Run(key, func(t *testing.T) {
			res := postDriverCSV(t, x, key+".csv", needs.SHA256, invalid, mapping)
			if res.Code == 201 {
				t.Fatalf("invalid file published: %s", res.Body.String())
			}
			if _, err := os.Stat(filepath.Join(project, "drivers", key+".csv")); !os.IsNotExist(err) {
				t.Fatal("partial or invalid driver published")
			}
		})
	}
	if rec = postDriverCSV(t, x, "unknown_alias.csv", needs.SHA256, data, map[string]string{"sku": "产品", "units": "bad"}); rec.Code == 201 {
		t.Fatal("missing weight source accepted")
	}
	if rec = postDriverCSV(t, x, "traversal.csv", needs.SHA256, data, map[string]string{"sku": "产品", "units": "数量", "fake": "成本部门"}); rec.Code == 201 {
		t.Fatal("unknown mapped field accepted")
	}
	if rec = postDriverCSV(t, x, "stale.csv", "wrong-hash", data, mapping); rec.Code != 409 {
		t.Fatalf("stale source allowed: %d", rec.Code)
	}
	x.busy["financial"] = true
	if rec = postDriverCSV(t, x, "running.csv", needs.SHA256, data, mapping); rec.Code != 409 {
		t.Fatalf("busy project allowed: %d", rec.Code)
	}
}

func TestDraftToMappedDriverToNewTemplateWorkflow(t *testing.T) {
	x, project, _, _ := designerBridgeFixture(t)
	// A user starts from the project itself: no manual copying of JSON.
	made := postVisualDraft(t, x, "financial", "base.json", "rule_from_finance")
	if made.Code != 201 {
		t.Fatalf("draft failed: %s", made.Body.String())
	}
	needs := driverRequirementsForTest(t, x)
	rows := "period,site,vehicle,volume\n2026-01,Shared,Product-A,3\n2026-01,Shared,Product-B,7\n"
	uploaded := postDriverCSV(t, x, "volume.csv", needs.SHA256, rows, map[string]string{"period": "period", "department": "site", "sku": "vehicle", "units": "volume"})
	if uploaded.Code != 201 {
		t.Fatalf("driver upload: %d %s", uploaded.Code, uploaded.Body.String())
	}
	req := map[string]any{"name": "financial", "case": "user_rule_from_finance", "template": "expense_by_volume.json", "preview": true, "driver_mapping": map[string]string{"units": "volume.csv"}}
	w := postDesignerBridge(t, x, req)
	if w.Code != 200 {
		t.Fatalf("new rule preview: %d %s", w.Code, w.Body.String())
	}
	var plan designerImportPlan
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.Ready {
		t.Fatalf("new mapped driver does not satisfy rule header: %+v", plan)
	}
	req["preview"] = false
	req["base_sha256"] = plan.SourceSHA256
	imported := postDesignerBridge(t, x, req)
	if imported.Code != 201 {
		t.Fatalf("new rule import: %d %s", imported.Code, imported.Body.String())
	}
	newTpl, err := os.ReadFile(filepath.Join(project, "templates", "expense_by_volume.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec engine.Spec
	if err = json.Unmarshal(newTpl, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Datasets.Drivers["units"] != "volume.csv" || spec.Datasets.Amounts["expenses"] != "amounts.csv" {
		t.Fatal("mapping did not reach new template")
	}
	ass, err := os.ReadFile(filepath.Join(project, "assignments.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ass) != "account,template\nAccount-X,base.json\n" {
		t.Fatal("workflow modified existing account assignments")
	}
}

// A synthetic, independent end-to-end proof of the complete user-visible chain:
// project template -> visual draft -> mapped Driver -> new rule -> assignment
// -> read-only Inspect -> real authoritative Core -> per-account conservation.
func TestBusinessWorkflowEndToEndRealCore(t *testing.T) {
	x, project, _, _ := designerBridgeFixture(t)
	draft := postVisualDraft(t, x, "financial", "base.json", "e2e_rule")
	if draft.Code != 201 {
		t.Fatalf("draft creation: %d %s", draft.Code, draft.Body.String())
	}
	needs := driverRequirementsForTest(t, x)
	csvSource := "date,cost_center,model,quantity\n2026-01,Shared,Product-A,3\n2026-01,Shared,Product-B,7\n"
	imported := postDriverCSV(t, x, "business_units.csv", needs.SHA256, csvSource, map[string]string{
		"period": "date", "department": "cost_center", "sku": "model", "units": "quantity",
	})
	if imported.Code != 201 {
		t.Fatalf("driver: %d %s", imported.Code, imported.Body.String())
	}
	ruleReq := map[string]any{"name": "financial", "case": "user_e2e_rule", "template": "e2e_rule.json", "preview": true,
		"driver_mapping": map[string]string{"units": "business_units.csv"}}
	preview := postDesignerBridge(t, x, ruleReq)
	if preview.Code != 200 {
		t.Fatalf("rule preview: %d %s", preview.Code, preview.Body.String())
	}
	var plan designerImportPlan
	if err := json.Unmarshal(preview.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.Ready {
		t.Fatal("new driver header did not satisfy new template")
	}
	ruleReq["preview"] = false
	ruleReq["base_sha256"] = plan.SourceSHA256
	installed := postDesignerBridge(t, x, ruleReq)
	if installed.Code != 201 {
		t.Fatalf("rule import: %d %s", installed.Code, installed.Body.String())
	}
	// Business user explicitly assigns the new rule. No hidden auto assignment.
	original, err := os.ReadFile(filepath.Join(project, "assignments.csv"))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(original)
	assignmentReq, _ := json.Marshal(map[string]any{"name": "financial", "base_sha256": hex.EncodeToString(hash[:]),
		"updates": []map[string]string{{"account": "Account-X", "template": "e2e_rule.json"}}})
	saved := httptest.NewRecorder()
	x.assign(saved, httptest.NewRequest(http.MethodPost, "/api/multi/assign", bytes.NewReader(assignmentReq)))
	if saved.Code != 200 {
		t.Fatalf("explicit rule assignment: %d %s", saved.Code, saved.Body.String())
	}
	cfg, err := os.ReadFile(filepath.Join("..", "..", "testdata", "multi_sample", "project.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(project, "project.json"), cfg, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(project, "overrides.csv"), []byte("account,stage,allocation,driver_dataset,weight_field,share\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inspect := multi.InspectProject(context.Background(), project)
	if inspect.Status != "PASS" || inspect.Accounts != 1 || inspect.InputAmount != "20.00" {
		t.Fatalf("preflight failed: %+v", inspect)
	}
	// A single executable is both the Studio host and authoritative worker.
	binaryName := "AllocationCore"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	bin := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build authoritative core %v: %s", err, out)
	}
	outputDir := filepath.Join(t.TempDir(), "financial-run")
	result, err := multi.Run(project, outputDir, bin)
	if err != nil {
		t.Fatalf("real allocation failed: %v", err)
	}
	if result.Status != "PASS" || result.Input != "20.00" || result.Output != "20.00" || result.Accounts != 1 || result.ResultRows != 2 {
		t.Fatalf("incorrect business result: %+v", result)
	}
	rowsFile, err := os.Open(filepath.Join(outputDir, "results.csv"))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(rowsFile).ReadAll()
	_ = rowsFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("wanted header plus 2 product allocations, got %d", len(rows))
	}
	col := map[string]int{}
	for i, k := range rows[0] {
		col[k] = i
	}
	totals := map[string]string{}
	for _, row := range rows[1:] {
		totals[row[col["sku"]]] = row[col["amount"]]
	}
	if totals["Product-A"] != "6.00" || totals["Product-B"] != "14.00" {
		t.Fatalf("expected independent 3:7 driver split of 20.00 into 6.00 and 14.00, got %+v", totals)
	}
}
