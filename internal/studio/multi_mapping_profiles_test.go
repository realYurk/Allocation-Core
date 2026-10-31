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
	r := requestProfile(t, newInstance, "GET", nil)
	if r.Code != 200 {
		t.Fatalf("list %d %s", r.Code, r.Body.String())
	}
	var list struct {
		Profiles []mappingProfile `json:"profiles"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Profiles) != 1 || list.Profiles[0].Columns["account"] != "科目" {
		t.Fatalf("incorrect shared mapping: %+v", list)
	}
	dest := filepath.Join(t.TempDir(), "amounts.csv")
	input := "月份,成本中心,产品,科目,币种,费用,单据号\n2026-01,Shared,Unassigned,Cost-A,CUR,12.25,ROW-A\n"
	preview, accounts, err := streamMappedOnboardingAmounts(context.Background(), strings.NewReader(input), dest, 2, p.Dimensions, "material.json", list.Profiles[0].Columns)
	if err != nil || preview.Rows != 1 || preview.Total != "12.25" || len(accounts) != 1 || accounts[0] != "Cost-A" {
		t.Fatalf("replayed profile failed: %+v accounts=%v err=%v", preview, accounts, err)
	}
	out, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "period,department,sku,account,currency,amount,row_id\n") {
		t.Fatalf("wrong output contract: %s", out)
	}
	if r := requestProfile(t, newInstance, "POST", p); r.Code != 409 {
		t.Fatalf("duplicate should not overwrite: %d %s", r.Code, r.Body.String())
	}
	if _, err := os.Stat(filepath.Join(app.root, p.Name+".json")); !os.IsNotExist(err) {
		t.Fatal("profile wrongly saved as multi project")
	}
}
func TestMappingProfileDriverPortabilityAndNoSourceFiles(t *testing.T) {
	app := profileTestApp(t)
	p := goodDriverProfile("driver_units_hours")
	if r := requestProfile(t, app, "POST", p); r.Code != 201 {
		t.Fatalf("save %d %s", r.Code, r.Body.String())
	}
	r := requestProfile(t, app, "GET", nil)
	var list struct {
		Profiles []mappingProfile `json:"profiles"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Profiles) != 1 || list.Profiles[0].Columns["units"] != "产量" || list.Profiles[0].Columns["hours"] != "工时" {
		t.Fatalf("driver mapping mismatch %+v", list)
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(app.root), "mapping-profiles"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "driver_units_hours.json" {
		t.Fatalf("unexpected artifacts: %v %v", entries, err)
	}
}
func TestMappingProfileRejectsUnsafeContractsWithoutSideEffects(t *testing.T) {
	cases := map[string]func(*mappingProfile){
		"traversal":           func(p *mappingProfile) { p.Name = "../outside" },
		"unsupported_schema":  func(p *mappingProfile) { p.Schema = "other" },
		"duplicate_source":    func(p *mappingProfile) { p.Columns["account"] = "费用" },
		"unknown_field":       func(p *mappingProfile) { p.Columns["unmapped"] = "秘密" },
		"missing_account":     func(p *mappingProfile) { delete(p.Columns, "account") },
		"invalid_precision":   func(p *mappingProfile) { p.Precision = 7 },
		"empty_dimension":     func(p *mappingProfile) { p.Dimensions[0] = "" },
		"duplicate_dimension": func(p *mappingProfile) { p.Dimensions[0] = "sku" },
		"embedded_separator":  func(p *mappingProfile) { p.Columns["period"] = "a\nb" },
	}
	for name, mutation := range cases {
		t.Run(name, func(t *testing.T) {
			app := profileTestApp(t)
			p := goodAmountProfile("valid_name")
			mutation(&p)
			r := requestProfile(t, app, "POST", p)
			if r.Code != 422 {
				t.Fatalf("invalid mapping accepted: %d %s", r.Code, r.Body.String())
			}
			listed := requestProfile(t, app, "GET", nil)
			if !strings.Contains(listed.Body.String(), `"profiles":[]`) {
				t.Fatalf("invalid save persisted: %s", listed.Body.String())
			}
		})
	}
	p := goodDriverProfile("driver_test")
	delete(p.Columns, "hours")
	if err := validateMappingProfile(p); err == nil {
		t.Fatal("driver missing weight field must fail")
	}
	p = goodDriverProfile("driver_test")
	p.Columns["unknown"] = "Other"
	if err := validateMappingProfile(p); err == nil {
		t.Fatal("driver unknown canonical field must fail")
	}
}
func TestMappingProfileConcurrentCreateNoOverwrite(t *testing.T) {
	app := profileTestApp(t)
	var wg sync.WaitGroup
	statuses := make(chan int, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); statuses <- requestProfile(t, app, "POST", goodAmountProfile("shared")).Code }()
	}
	wg.Wait()
	close(statuses)
	success, conflict := 0, 0
	for status := range statuses {
		switch status {
		case 201:
			success++
		case 409:
			conflict++
		default:
			t.Errorf("unexpected HTTP %d", status)
		}
	}
	if success != 1 || conflict != 11 {
		t.Fatalf("successful=%d conflicts=%d", success, conflict)
	}
}
