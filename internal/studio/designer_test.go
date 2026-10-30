package studio

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func designerFixture(t *testing.T) (*Application, string) {
	t.Helper()
	root := t.TempDir()
	custom := filepath.Join(root, "cases")
	home := filepath.Join(root, "app")
	for _, dir := range []string{custom, filepath.Join(home, "examples", "example")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(home, "examples", "example")
	rule := `{"schema":"allocation-ir/2.0","name":"example","precision":2,"dimensions":["period","sku"],"datasets":{"amounts":{"expenses":"amounts.csv"},"drivers":{"units":"units.csv"}},"stages":[{"id":"step1","inputs":[{"from":"expenses","share":"1"}],"source":{"grouping":{"period":"AS_IS","sku":"TOTAL"},"filters":{}},"target":{"period":{"mode":"SOURCE"},"sku":{"mode":"INCLUDE","include":["A","B"]}},"conservation_keys":["period"],"allocations":[{"id":"units","share":"1","driver_dataset":"units","weight_field":"units","negative_driver_policy":"REJECT"}]}]}`
	for file, content := range map[string]string{"rule.json": rule, "amounts.csv": "period,sku,amount\n2026-01,SOURCE,10.00\n", "units.csv": "period,sku,units\n2026-01,A,3\n2026-01,B,1\n"} {
		if err := os.WriteFile(filepath.Join(source, file), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a := &Application{home: home, data: home, custom: custom, mu: sync.RWMutex{}, jobs: map[string]*Job{}}
	return a, rule
}
func TestDesignerClonesExampleWithoutModifyingIt(t *testing.T) {
	a, orig := designerFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/designer/clone", strings.NewReader(`{"from":"example","name":"my_plan"}`))
	rr := httptest.NewRecorder()
	a.designerClone(rr, req)
	if rr.Code != 201 {
		t.Fatalf("clone failed %d %s", rr.Code, rr.Body.String())
	}
	copyRule, e := os.ReadFile(filepath.Join(a.custom, "my_plan", "rule.json"))
	if e != nil || string(copyRule) != orig {
		t.Fatalf("cloned rule differs: %v", e)
	}
	exampleRule, _ := os.ReadFile(filepath.Join(a.home, "examples", "example", "rule.json"))
	if string(exampleRule) != orig {
		t.Fatal("source example was modified")
	}
	for _, name := range []string{"../escape", "space name", "x/x", "bad.csv"} {
		b, _ := json.Marshal(map[string]string{"from": "example", "name": name})
		r := httptest.NewRecorder()
		a.designerClone(r, httptest.NewRequest("POST", "/api/designer/clone", bytes.NewReader(b)))
		if r.Code == 201 {
			t.Fatalf("invalid name accepted %q", name)
		}
	}
}
func TestDesignerRejectsUnsafeRuleAndDataPaths(t *testing.T) {
	valid := `{"schema":"allocation-ir/2.0","datasets":{"amounts":{"expenses":"a.csv"},"drivers":{}},"stages":[{"id":"step"}]}`
	if _, e := validateRuleBytes(json.RawMessage(valid)); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{
		`{"schema":"other","datasets":{},"stages":[{}]}`,
		`{"schema":"allocation-ir/2.0","datasets":{"amounts":{"expenses":"../outside.csv"}},"stages":[{}]}`,
		`{"schema":"allocation-ir/2.0","datasets":{},"stages":[]}`,
		`{"schema":"allocation-ir/2.0","datasets":{},"stages":[{}]} trailing`,
	} {
		if _, e := validateRuleBytes(json.RawMessage(raw)); e == nil {
			t.Fatalf("unsafe rule accepted %s", raw)
		}
	}
}
func TestDesignerSaveNeedsFreshHashAndNoRunningJobs(t *testing.T) {
	a, _ := designerFixture(t)
	a.jobs["job"] = &Job{Case: "user_my_plan", Status: "RUNNING"}
	if !a.caseBusy("user_my_plan") {
		t.Fatal("running job should block edits")
	}
	a.jobs["job"].Status = "PASS"
	if a.caseBusy("user_my_plan") {
		t.Fatal("completed job should not block edits")
	}
}
