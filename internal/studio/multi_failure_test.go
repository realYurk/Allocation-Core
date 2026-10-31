package studio
import ("allocationcore/internal/multi";"context";"encoding/json";"net/http";"net/http/httptest";"os";"path/filepath";"strings";"testing")
func TestFailedMultiDiagnosticSavedAndShownAfterRestart(t *testing.T){
 root:=t.TempDir();id:=strings.Repeat("a",24)
 j:=&Job{ID:id,Case:"finance",Output:filepath.Join(root,"multi-jobs",id),Start:"2026-10-31T07:00:00Z",Status:"RUNNING"}
 x:=&MultiApp{parent:&Application{data:root},jobs:map[string]*Job{id:j},busy:map[string]bool{"finance":true},cancels:map[string]context.CancelFunc{}}
 d:=multi.FailureDiagnosis{Code:"DRIVER_NO_MATCH",Title:"没有匹配因子",Scope:"EXACT_POOL",GroupID:strings.Repeat("b",20),AccountCount:1,Accounts:[]string{"Cost-A"},DriverFile:"drivers/material.csv",Pool:map[string]string{"account":"Cost-A"},NextSteps:[]string{"核对 Driver"},Limitations:"仅按本次数据"}
 raw,e:=json.Marshal(d);if e!=nil{t.Fatal(e)}
 x.complete(j,"FAILED","exit status 1","ALLOCATION_FAILURE_V1:"+string(raw)+"\nBLOCK: error\n")
 if j.Diagnosis==nil||j.Diagnosis.Pool["account"]!="Cost-A"||strings.Contains(j.Log,"ALLOCATION_FAILURE"){t.Fatal("missing structured diagnosis or raw machine line leaked")}
 restored,e:=readJournal(filepath.Join(root,"multi-jobs"),id,true)
 if e!=nil||restored.Diagnosis==nil||restored.Diagnosis.Code!="DRIVER_NO_MATCH"{t.Fatalf("journal lost diagnosis: %+v %v",restored,e)}
 x.jobs=map[string]*Job{}
 w:=httptest.NewRecorder()
 x.jobDetail(w,httptest.NewRequest(http.MethodGet,"/api/multi/jobs/"+id,nil))
 if w.Code!=200{t.Fatalf("%d %s",w.Code,w.Body.String())}
 var body struct{Job Job `json:"job"`}
 if e=json.Unmarshal(w.Body.Bytes(),&body);e!=nil||body.Job.Diagnosis==nil||body.Job.Diagnosis.DriverFile!="drivers/material.csv"{t.Fatalf("historical GET lost diagnosis: %+v %v",body,e)}
 if _,e=os.Stat(filepath.Join(root,"multi-jobs",id,"results.csv"));!os.IsNotExist(e){t.Fatal("published failed task")}
}
func TestFailureDiagnosticIgnoresMalformedPayload(t *testing.T){
 d,log:=extractMultiFailure([]byte("ALLOCATION_FAILURE_V1:broken\nBLOCK: failed\n"))
 if d!=nil||strings.Contains(log,"ALLOCATION_FAILURE")||!strings.Contains(log,"BLOCK: failed"){t.Fatalf("malformed payload: %+v, %s",d,log)}
 raw,e:=publicFS.ReadFile("public/multi.html");if e!=nil{t.Fatal(e)}
 if !strings.Contains(string(raw),`id="failureDiagnosis"`)||!strings.Contains(string(raw),"/assets/failure_diagnosis.js"){t.Fatal("UI not connected")}
}
