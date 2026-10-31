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
	req := httptest.NewRequest(http.MethodPost, "/api/multi/driver-import"