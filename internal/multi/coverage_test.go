package multi

import (
 "context"
 "encoding/json"
 "os"
 "path/filepath"
 "strings"
 "testing"
)
func coverageAccount(r CoverageReport,account string) CoverageAccount {
 for _,a:=range r.Accounts {if a.Account==account {return a}}
 return CoverageAccount{Status:"MISSING"}
}
func TestCoverageMatchedReadOnly(t *testing.T) {
 dir:=preflightFixture(t);path:=filepath.Join(dir,"amounts.csv")
 before,e:=os.ReadFile(path);if e!=nil {t.Fatal(e)}
 report:=InspectDriverCoverage(context.Background(),dir)
 if report.Status!="SOURCE_MATCHED"||report.BlockedPairs!=0||report.UnverifiedAccounts!=0||report.CheckedPairs<4 {t.Fatalf("unexpected source-pool coverage: %+v",report)}
 for _,a:=range []string{"Cost-A","Cost-B","Cost-C","Cost-D"} {
  if coverageAccount(report,a).Status!="SOURCE_MATCHED" {t.Fatalf("unmatched %s: %+v",a,report)}
 }
 after,e:=os.ReadFile(path);if e!=nil||string(after)!=string(before) {t.Fatal("inspection mutated source amounts")}
 if files,_:=os.ReadDir(dir);len(files)!=6 {t.Fatal("inspection wrote run outputs")}
}
func TestCoverageFindsMismatchedPeriodAndZeroDenominator(t *testing.T) {
 for _,tc:=range []struct{name,driver,why string}{
  {"wrong-period","period,department,sku,material\n2025-12,SharedPool,Item-A,8\n2025-12,SharedPool,Item-B,2\n","没有符合"},
  {"zero-denominator","period,department,sku,material\n2026-01,SharedPool,Item-A,0\n2026-01,SharedPool,Item-B,0\n","不大于零"},
 } {
  t.Run(tc.name,func(t *testing.T){
   dir:=preflightFixture(t)
   if e:=os.WriteFile(filepath.Join(dir,"drivers","material.csv"),[]byte(tc.driver),0600);e!=nil {t.Fatal(e)}
   report:=InspectDriverCoverage(context.Background(),dir)
   if report.Status!="BLOCK"||report.BlockedPairs==0 {t.Fatalf("mismatch accepted: %+v",report)}
   found:=false
   for _,f:=range report.Findings {
    if f.Level=="BLOCK"&&f.Account!=""&&f.Pool["period"]=="2026-01"&&strings.Contains(f.Reason,tc.why) {found=true}
   }
   if !found {t.Fatalf("did not report exact source-pool context: %+v",report.Findings)}
  })
 }
}
func TestCoverageNegativeWeightRejectedIsBlock(t *testing.T) {
 dir:=preflightFixture(t)
 data:="period,department,sku,material\n2026-01,SharedPool,Item-A,8\n2026-01,SharedPool,Item-B,-1\n"
 if e:=os.WriteFile(filepath.Join(dir,"drivers","material.csv"),[]byte(data),0600);e!=nil {t.Fatal(e)}
 report:=InspectDriverCoverage(context.Background(),dir)
 if report.Status!="BLOCK"||report.BlockedPairs==0 {t.Fatalf("negative weights forbidden by rule must BLOCK: %+v",report)}
 found:=false
 for _,f:=range report.Findings {if f.Level=="BLOCK"&&strings.Contains(f.Reason,"负权重") {found=true}}
 if !found {t.Fatalf("negative-weight policy failure missing: %+v",report.Findings)}
}
func TestCoverageMultistageNotVerified(t *testing.T) {
 dir:=preflightFixture(t);path:=filepath.Join(dir,"templates","material.json")
 raw,e:=os.ReadFile(path);if e!=nil {t.Fatal(e)}
 var doc map[string]any;if e=json.Unmarshal(raw,&doc);e!=nil {t.Fatal(e)}
 stages:=doc["stages"].([]any);first:=stages[0].(map[string]any)
 next:=map[string]any{"id":"Second","operation":"PASS","inputs":[]any{map[string]any{"from":first["id"],"share":"1"}},"source":first["source"],"target":first["target"],"conservation_keys":first["conservation_keys"]}
 doc["stages"]=append(stages,next);updated,e:=json.Marshal(doc);if e!=nil {t.Fatal(e)}
 if e=os.WriteFile(path,updated,0600);e!=nil {t.Fatal(e)}
 report:=InspectDriverCoverage(context.Background(),dir)
 if report.Status!="PARTIAL"||coverageAccount(report,"Cost-A").Status!="ROOT_MATCHED"||report.UnverifiedAccounts!=1 {t.Fatalf("direct root checked but downstream unverified: %+v",report)}
 if coverageAccount(report,"Cost-A").CheckedPairs==0 {t.Fatal("first stage not checked")}
 if len(report.Findings)==0||!strings.Contains(report.Findings[0].Reason,"后续") {t.Fatal("missing downstream limit")}
}
func TestCoverageCancelCannotPass(t *testing.T) {
 dir:=preflightFixture(t);ctx,cancel:=context.WithCancel(context.Background());cancel()
 if report:=InspectDriverCoverage(ctx,dir);report.Status=="SOURCE_MATCHED" {t.Fatal("canceled inspection passed")}
}


