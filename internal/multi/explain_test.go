package multi

import (
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func explainFixture(t *testing.T)string{
 t.Helper()
 dir:=t.TempDir()
 for _,sub:=range []string{"templates","drivers"}{if err:=os.Mkdir(filepath.Join(dir,sub),0700);err!=nil{t.Fatal(err)}}
 sample,err:=os.ReadFile(filepath.Join("..","..","testdata","multi_sample","templates","dual.json"))
 if err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(filepath.Join(dir,"templates","dual.json"),sample,0600);err!=nil{t.Fatal(err)}
 for _,name:=range []string{"material","revenue"}{
  if err:=os.WriteFile(filepath.Join(dir,"drivers",name+".csv"),[]byte("sku,"+name+"\nItem-A,3\nItem-B,7\n"),0600);err!=nil{t.Fatal(err)}
 }
 files:=map[string]string{
  "project.json":`{"amounts":"amounts.csv","assignments":"assignments.csv","overrides":"overrides.csv","templates":"templates","drivers":"drivers","workers":2,"jobs":2,"disk_partitions":8,"keep_group_outputs":true}`,
  "assignments.csv":"account,template\nCost-A,dual.json\nCost-B,dual.json\n",
  "overrides.csv":"account,stage,allocation,driver_dataset,weight_field,share\nCost-B,双因子分摊,material,,,0.50\nCost-B,双因子分摊,revenue,,,0.50\n",
 }
 for filename,data:=range files{if err:=os.WriteFile(filepath.Join(dir,filename),[]byte(data),0600);err!=nil{t.Fatal(err)}}
 return dir
}

func TestExplainAccountReflectsActualOverrides(t *testing.T){
 dir:=explainFixture(t)
 a,err:=ExplainAccount(dir,"Cost-A");if err!=nil{t.Fatal(err)}
 b,err:=ExplainAccount(dir,"Cost-B");if err!=nil{t.Fatal(err)}
 if a.Template!="dual.json"||b.Template!="dual.json"{t.Fatal("wrong template")}
 if a.GroupID==b.GroupID||a.RuleSHA256==b.RuleSHA256{t.Fatal("account overrides did not change effective rule identity")}
 if len(a.Stages)!=1||len(b.Stages)!=1||len(b.Stages[0].Drivers)!=2{t.Fatal("missing effective stages/drivers")}
 if a.Stages[0].Drivers[0].Share!="0.75"||b.Stages[0].Drivers[0].Share!="0.50"{t.Fatalf("override not applied: %+v %+v",a.Stages[0].Drivers,b.Stages[0].Drivers)}
 if b.Stages[0].Dimensions[2].Name!="sku"||b.Stages[0].Dimensions[2].TargetMode!="INCLUDE" {t.Fatal("dimension route missing")}
 if !b.Stages[0].Dimensions[3].Conserved{t.Fatal("account conservation missing")}
 for _,d:=range b.Stages[0].Drivers{if strings.Contains(d.File,dir)||strings.ContainsAny(d.File,"/\\"){t.Fatal("private path exposed")}}
}

func TestExplainAccountFailsClosed(t *testing.T){
 dir:=explainFixture(t)
 for _,account:=range []string{"","missing",strings.Repeat("x",1025)}{
  if _,err:=ExplainAccount(dir,account);err==nil{t.Fatalf("invalid account accepted: %q",account)}
 }
 if err:=os.Remove(filepath.Join(dir,"drivers","revenue.csv"));err!=nil{t.Fatal(err)}
 if _,err:=ExplainAccount(dir,"Cost-A");err==nil{t.Fatal("missing driver accepted")}
}
