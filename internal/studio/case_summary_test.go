package studio

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func financeSummaryCase(t *testing.T, precision int, files map[string]string) (*Application, string) {
	t.Helper()
	root := t.TempDir()
	caseDir := filepath.Join(root, "examples", "sample")
	if err := os.MkdirAll(caseDir, 0700); err != nil {
		t.Fatal(err)
	}
	var rule GenericRule
	rule.Name = "Case summary financial consistency"
	rule.Precision = precision
	rule.Dimensions = []string{"period", "account"}
	rule.Stages = []map[string]any{{"id": "test"}}
	rule.Datasets.Amounts = map[string]string{}
	for k, content := range files {
		name := k + ".csv"
		rule.Datasets.Amounts[k] = name
		if err := os.WriteFile(filepath.Join(caseDir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "rule.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return &Application{data: root, custom: t.TempDir()}, caseDir
}

func getCaseSummary(t *testing.T, a *Application) CaseSummary {
	t.Helper()
	rec := httptest.NewRecorder()
	a.cases(rec, httptest.NewRequest(http.MethodGet, "/api/cases", nil))
	if rec.Code != 200 {
		t.Fatalf("GET cases status %d: %s", rec.Code, rec.Body.String())
	}
	var output struct {
		Cases []CaseSummary `json:"cases"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Cases) != 1 {
		t.Fatalf("expected one case, got %+v", output.Cases)
	}
	return output.Cases[0]
}

// Reproduces the audit's observed 501-row defect, including the actual HTTP contract.
func TestCasesSummaryReadsAllRowsNotJustPreview(t *testing.T) {
	var b strings.Builder
	writer := csv.NewWriter(&b)
	if err := writer.Write([]string{"period", "account", "amount"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 501; i++ {
		account := "Cost-A"
		amount := "1.00"
		if i == 500 {
			account = "Cost-B"
			amount = "100.00"
		}
		if err := writer.Write([]string{"2026-01", account, amount}); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	a, _ := financeSummaryCase(t, 2, map[string]string{"a": b.String()})
	got := getCaseSummary(t, a)
	if !got.SummaryComplete || got.Amount != "600.00" || got.AccountCount != 2 || got.SourceRows != 501 || got.Period != "2026-01" {
		t.Fatalf("partial overview or wrong monetary precision: %+v", got)
	}
	rec := httptest.NewRecorder()
	a.caseDetail(rec, httptest.NewRequest(http.MethodGet, "/api/cases/sample", nil))
	if rec.Code != 200 {
		t.Fatalf("detail status=%d %s", rec.Code, rec.Body.String())
	}
	var detail struct {
		Sources   []map[string]string `json:"sources"`
		Truncated bool                `json:"sources_truncated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Sources) != 500 || !detail.Truncated {
		t.Fatalf("preview lacks truncation marker: %d %+v", len(detail.Sources), detail.Truncated)
	}
}

func TestCaseSummaryExactSignedPrecisionMultipleInputs(t *testing.T) {
	a, _ := financeSummaryCase(t, 6, map[string]string{
		"a": "period,account,amount\n2026-01,Cost-A,9007199254.740991\n2026-01,Cost-B,-9007199254.740990\n",
		"b": "period,account,amount\n2026-02,Cost-C,0.000001\n",
	})
	got := getCaseSummary(t, a)
	if !got.SummaryComplete || got.Amount != "0.000002" || got.AccountCount != 3 || got.SourceRows != 3 {
		t.Fatalf("precision drift: %+v", got)
	}
}

func TestCaseSummaryRejectsMalformedAndOverflowInsteadOfPartialAmount(t *testing.T) {
	cases := []struct {
		label     string
		precision int
		content   string
		expect    string
	}{
		{"invalid_decimal", 2, "period,account,amount\n2026-01,Cost-A,10.00\n2026-01,Cost-B,1.001\n", "decimal scale"},
		{"malformed_csv", 2, "period,account,amount\n2026-01,Cost-A,1.00\n\"unterminated\n", "quoted-field"},
		{"int64_overflow", 0, fmt.Sprintf("period,account,amount\n2026-01,Cost-A,%d\n2026-01,Cost-B,1\n", int64(math.MaxInt64)), "overflows"},
		{"duplicate_header", 2, "period,account,amount,amount\n2026-01,Cost-A,1.00,2.00\n", "duplicate"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			a, _ := financeSummaryCase(t, tc.precision, map[string]string{"a": tc.content})
			got := getCaseSummary(t, a)
			if got.SummaryComplete || got.Amount != "" || got.AccountCount != 0 || got.SourceRows != 0 || !strings.Contains(got.SummaryError, tc.expect) {
				t.Fatalf("partial/invalid values misrepresented as complete: %+v", got)
			}
		})
	}
}