func TestCoverageMultiStageRootDriverFailure(t *testing.T) {
 dir:=preflightFixture(t)
 path:=filepath.Join(dir,"templates","material.json")
 raw,err:=os.ReadFile(path);if err!=nil {t.Fatal(err)}
 var doc map[string]any;if err=json.Unmarshal(raw,&doc);err!=nil {t.Fatal(err)}
 stages:=doc["stages"].([]any)
 first:=stages[0].(map[string]any)
 doc["stages"]=append(stages,map[string]any{
  "id":"FollowUp","operation":"PASS",
  "inputs":[]any{map[string]any{"from":first["id"],"share":"1"}},
  "source":first["source"],"target":first["target"],
  "conservation_keys":first["conservation_keys"],
 })
 b,err:=json.Marshal(doc);if err!=nil {t.Fatal(err)}
 if err=os.WriteFile(path,b,0600);err!=nil {t.Fatal(err)}
 driver:="period,department,sku,material\n2025-12,SharedPool,Item-A,8\n2025-12,SharedPool,Item-B,2\n"
 if err=os.WriteFile(filepath.Join(dir,"drivers","material.csv"),[]byte(driver),0600);err!=nil {t.Fatal(err)}
 r:=InspectDriverCoverage(context.Background(),dir)
 if r.Status!="BLOCK"||r.BlockedPairs==0||coverageAccount(r,"Cost-A").Status!="BLOCK" {t.Fatalf("root failure missing: %+v",r)}
 found:=false
 for _,f:=range r.Findings {if f.Level=="BLOCK"&&f.Pool["period"]=="2026-01" {found=true}}
 if !found {t.Fatalf("lost root coordinate: %+v",r.Findings)}
}

func TestCoverageMultiRootIsUnverified(t *testing.T) {
 dir:=preflightFixture(t)
 path:=filepath.Join(dir,"templates","material.json")
 raw,err:=os.ReadFile(path);if err!=nil {t.Fatal(err)}
 var doc map[string]any;if err=json.Unmarshal(raw,&doc);err!=nil {t.Fatal(err)}
 stages:=doc["stages"].([]any)
 first:=stages[0].(map[string]any)
 first["inputs"]=[]any{map[string]any{"from":"expenses","share":"0.5"}}
 doc["stages"]=append(stages,map[string]any{
  "id":"OtherRoot","operation":"PASS",
  "inputs":[]any{map[string]any{"from":"expenses","share":"0.5"}},
  "source":first["source"],"target":first["target"],
  "conservation_keys":first["conservation_keys"],
 })
 b,err:=json.Marshal(doc);if err!=nil {t.Fatal(err)}
 if err=os.WriteFile(path,b,0600);err!=nil {t.Fatal(err)}
 r:=InspectDriverCoverage(context.Background(),dir)
 if r.Status=="SOURCE_MATCHED"||coverageAccount(r,"Cost-A").Status!="UNVERIFIED" {t.Fatalf("multi-root false pass: %+v",r)}
}

func TestCoverageSignedDriverReview(t *testing.T) {
 dir:=preflightFixture(t)
 path:=filepath.Join(dir,"templates","material.json")
 raw,err:=os.ReadFile(path);if err!=nil {t.Fatal(err)}
 var doc map[string]any;if err=json.Unmarshal(raw,&doc);err!=nil {t.Fatal(err)}
 stages:=doc["stages"].([]any)
 allocs:=stages[0].(map[string]any)["allocations"].([]any)
 alloc:=allocs[0].(map[string]any)
 alloc["negative_driver_policy"]="ALLOW_SIGNED"
 alloc["signed_driver_reason"]="Explicit approved reimbursement reversal policy"
 b,err:=json.Marshal(doc);if err!=nil {t.Fatal(err)}
 if err=os.WriteFile(path,b,0600);err!=nil {t.Fatal(err)}
 driver:="period,department,sku,material\n2026-01,SharedPool,Item-A,8\n2026-01,SharedPool,Item-B,-1\n"
 if err=os.WriteFile(filepath.Join(dir,"drivers","material.csv"),[]byte(driver),0600);err!=nil {t.Fatal(err)}
 r:=InspectDriverCoverage(context.Background(),dir)
 if coverageAccount(r,"Cost-A").Status!="REVIEW" {t.Fatalf("signed exception not review: %+v",r)}
}
