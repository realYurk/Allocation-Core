package studio

import (
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "testing"
)

func TestAccountPlanAPIReadOnlyAndIsolated(t *testing.T){
 root:=t.TempDir();project:=filepath.Join(root,"finance")
 for _,folder:=range []string{project,filepath.Join(project,"templates"),filepath.Join(project,"drivers")} {
  if err:=os.MkdirAll(folder,0700);err!=nil{t.Fatal(err)}
 }
 template,err:=os.ReadFile(filepath.Join("..","..","testdata","multi_sample","templates","dual.json"))
 if err!=nil{t.Fatal(err)}
 sources:=map[string][]byte{
  "templates/dual.json":template,
  "project.json":[]byte(`{"amounts":"amounts.csv","assignments":"assignments.csv","overrides":"overrides.csv","templates":"templates","drivers":"drivers","workers":2,"jobs":2,"disk_partitions":8,"keep_group_outputs":true}`),
  "assignments.csv":[]byte("account,template\nCost-A,dual.json\n"),
  "overrides.csv":[]byte("account,stage,allocation,driver_dataset,weight_field,share\n"),
  "drivers/material.csv":[]byte("sku,material\nItem-A,3\n"),
  "drivers/revenue.csv":[]byte("sku,revenue\nItem-A,7\n"),
 }
 for path,data:=range sources{
  if err:=os.WriteFile(filepath.Join(project,filepath.FromSlash(path)),data,0600);err!=nil{t.Fatal(err)}
 }
 x:=&MultiApp{root:root}
 send:=func(method,url string)*httptest.ResponseRecorder{
  w:=httptest.NewRecorder()
  x.accountPlan(w,httptest.NewRequest(method,url,nil))
  return w
 }
 ok:=send(http.MethodGet,"/api/multi/account-plan?name=finance&account=Cost-A")
 if ok.Code!=200{t.Fatalf("status %d: %s",ok.Code,ok.Body.String())}
 var result struct{
  Account string `json:"account"`
  Template string `json:"template"`
  RuleSHA256 string `json:"rule_sha256"`
  Stages []any `json:"stages"`
 }
 if err:=json.Unmarshal(ok.Body.Bytes(),&result);err!=nil{t.Fatal(err)}
 if result.Account!="Cost-A"||result.Template!="dual.json"||result.RuleSHA256==""||len(result.Stages)!=1{
  t.Fatalf("unexpected result: %+v",result)
 }
 if send(http.MethodGet,"/api/multi/account-plan?name=finance&account=unknown").Code!=422{t.Fatal("unknown account accepted")}
 if send(http.MethodGet,"/api/multi/account-plan?name=../finance&account=Cost-A").Code!=404{t.Fatal("traversal accepted")}
 if send(http.MethodGet,"/api/multi/account-plan?name=finance").Code!=400{t.Fatal("missing account accepted")}
 if send(http.MethodPost,"/api/multi/account-plan?name=finance&account=Cost-A").Code!=405{t.Fatal("mutation verb accepted")}
}
