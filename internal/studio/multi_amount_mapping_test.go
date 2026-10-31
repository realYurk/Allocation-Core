package studio

import (
	"allocationcore/internal/multi"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

func mappedOnboardRequest(t *testing.T, app *MultiApp, project string, mapping map[string]string, contents string) *httptest.ResponseRecorder {
	t.Helper()
	var buffer bytes.Buffer
	form := multipart.NewWriter(&buffer)
	for _, p := range [][2]string{{"name", project}, {"source", "original"}, {"template", "material.json"}} {
		if err := form.WriteField(p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	if mapping != nil {
		m, err := json.Marshal(mapping)
		if err != nil {
			t.Fatal(err)
		}
		if err = form.WriteField("mapping", string(m)); err != nil {
			t.Fatal(err)
		}
	}
	part, err := form.CreateFormFile("csv", "raw_business.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/multi/onboard", &buffer)
	request.Header.Set("Content-Type", form.FormDataContentType())
	recorder := httptest.NewRecorder()
	app.createFromAmountsCSV(recorder, request)
	return recorder
}

func businessAmountMapping() map[string]string {
	return map[string]string{"period": "年月", "department": "成本中心", "sku": "产品", "account": "会计科目", "currency": "币种", "amount": "费用金额", "row_id": "单据号"}
}

func TestMappedOnboardingCreatesNewProjectAndKeepsAuditPrecision(t *testing.T) {
	app, source := onboardingApp(t)
	before, err := os.ReadFile(filepath.Join(source, "amounts.csv"))
	if err != nil {
		t.Fatal(err)
	}
	raw := "\ufeff单据号,会计科目,年月,产品,费用金额,币种,成本中心,不相关字段\r\n" +
		"INV-001,Cost-A,2026-01,Unassigned,12.25,CUR,SharedPool,保留在源文件\r\n" +
		"INV-002,Cost-B,2026-01,Unassigned,13.75,CUR,SharedPool,无关\r\n" +
		"INV-003,Cost-A,2026-01,Unassigned,-2.00,CUR,SharedPool,无关\r\n"
	rec := mappedOnboardRequest(t, app, "external_columns", businessAmountMapping(), raw)
	if rec.Code != 201 {
		t.Fatalf("mapped onboarding got %d: %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Project string         `json:"project"`
		Import  onboardPreview `json:"import"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(raw))
	if result.Project != "external_columns" || result.Import.Accounts != 2 || result.Import.Rows != 3 || result.Import.Total != "24.00" || result.Import.SourceSHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("invalid import report: %+v", result)
	}
	f, err := os.Open(filepath.Join(app.root, "external_columns", "amounts.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	expectedHeader := []string{"period", "department", "sku", "account", "currency", "amount", "row_id"}
	if strings.Join(rows[0], ",") != strings.Join(expectedHeader, ",") {
		t.Fatalf("header: %v", rows[0])
	}
	if len(rows) != 4 || strings.Join(rows[1], ",") != "2026-01,SharedPool,Unassigned,Cost-A,CUR,12.25,INV-001" || rows[3][5] != "-2.00" {
		t.Fatalf("unexpected amount rows: %v", rows)
	}
	receiptBytes, err := os.ReadFile(filepath.Join(app.root, "external_columns", "onboarding_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest onboardingReceipt
	if err = json.Unmarshal(receiptBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Kind != "business-onboarding/v1" || manifest.Mapping["account"] != "会计科目" || manifest.SourceSHA256 != result.Import.SourceSHA256 || manifest.AmountsSHA256 != result.Import.SHA256 || manifest.Status != "INPUT_ONLY_NOT_BUSINESS_VERIFIED" {
		t.Fatalf("business import receipt is incorrect: %+v", manifest)
	}
	normalized, err := os.ReadFile(filepath.Join(app.root, "external_columns", "amounts.csv"))
	if err != nil {
		t.Fatal(err)
	}
	outSHA := sha256.Sum256(normalized)
	if result.Import.SHA256 != hex.EncodeToString(outSHA[:]) || result.Import.SHA256 == result.Import.SourceSHA256 {
		t.Fatal("normalized/raw hashes incorrect")
	}
	after, err := os.ReadFile(filepath.Join(source, "amounts.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("source project amount data mutated")
	}
	report := multi.InspectProject(context.Background(), filepath.Join(app.root, "external_columns"))
	if report.Status != "PASS" || report.Accounts != 2 || report.TotalRows != 3 {
		t.Fatalf("mapped project preflight failed: %+v", report)
	}
}

func TestMappedOnboardingRejectsAmbiguousOrBrokenDataWithoutPublishing(t *testing.T) {
	sample := "年月,成本中心,产品,会计科目,币种,费用金额,单据号\n2026-01,SharedPool,Unassigned,Cost-A,CUR,12.25,INV-A\n"
	tests := []struct {
		name    string
		mapping map[string]string
		raw     string
	}{
		{"duplicate_source_mapping", func() map[string]string {
			m := businessAmountMapping()
			m["amount"] = "单据号"
			m["row_id"] = "单据号"
			return m
		}(), sample},
		{"missing_account_mapping", func() map[string]string { m := businessAmountMapping(); delete(m, "account"); return m }(), sample},
		{"unknown_canonical_field", func() map[string]string { m := businessAmountMapping(); m["invented"] = "单据号"; return m }(), sample},
		{"nonexistent_source_column", func() map[string]string { m := businessAmountMapping(); m["account"] = "不存在"; return m }(), sample},
		{"repeated_header", businessAmountMapping(), "年月,年月,产品,会计科目,币种,费用金额,单据号\n2026-01,SharedPool,Unassigned,Cost-A,CUR,12.25,INV-A\n"},
		{"invalid_decimal", businessAmountMapping(), strings.Replace(sample, "12.25", "12.251", 1)},
		{"empty_account", businessAmountMapping(), strings.Replace(sample, "Cost-A", "", 1)},
		{"empty_dimension", businessAmountMapping(), strings.Replace(sample, "Unassigned,", "", 1)},
		{"malformed_csv", businessAmountMapping(), sample + "\"unterminated"},
		{"empty_file", businessAmountMapping(), "年月,成本中心,产品,会计科目,币种,费用金额,单据号\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, _ := onboardingApp(t)
			rec := mappedOnboardRequest(t, app, "new_project", tt.mapping, tt.raw)
			if rec.Code != 422 {
				t.Fatalf("must fail with 422, got %d %s", rec.Code, rec.Body.String())
			}
			if _, err := os.Stat(filepath.Join(app.root, "new_project")); !os.IsNotExist(err) {
				t.Fatalf("invalid data published new project: %v", err)
			}
		})
	}
}

func TestMappedOnboardDoesNotOverwriteAndCanDropUnmappedSourceColumns(t *testing.T) {
	app, _ := onboardingApp(t)
	raw := "年月,成本中心,产品,会计科目,币种,费用金额,额外\n2026-01,SharedPool,Unassigned,Cost-A,CUR,1.00,private\n"
	mapping := businessAmountMapping()
	delete(mapping, "row_id")
	one := mappedOnboardRequest(t, app, "created", mapping, raw)
	if one.Code != 201 {
		t.Fatalf("create first: %d %s", one.Code, one.Body.String())
	}
	first, err := os.ReadFile(filepath.Join(app.root, "created", "amounts.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(first), "private") || strings.Contains(string(first), "row_id") {
		t.Fatal("unmapped data leaked into normalized project")
	}
	second := mappedOnboardRequest(t, app, "created", mapping, raw)
	if second.Code != 409 {
		t.Fatalf("duplicate should be 409 not %d: %s", second.Code, second.Body.String())
	}
	last, err := os.ReadFile(filepath.Join(app.root, "created", "amounts.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, last) {
		t.Fatal("second request modified published project")
	}
}

func TestMappedOnboardingStreamingOverFiveMiB(t *testing.T) {
	if testing.Short() {
		t.Skip("large streaming file")
	}
	app, _ := onboardingApp(t)
	var source strings.Builder
	source.WriteString("年月,成本中心,产品,会计科目,币种,费用金额,单据号\n")
	for i := 0; i < 90000; i++ {
		fmt.Fprintf(&source, "2026-01,SharedPool,Unassigned,Cost-A,CUR,0.01,INV-%07d-padding\n", i)
	}
	if source.Len() <= 5<<20 {
		t.Fatalf("fixture too small: %d", source.Len())
	}
	rec := mappedOnboardRequest(t, app, "large_mapped", businessAmountMapping(), source.String())
	if rec.Code != 201 {
		t.Fatalf("large mapped CSV failed: %d %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Import onboardPreview `json:"import"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Import.Rows != 90000 || result.Import.Accounts != 1 || result.Import.Total != "900.00" {
		t.Fatalf("unexpected large summary %+v", result.Import)
	}
}

// Real Core E2E: no simulated arithmetic. A vendor CSV with entirely custom
// headers is normalized, split into four accounts and allocated by the
// existing authoritative engine, with source/target money checked exactly.
func TestMappedOnboardingRunsFullAllocationCore(t *testing.T) {
	if testing.Short() {
		t.Skip("builds local Core executable")
	}
	app, _ := onboardingApp(t)
	input := "年月,成本中心,产品,会计科目,币种,费用金额,单据号\n" +
		"2026-01,SharedPool,Unassigned,Cost-A,CUR,1200.00,E1\n" +
		"2026-01,SharedPool,Unassigned,Cost-B,CUR,800.00,L1\n" +
		"2026-01,SharedPool,Unassigned,Cost-C,CUR,600.00,M1\n" +
		"2026-01,SharedPool,Unassigned,Cost-D,CUR,400.00,D1\n"
	response := mappedOnboardRequest(t, app, "calculation_case", businessAmountMapping(), input)
	if response.Code != 201 {
		t.Fatalf("mapped onboarding: %d %s", response.Code, response.Body.String())
	}
	dir := filepath.Join(app.root, "calculation_case")
	inspect := multi.InspectProject(context.Background(), dir)
	if inspect.Status != "PASS" {
		t.Fatalf("mapped project preflight: %+v", inspect)
	}
	executable := filepath.Join(t.TempDir(), "AllocationCore")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", executable, ".")
	cmd.Dir = filepath.Join("..", "..")
	if log, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Core failed: %v %s", err, log)
	}
	result, err := multi.RunContext(context.Background(), dir, filepath.Join(t.TempDir(), "result"), executable)
	if err != nil {
		t.Fatalf("real Core failed: %v", err)
	}
	if result.Status != "PASS" || result.Accounts != 4 || result.Input != "3000.00" || result.Output != "3000.00" {
		t.Fatalf("real mapped business allocation: %+v", result)
	}
	if len(result.Details) != 4 {
		t.Fatalf("expected four account audits got %+v", result.Details)
	}
	for _, account := range result.Details {
		if account.Input != account.Output || account.Status != "PASS" {
			t.Fatalf("per-account conservation: %+v", account)
		}
	}
}
