package multi

import (
 "context"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "testing"
)
func businessHasCode(r PreflightReport,code string) bool {
 for _,f:=range r.Findings {if f.Code==code{return true}}
 return false
}
func businessAccount(r PreflightReport,name string) string {
 for _,a:=range r.AccountDetails {if a.Account==name{return a.Status}}
 return "NOT_FOUND"
}
func TestBusinessReadinessNormal(t *testing.T) {
 dir:=preflightFixture(t)
 r:=InspectProject(context.Background(),dir)
 if r.Status!="PASS"||len(r.AccountDetails)!=4||r.ReviewAccounts!=0||r.DataMatchVerified {
  t.Fatalf("unexpected input status: %+v",r)
 }
 if businessAccount(r,"Cost-A")!="INPUT_OK" {t.Fatal("missing account")}
}
func TestBusinessReadinessZeroAndNegativeDrivers(t *testing.T) {
 dir:=preflightFixture(t)
 material:=filepath.Join(dir,"drivers","material.csv")
 if err:=os.WriteFile(material,[]byte("period,department,sku,material\n2026-01,SharedPool,Item-A,0\n2026-01,SharedPool,Item-B,0\n"),0600);err!=nil {t.Fatal(err)}
 revenue:=filepath.Join(dir,"drivers","revenue.csv")
 if err:=os.WriteFile(revenue,[]byte("period,department,sku,revenue\n2026-01,SharedPool,Item-A,5\n2026-01,SharedPool,Item-B,-1\n"),0600);err!=nil {t.Fatal(err)}
 r:=InspectProject(context.Background(),dir)
 if r.Status!="PASS" || !businessHasCode(r,"ALL_ZERO_DRIVER_FIELD") || !businessHasCode(r,"NEGATIVE_DRIVER_FIELD"){
  t.Fatalf("should warn without claiming actual pool failure: %+v",r)
 }
 if businessAccount(r,"Cost-A")!="CHECK_DRIVER" || businessAccount(r,"Cost-B")!="CHECK_DRIVER" {
  t.Fatalf("account warnings missing: %+v",r.AccountDetails)
 }
 for _,d:=range r.Drivers {
  if len(d.WeightSignals)!=1 {t.Fatalf("missing signal %+v",d)}
 }
}
func TestBusinessReadinessBlockingRepairAdvice(t *testing.T) {
 dir:=preflightFixture(t)
 path:=filepath.Join(dir,"amounts.csv")
 data,err:=os.ReadFile(path);if err!=nil {t.Fatal(err)}
 if err:=os.WriteFile(path,[]byte(strings.Replace(string(data),"Cost-D,","Unknown-Cost,",1)),0600);err!=nil {t.Fatal(err)}
 r:=InspectProject(context.Background(),dir)
 if r.Status!="BLOCK"||len(r.Findings)==0||r.Findings[0].NextStep=="" {t.Fatalf("missing repair advice: %+v",r)}
}
func TestBusinessReadinessThousandAccounts(t *testing.T) {
 dir:=preflightFixture(t)
 assignments,err:=os.OpenFile(filepath.Join(dir,"assignments.csv"),os.O_WRONLY|os.O_APPEND,0600)
 if err!=nil {t.Fatal(err)}
 amounts,err:=os.OpenFile(filepath.Join(dir,"amounts.csv"),os.O_WRONLY|os.O_APPEND,0600)
 if err!=nil {t.Fatal(err)}
 for i:=0;i<996;i++ {
  name:=fmt.Sprintf("Bulk-%04d",i)
  if _,err:=fmt.Fprintf(assignments,"%s,material.json\n",name);err!=nil {t.Fatal(err)}
  if _,err:=fmt.Fprintf(amounts,"2026-01,SharedPool,Unassigned,%s,CUR,1.00,R-%04d\n",name,i);err!=nil {t.Fatal(err)}
 }
 if err:=assignments.Close();err!=nil {t.Fatal(err)}
 if err:=amounts.Close();err!=nil {t.Fatal(err)}
 r:=InspectProject(context.Background(),dir)
 if r.Status!="PASS"||r.Accounts!=1000||len(r.AccountDetails)!=1000||r.TotalRows!=1000 {
  t.Fatalf("lost account: %+v",r)
 }
 if businessAccount(r,"Bulk-0995")!="INPUT_OK" {t.Fatal("tail account missing")}
}
