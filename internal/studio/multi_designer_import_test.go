package studio

import (
    "allocationcore/internal/engine"
    "bytes"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func designerBridgeFixture(t *testing.T) (*MultiApp,string,string,[]byte) {
    t.Helper()
    home:=t.TempDir()
    root:=filepath.Join(home,"multi")
    project:=filepath.Join(root,"financial")
    designerRoot:=filepath.Join(home,"cases")
    designerDir:=filepath.Join(designerRoot,"example")
    for _,p:=range []string{filepath.Join(project,"templates"),filepath.Join(project,"drivers"),designerDir}{
        if err:=os.MkdirAll(p,0700);err!=nil {t.Fatal(err)}
    }
    write:=func(path,body string){
        t.Helper()
        if err:=os.WriteFile(path,[]byte(body),0600);err!=nil {t.Fatal(err)}
    }
    source:=engine.Spec{
        Schema:"allocation-ir/2.0",Name:"Designer financial allocation",Precision:2,
        Dimensions:[]string{"period","department","sku","account","currency"},
        Immutable:[]string{"period","currency","account"},
        Hierarchies:map[string]map[string][]string{},
    }
    source.Datasets.Amounts=map[string]string{"expenses":"source.csv"}
    source.Datasets.Drivers=map[string]string{"units":"driver_pool.csv"}
    source.Stages=[]engine.Stage{{
        ID:"By units",
        Inputs:[]engine.InputEdge{{From:"expenses",Share:"1"}},
        Source:engine.Source{Grouping:map[string]string{
            "period":"AS_IS","department":"AS_IS","sku":"TOTAL",
            "account":"AS_IS","currency":"AS_IS",
        }, Filters:map[string]engine.Filter{}},
        Target:map[string]engine.Target{
            "period":{Mode:"SOURCE"},"department":{Mode:"SOURCE"},
            "sku":{Mode:"INCLUDE",Include:[]string{"Product-A","Product-B"}},
            "account":{Mode:"SOURCE"},"currency":{Mode:"SOURCE"},
        },
        ConservationKeys:[]string{"period","account","currency","department"},
        Allocations:[]engine.Allocation{{ID:"by_units",Share:"1",DriverDataset:"units",WeightField:"units",NegativePolicy:"REJECT"}},
    }}
    raw,err:=json.MarshalIndent(source,"","  ");if err!=nil {t.Fatal(err)}
    if _,err:=engine.Compile(source);err!=nil {t.Fatal(err)}
    write(filepath.Join(designerDir,"rule.json"),string(raw))
    write(filepath.Join(designerDir,"source.csv"),"period,department,sku,account,currency,amount\n2026-01,Shared,Unassigned,Account-X,CUR,20.00\n")
    write(filepath.Join(designerDir,"driver_pool.csv"),"period,department,sku,units\n2026-01,Shared,Product-A,3\n")
    write(filepath.Join(project,"drivers","units.csv"),"period,department,sku,units\n2026-01,Shared,Product-A,3\n")
    write(filepath.Join(project,"assignments.csv"),"account,template\nAccount-X,base.json\n")
    // The business workspace uses the SAME coordinate contract as the source,
    // but points at project-local source/driver files.
    source.Datasets.Amounts["expenses"]="amounts.csv"
    source.Datasets.Drivers["units"]="units.csv"
    current,err:=json.Marshal(source);if err!=nil {t.Fatal(err)}
    write(filepath.Join(project,"templates","base.json"),string(current))
    write(filepath.Join(project,"amounts.csv"),"period,department,sku,account,currency,amount\n2026-01,Shared,Unassigned,Account-X,CUR,20.00\n")
    app:=&Application{data:home,custom:designerRoot}
    return &MultiApp{parent:app,root:root,busy:map[string]bool{}},project,designerDir,raw
}

func postDesignerBridge(t *testing.T,x *MultiApp, payload map[string]any) *httptest.ResponseRecorder {
    t.Helper()
    body,err:=json.Marshal(payload);if err!=nil {t.Fatal(err)}
    w:=httptest.NewRecorder()
    req:=httptest.NewRequest(http.MethodPost,"/api/multi/import-template",bytes.NewReader(body))
    x.importTemplateFromDesigner(w,req)
    return w
}
func designerBridgeRequest(preview bool,sha string) map[string]any{
    return map[string]any{
        "name":"financial","case":"user_example","template":"new_business.json",
        "preview":preview,"base_sha256":sha,
        "driver_mapping":map[string]string{"units":"units.csv"},
    }
}

func TestDesignerBridgePreviewAndImport(t *testing.T) {
    x,project,_,original:=designerBridgeFixture(t)
    before,err:=os.ReadFile(filepath.Join(project,"assignments.csv"));if err!=nil {t.Fatal(err)}
    amountBefore,err:=os.ReadFile(filepath.Join(project,"amounts.csv"));if err!=nil {t.Fatal(err)}
    preview:=postDesignerBridge(t,x,designerBridgeRequest(true,""))
    if preview.Code!=200 {t.Fatalf("preview %d: %s",preview.Code,preview.Body.String())}
    var report designerImportPlan
    if err:=json.Unmarshal(preview.Body.Bytes(),&report);err!=nil {t.Fatal(err)}
    if !report.Ready || len(report.Drivers)!=1 || report.Drivers[0].File!="units.csv" {
        t.Fatalf("incorrect preview: %+v",report)
    }
    expected:=sha256.Sum256(original)
    if report.SourceSHA256!=hex.EncodeToString(expected[:]) {t.Fatal("source hash mismatch")}
    dest:=filepath.Join(project,"templates","new_business.json")
    if _,err:=os.Stat(dest);!os.IsNotExist(err) {t.Fatalf("preview published a rule: %v",err)}
    result:=postDesignerBridge(t,x,designerBridgeRequest(false,report.SourceSHA256))
    if result.Code!=201 {t.Fatalf("import %d: %s",result.Code,result.Body.String())}
    imported,err:=os.ReadFile(dest);if err!=nil {t.Fatal(err)}
    spec,err:=engine.ParseSpec(imported);if err!=nil {t.Fatal(err)}
    if len(spec.Stages)!=1 || spec.Datasets.Amounts["expenses"]!="amounts.csv" || spec.Datasets.Drivers["units"]!="units.csv" {
        t.Fatalf("import changed graph or mapping: %+v",spec)
    }
    if _,err:=engine.Compile(spec);err!=nil {t.Fatalf("imported graph invalid: %v",err)}
    after,err:=os.ReadFile(filepath.Join(project,"assignments.csv"));if err!=nil {t.Fatal(err)}
    amountAfter,err:=os.ReadFile(filepath.Join(project,"amounts.csv"));if err!=nil {t.Fatal(err)}
    if !bytes.Equal(before,after)||!bytes.Equal(amountBefore,amountAfter) {t.Fatal("import mutated existing business amounts or assignments")}
    if duplicate:=postDesignerBridge(t,x,designerBridgeRequest(false,report.SourceSHA256));duplicate.Code!=409 {
        t.Fatalf("duplicate imported instead of rejected: %d %s",duplicate.Code,duplicate.Body.String())
    }
    afterDuplicate,err:=os.ReadFile(dest);if err!=nil {t.Fatal(err)}
    if !bytes.Equal(imported,afterDuplicate) {t.Fatal("duplicate changed first imported rule")}
}

func TestDesignerBridgeRejectsIncompatibleAndUnsafeRules(t *testing.T) {
    x,project,_,raw:=designerBridgeFixture(t)
    test:=func(name string,mutate func(*engine.Spec)){
        t.Run(name,func(t *testing.T){
            spec,err:=engine.ParseSpec(raw);if err!=nil {t.Fatal(err)}
            mutate(&spec)
            candidate,err:=json.Marshal(spec);if err!=nil {t.Fatal(err)}
            if _,_,err:=importDesignerRule(candidate,project,map[string]string{"units":"units.csv"});err==nil {
                t.Fatal("invalid rule was accepted")
            }
        })
    }
    test("account_not_immutable",func(s *engine.Spec){s.Immutable=[]string{"period","currency"}})
    test("account_no_conservation",func(s *engine.Spec){s.Stages[0].ConservationKeys=[]string{"period","currency"}})
    test("changed_precision",func(s *engine.Spec){s.Precision=3})
    test("changed_dimensions",func(s *engine.Spec){s.Dimensions=[]string{"period","sku","department","account","currency"}})
    test("invalid_graph",func(s *engine.Spec){s.Stages[0].Inputs=[]engine.InputEdge{{From:"missing",Share:"1"}}})
    test("unbalanced_factors",func(s *engine.Spec){s.Stages[0].Allocations[0].Share="0.5"})
    if _,_,err:=importDesignerRule(raw,project,map[string]string{"unknown":"units.csv"});err==nil {
        t.Fatal("unknown driver alias should be rejected")
    }
    if _,_,err:=importDesignerRule(raw,project,map[string]string{"units":"../escape.csv"});err==nil {
        t.Fatal("unsafe driver file should be rejected")
    }
    _=x
}

func TestDesignerBridgeHandlesStaleSourceAndBusyProject(t *testing.T) {
    x,project,sourceDir,_:=designerBridgeFixture(t)
    preview:=postDesignerBridge(t,x,designerBridgeRequest(true,""))
    if preview.Code!=200 {t.Fatal(preview.Body.String())}
    var plan designerImportPlan
    if err:=json.Unmarshal(preview.Body.Bytes(),&plan);err!=nil {t.Fatal(err)}
    x.busy["financial"]=true
    if r:=postDesignerBridge(t,x,designerBridgeRequest(false,plan.SourceSHA256));r.Code!=409 {
        t.Fatalf("busy project changed: %d %s",r.Code,r.Body.String())
    }
    x.busy["financial"]=false
    b,err:=os.ReadFile(filepath.Join(sourceDir,"rule.json"));if err!=nil {t.Fatal(err)}
    b=bytes.Replace(b,[]byte("Designer financial allocation"),[]byte("Designer changed"),1)
    if err:=os.WriteFile(filepath.Join(sourceDir,"rule.json"),b,0600);err!=nil {t.Fatal(err)}
    if r:=postDesignerBridge(t,x,designerBridgeRequest(false,plan.SourceSHA256));r.Code!=409 {
        t.Fatalf("stale designer source accepted: %d %s",r.Code,r.Body.String())
    }
    if _,err:=os.Stat(filepath.Join(project,"templates","new_business.json"));!os.IsNotExist(err) {
        t.Fatal("stale or busy attempt published a template")
    }
}

func TestDesignerBridgeMissingDriverIsReportedNotCopied(t *testing.T) {
    x,project,_,_:=designerBridgeFixture(t)
    // No explicit mapping: visual designer refers to driver_pool.csv, which
    // must NOT be automatically copied from an unrelated scenario.
    r:=postDesignerBridge(t,x,map[string]any{
        "name":"financial","case":"user_example","template":"requires_driver.json","preview":true,
    })
    if r.Code!=200 {t.Fatalf("%d %s",r.Code,r.Body.String())}
    var plan designerImportPlan
    if err:=json.Unmarshal(r.Body.Bytes(),&plan);err!=nil {t.Fatal(err)}
    if plan.Ready || len(plan.Drivers)!=1 || !strings.Contains(plan.Drivers[0].Issue,"does not exist") {
        t.Fatalf("missing driver not reported: %+v",plan)
    }
    r=postDesignerBridge(t,x,map[string]any{
        "name":"financial","case":"user_example","template":"requires_driver.json",
        "base_sha256":plan.SourceSHA256,
    })
    if r.Code!=201 {t.Fatalf("template should be importable for later driver onboarding: %d %s",r.Code,r.Body.String())}
    if _,err:=os.Stat(filepath.Join(project,"drivers","driver_pool.csv"));!os.IsNotExist(err) {
        t.Fatal("source case driver data was copied into business project")
    }
}
