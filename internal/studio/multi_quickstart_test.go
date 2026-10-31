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
func TestQuickTemplateGuideReadOnlyAndBusinessMeaning(t *testing.T){
 x,project:=onboardingApp(t)
 before,err:=os.ReadFile(filepath.Join(project,"assignments.csv"));if err!=nil{t.Fatal(err)}
 w:=httptest.NewRecorder()
 x.templateGuide(w,httptest.NewRequest(http.MethodGet,"/api/multi/template-guide?name=original&template=dual.json",nil))
 if w.Code!=200{t.Fatalf("guide: %d %s",w.Code,w.Body.String())}
 var guide quickTemplate
 if err:=json.Unmarshal(w.Body.Bytes(),&guide);err!=nil{t.Fatal(err)}
 if guide.Template!="dual.json"||guide.Precision!=2||len(guide.Stages)!=1||len(guide.Stages[0].Drivers)!=2 {
  t.Fatalf("incorrect guide: %+v",guide)
 }
 drivers:=guide.Stages[0].Drivers
 if drivers[0].Percent!="75%"||drivers[1].Percent!="25%"||drivers[0].Field!="material"{
  t.Fatalf("wrong ratios: %+v",drivers)
 }
 if len(guide.Warnings)!=0{t.Fatalf("unexpected warnings: %+v",guide.Warnings)}
 if strings.Contains(w.Body.String(),project)||strings.Contains(w.Body.String(),"Cost-A"){t.Fatal("guide disclosed project path or account values")}
 after,err:=os.ReadFile(filepath.Join(project,"assignments.csv"))
 if err!=nil||string(after)!=string(before){t.Fatal("read-only guide modified assignments")}
}
func TestQuickTemplateGuideRejectsBadRequestsAndInvalidRules(t *testing.T){
 x,project:=onboardingApp(t)
 for _,tc:=range []struct{url,method string;want int}{
  {"/api/multi/template-guide?name=original&template=dual.json","POST",405},
  {"/api/multi/template-guide?name=original&template=../secret.json","GET",422},
  {"/api/multi/template-guide?name=original&template=missing.json","GET",422},
  {"/api/multi/template-guide?name=../wrong&template=dual.json","GET",404},
 }{
  w:=httptest.NewRecorder();x.templateGuide(w,httptest.NewRequest(tc.method,tc.url,nil))
  if w.Code!=tc.want{t.Errorf("%s %s got=%d want=%d body=%s",tc.method,tc.url,w.Code,tc.want,w.Body.String())}
 }
 original,err:=os.ReadFile(filepath.Join(project,"templates","dual.json"));if err!=nil{t.Fatal(err)}
 bad:=strings.Replace(string(original),`"share": "0.75"`,`"share": "0.80"`,1)
 if err:=os.WriteFile(filepath.Join(project,"templates","invalid.json"),[]byte(bad),0600);err!=nil{t.Fatal(err)}
 w:=httptest.NewRecorder();x.templateGuide(w,httptest.NewRequest("GET","/api/multi/template-guide?name=original&template=invalid.json",nil))
 if w.Code!=422{t.Fatalf("invalid financial shares accepted: %d %s",w.Code,w.Body.String())}
}
func TestQuickTemplatePercentExactAndSignedRisk(t *testing.T){
 checks:=map[string]string{"":"100%","0":"0%","1":"100%","0.75":"75%","0.000001":"0.0001%","0.333333":"33.3333%"}
 for source,want:=range checks {
  got,err:=exactPercent(source)
  if err!=nil||got!=want{t.Errorf("%q: got %q want %q error %v",source,got,want,err)}
 }
 for _,source:=range []string{"1.01","-0.01","foo","0.0000001"}{
  if _,err:=exactPercent(source);err==nil{t.Errorf("accepted %q",source)}
 }
 x,project:=onboardingApp(t)
 path:=filepath.Join(project,"templates","dual.json")
 raw,err:=os.ReadFile(path);if err!=nil{t.Fatal(err)}
 updated:=strings.Replace(string(raw),`"negative_driver_policy": "REJECT"`,`"negative_driver_policy": "ALLOW_SIGNED", "signed_driver_reason": "credit reversal"`,1)
 if err:=os.WriteFile(path,[]byte(updated),0600);err!=nil{t.Fatal(err)}
 w:=httptest.NewRecorder();x.templateGuide(w,httptest.NewRequest("GET","/api/multi/template-guide?name=original&template=dual.json",nil))
 if w.Code!=200{t.Fatalf("signed: %d %s",w.Code,w.Body.String())}
 var guide quickTemplate
 if err:=json.Unmarshal(w.Body.Bytes(),&guide);err!=nil{t.Fatal(err)}
 if len(guide.Warnings)!=1||!strings.Contains(guide.Warnings[0],"credit reversal"){
  t.Fatalf("missing signed factor risk: %+v",guide.Warnings)
 }
}
