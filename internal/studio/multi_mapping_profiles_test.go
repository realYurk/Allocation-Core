package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func profileTestApp(t *testing.T) *MultiApp {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "multi")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	return &MultiApp{root: root, busy: map[string]bool{}}
}
func requestProfile(t *testing.T, x *MultiApp, method string, p any) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if p != nil {
		var err error
		body, err = json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	x.mappingProfiles(rec, httptest.NewRequest(method, "/api/multi/mapping-profiles", bytes.NewReader(body)))
	return rec
}
func goodAmountProfile(name string) mappingProfile {
	return mappingProfile{Schema: mappingProfileSchema, Name: name, Kind: "amount",
		Dimensions: []string{"period", "department", "sku", "account", "currency"}, Precision: 2,
		Columns: map[string]string{"period": "月份", "department": "成本中心", "sku": "产品", "account": "科目", "currency": "币种", "amount": "费用", "row_id": "单据号"}}
}
func goodDriverProfile(name string) mappingProfile {
	return mappingProfile{Schema: mappingProfileSchema, Name: name, Kind: "driver",
		Dimensions:   []string{"period", "department", "sku", "account", "currency"},
		WeightFields: []string{"units", "hours"}, Columns: map[string]string{
			"period": "月份", "department": "部门", "sku": "商品", "units": "产量", "hours": "工时"}}
}
func TestMappingProfilePersistsAcrossProjectsAndRestarts(t *testing.T) {
	app := profileTestApp(t)
	p := goodAmountProfile("finance_common")
	if r := requestProfile(t, app, "POST", p); r.Code != 201 {
		t.Fatalf("save %d %s", r.Code, r.Body.String())
	}
	// The mapping is not stored under any project, so it can be reused in
	// another scenario and still exists when the local Studio is restarted.
	newInstance := &MultiApp{root: app.root, busy: map[string]bool{}}
	r := requestProfi