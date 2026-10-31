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
func TestCoverageNegativeWeightOnlyReview(t *testing.T) {
 dir:=preflightFixture(t)
 data:="period,department,sku,material\n2026-01,SharedPool,Item-A,8\n2026-01,SharedPool,Item-B,-1\n"
 if e:=os.WriteFile(filepath.Join(dir,"drivers","material.csv"),[]byte(data),0600);e!=nil {t.Fatal(e)}
 report:=InspectDriverCoverage(context.Background(),dir)
 if report.Status=="SOURCE_MATCHED"||report.BlockedPairs!=0 {t.Fatalf("unsafe negative-weight claim: %+v",report)}
 found:=false;for _,f:=range report.Findings {if f.Level=="REVIEW" {found=true}}
 if !found {t.Fatal("negative-weight review missing")}
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
 if report.Status=="SOURCE_MATCHED"||coverageAccount(report,"Cost-A").Status!="UNVERIFIED" {t.Fatalf("multistage falsely certified: %+v",report)}
}
func TestCoverageCancelCannotPass(t *testing.T) {
 dir:=preflightFixture(t);ctx,cancel:=context.WithCancel(context.Background());cancel()
 if report:=InspectDriverCoverage(ctx,dir);report.Status=="SOURCE_MATCHED" {t.Fatal("canceled inspection passed")}
}
