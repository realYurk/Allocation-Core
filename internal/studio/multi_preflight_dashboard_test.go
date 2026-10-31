package studio

import (
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "testing"
)
func TestFinancePreflightDashboardContract(t *testing.T){
 root:=t.TempDir()
 project:=filepath.Join(root,"finance")
 if err:=copyFixture(filepath.Join("..","..","testdata","multi_sample"),project);err!=nil {t.Fatal(err)}
 x:=&MultiApp{root:root}
 w:=httptest.NewRecorder()
 x.preflight(w,httptest.NewRequest(http.MethodPost,"/api/multi/preflight",strings.NewReader("{\"name\":\"finance\"}")))
 if w.Code!=200 {t.Fatalf("status %d: %s",w.Code,w.Body.String())}
 var report struct {
  Status string `json:"status"`
  Accounts []struct{Account string `json:"account"`} `json:"account_details"`
  Verified bool `json:"data_match_verified"`
 }
 if err:=json.Unmarshal(w.Body.Bytes(),&report);err!=nil {t.Fatal(err)}
 if report.Status!="PASS"||len(report.Accounts)!=4||report.Verified {t.Fatalf("bad report %+v",report)}
 entries,err:=os.ReadDir(project)
 if err!=nil {t.Fatal(err)}
 if len(entries)!=6 {t.Fatalf("inspection modified project: %v",entries)}
 for _,name:=range []string{"public/preflight.js","public/multi.html"} {
  data,err:=publicFS.ReadFile(name);if err!=nil {t.Fatal(err)}
  if name=="public/multi.html"&&!strings.Contains(string(data),"/assets/preflight.js") {t.Fatal("dashboard asset not linked")}
 }
}
