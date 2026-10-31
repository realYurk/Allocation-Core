package studio

import (
 "bytes"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

type factorEditVersion struct {
 Versions struct {
  Assignments string `json:"assignments"`
  Template string `json:"template"`
  Overrides string `json:"overrides"`
 } `json:"versions"`
}
func readFactorVersion(t *testing.T, app *MultiApp) factorEditVersion {
 t.Helper()
 r:=httptest.NewRecorder()
 app.factor(r,httptest.NewRequest(http.MethodGet,"/api/multi/factor?name=original&account=Cost-C",nil))
 if r.Code!=200 {t.Fatalf("factor GET %d %s",r.Code,r.Body.String())}
 var v factorEditVersion
 if err:=json.Unmarshal(r.Body.Bytes(),&v);err!=nil {t.Fatal(err)}
 if v.Versions.Assignments=="" || v.Versions.Template=="" || v.Versions.Overrides=="" {t.Fatal("missing financial edit tokens")}
 return v
}
func postFactorEdit(t *testing.T, app *MultiApp, v factorEditVersion, shares ...string) *httptest.ResponseRecorder {
 t.Helper()
 rows:=[]factorRow{{Account:"Cost-C",Stage:"双因子分摊",Allocation:"material",Share:shares[0]},
  {Account:"Cost-C",Stage:"双因子分摊",Allocation:"revenue",Share:shares[1]}}
 b,e:=json.Marshal(map[string]any{"name":"original","account":"Cost-C","rows":rows,"versions":v.Versions})
 if e!=nil {t.Fatal(e)}
 r:=httptest.NewRecorder()
 app.factor(r,httptest.NewRequest(http.MethodPost,"/api/multi/factor",bytes.NewReader(b)))
 return r
}
func TestFactorEditorRejectsInvalidStageTotals(t *testing.T) {
 app,dir:=onboardingApp(t)
 initial:=readFactorVersion(t,app)
 path:=filepath.Join(dir,"overrides.csv")
 before,e:=os.ReadFile(path);if e!=nil {t.Fatal(e)}
 for _,shares:=range [][]string{{"0.75","0.50"},{"0.75","0.20"},{"0.333333","0.333333"}}{
  r:=postFactorEdit(t,app,initial,shares...)
  if r.Code!=422 || !strings.Contains(r.Body.String(),"100%") {t.Fatalf("invalid percentages accepted: %v / %d / %s",shares,r.Code,r.Body.String())}
  got,e:=os.ReadFile(path);if e!=nil {t.Fatal(e)}
  if !bytes.Equal(before,got){t.Fatal("failed save modified financial override file")}
 }
 r:=postFactorEdit(t,app,initial,"0.60","0.40")
 if r.Code!=200 {t.Fatalf("valid exact split rejected: %d %s",r.Code,r.Body.String())}
 saved,e:=factorRead(path);if e!=nil {t.Fatal(e)}
 count:=0
 for _,row:=range saved{if row.Account=="Cost-C"{
  count++
  if row.Allocation=="material" && row.Share!="0.60" {t.Fatal(row)}
  if row.Allocation=="revenue" && row.Share!="0.40" {t.Fatal(row)}
 }}
 if count!=2 {t.Fatalf("expected 2 account rows, got %d",count)}
}
func TestFactorEditorStaleTabsDoNotOverwrite(t *testing.T) {
 app,dir:=onboardingApp(t)
 initial:=readFactorVersion(t,app)
 if r:=postFactorEdit(t,app,initial,"0.50","0.50");r.Code!=200 {t.Fatalf("first save: %d %s",r.Code,r.Body.String())}
 path:=filepath.Join(dir,"overrides.csv")
 saved,e:=os.ReadFile(path);if e!=nil {t.Fatal(e)}
 if r:=postFactorEdit(t,app,initial,"0.10","0.90");r.Code!=409 {t.Fatalf("stale override was accepted: %d %s",r.Code,r.Body.String())}
 after,e:=os.ReadFile(path);if e!=nil {t.Fatal(e)}
 if !bytes.Equal(saved,after) {t.Fatal("stale tab overwrote newer override")}
 fresh:=readFactorVersion(t,app)
 if r:=postFactorEdit(t,app,fresh,"0.25","0.75");r.Code!=200 {t.Fatalf("fresh token rejected: %d %s",r.Code,r.Body.String())}
}
func TestFactorEditorRejectsChangedAssignmentAndTemplate(t *testing.T) {
 app,dir:=onboardingApp(t)
 original:=readFactorVersion(t,app)
 path:=filepath.Join(dir,"assignments.csv")
 b,e:=os.ReadFile(path);if e!=nil {t.Fatal(e)}
 if e:=os.WriteFile(path,append(b,byte('\n')),0600);e!=nil {t.Fatal(e)}
 if r:=postFactorEdit(t,app,original,"0.50","0.50");r.Code!=409 {t.Fatalf("stale account assignment accepted: %d %s",r.Code,r.Body.String())}
 fresh:=readFactorVersion(t,app)
 path=filepath.Join(dir,"templates","dual.json")
 b,e=os.ReadFile(path);if e!=nil {t.Fatal(e)}
 if e:=os.WriteFile(path,append(b,byte('\n')),0600);e!=nil {t.Fatal(e)}
 if r:=postFactorEdit(t,app,fresh,"0.50","0.50");r.Code!=409 {t.Fatalf("stale template accepted: %d %s",r.Code,r.Body.String())}
}
