package studio

import (
	"allocationcore/internal/engine"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDriverContractDistinguishesMissingDataAndSchemaReadiness(t *testing.T) {
	for _, tc := range []struct{ name, body, status string }{
		{"missing", "", "MISSING_FILE"},
		{"missing-target", "period,units\n2026-01,5\n", "MISSING_COLUMNS"},
		{"empty-template", "sku,units\n", "HEADER_ONLY"},
		{"duplicate-header", "sku,units,units\nA,1,1\n", "INVALID_HEADER"},
		{"broadcast", "sku,units\nA,PRIVATE_WEIGHT_SENTINEL\n", "SCOPE_REVIEW"},
		{"all-bindings", "period,department,sku,account,currency,units\n2026-01,Shared,A,Account-X,CUR,5\n", "SCHEMA_READY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x, project, _, _ := designerBridgeFixture(t)
			file := filepath.Join(project, "drivers", "units.csv")
			if tc.body == "" {
				if e := os.Remove(file); e != nil {
					t.Fatal(e)
				}
			} else if e := os.WriteFile(file, []byte(tc.body), 0600); e != nil {
				t.Fatal(e)
			}
			w := httptest.NewRecorder()
			x.driverNeeds(w, httptest.NewRequest(http.MethodGet, "/api/multi/driver-needs?name=financial&template=base.json", nil))
			if w.Code != 200 {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
			var r driverRequirements
			if e := json.Unmarshal(w.Body.Bytes(), &r); e != nil {
				t.Fatal(e)
			}
			n := r.Drivers[0]
			if n.Status != tc.status || !reflect.DeepEqual(n.RequiredDimensions, []string{"sku"}) || len(n.Uses) != 1 || n.NextStep == "" {
				t.Fatalf("bad contract %+v", n)
			}
			if strings.Contains(w.Body.String(), "PRIVATE_WEIGHT_SENTINEL") {
				t.Fatal("data record leaked into schema response")
			}
			if tc.status == "MISSING_COLUMNS" && !reflect.DeepEqual(n.MissingColumns, []string{"sku"}) {
				t.Fatalf("wrong missing dimensions: %v", n.MissingColumns)
			}
		})
	}
}

func TestDriverContractMissingFolderNeedsRealExport(t *testing.T) {
	need := driverNeed{CurrentFile: "weights.csv", Uses: []driverUse{{Stage: "S"}}}
	inspectDriverHeader(t.TempDir(), &need)
	if need.Status != "MISSING_FILE" || !strings.Contains(need.NextStep, "原系统") {
		t.Fatalf("incorrect missing folder guidance: %+v", need)
	}
}

func TestDriverHeaderDownloadIsEmptyVersionedAndReadOnly(t *testing.T) {
	x, project, _, _ := designerBridgeFixture(t)
	req := driverRequirementsForTest(t, x)
	before, e := os.ReadFile(filepath.Join(project, "drivers", "units.csv"))
	if e != nil {
		t.Fatal(e)
	}
	q := url.Values{"name": {"financial"}, "template": {"base.json"}, "alias": {"units"}, "download": {"template"}, "base_sha256": {req.SHA256}}
	w := httptest.NewRecorder()
	x.driverNeeds(w, httptest.NewRequest(http.MethodGet, "/api/multi/driver-needs?"+q.Encode(), nil))
	if w.Code != 200 {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff")))
	head, e := reader.Read()
	if e != nil {
		t.Fatal(e)
	}
	columns, e := engine.HeaderPositions(head)
	if e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"period", "department", "account", "currency", "sku", "units"} {
		if _, ok := columns[field]; !ok {
			t.Fatalf("template omitted %s", field)
		}
	}
	if _, e = reader.Read(); e != io.EOF {
		t.Fatal("template contains data rows")
	}
	after, e := os.ReadFile(filepath.Join(project, "drivers", "units.csv"))
	if e != nil || string(before) != string(after) {
		t.Fatal("download mutated source")
	}
	q.Set("base_sha256", "stale")
	w = httptest.NewRecorder()
	x.driverNeeds(w, httptest.NewRequest(http.MethodGet, "/api/multi/driver-needs?"+q.Encode(), nil))
	if w.Code != 409 {
		t.Fatal("stale template was silently downloaded")
	}
	q.Set("base_sha256", req.SHA256)
	q.Set("alias", "../escape")
	w = httptest.NewRecorder()
	x.driverNeeds(w, httptest.NewRequest(http.MethodGet, "/api/multi/driver-needs?"+q.Encode(), nil))
	if w.Code != 422 {
		t.Fatal("unknown alias accepted")
	}
}

func TestDriverImportBlocksMissingTargetBeforePublishing(t *testing.T) {
	x, project, _, _ := designerBridgeFixture(t)
	req := driverRequirementsForTest(t, x)
	result := postDriverCSV(t, x, "missing_target.csv", req.SHA256, "年月,数量\n2026-01,1\n", map[string]string{"period": "年月", "units": "数量"})
	if result.Code != 422 || !strings.Contains(result.Body.String(), "sku") {
		t.Fatalf("missing required target imported: %d %s", result.Code, result.Body.String())
	}
	if _, e := os.Stat(filepath.Join(project, "drivers", "missing_target.csv")); !os.IsNotExist(e) {
		t.Fatal("invalid import published a file")
	}
}

func TestDriverContractIncludesFilterColumnsAndSkipsDisabledUses(t *testing.T) {
	n := driverNeed{Alias: "d"}
	off := false
	spec := engine.Spec{Dimensions: []string{"period", "item", "region"}, Stages: []engine.Stage{
		{ID: "active", Target: map[string]engine.Target{"period": {Mode: "SOURCE"}, "item": {Mode: "INCLUDE"}, "region": {Mode: "ELEMENT"}}, Allocations: []engine.Allocation{{ID: "a", Share: "1", DriverDataset: "d", DriverFilters: map[string]engine.Filter{"region": {Include: []string{"R"}}}}}},
		{ID: "disabled", Enabled: &off, Target: map[string]engine.Target{"period": {Mode: "INCLUDE"}}, Allocations: []engine.Allocation{{DriverDataset: "d"}}},
	}}
	populateDriverContract(&n, spec)
	if !reflect.DeepEqual(n.RequiredDimensions, []string{"item", "region"}) || !reflect.DeepEqual(n.SourceDimensions, []string{"period"}) || len(n.Uses) != 1 {
		t.Fatalf("bad derived requirements: %+v", n)
	}
}
