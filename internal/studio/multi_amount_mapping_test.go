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
		