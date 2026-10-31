package engine_test

import (
 "allocationcore/internal/engine"
 "encoding/csv"
 "encoding/json"
 "fmt"
 "math"
 "math/big"
 "os"
 "path/filepath"
 "reflect"
 "strconv"
 "strings"
 "testing"
)

func TestFinancialSignedPolicyAndUnknownRoundingFailClosed(t *testing.T) {
 s:=engine.Spec{Schema:engine.Schema,Dimensions:[]string{"account","sku"},Immutable:[]string{"account"}}
 s.Datasets.Amounts=map[string]string{"money":"amounts.csv"}
 s.Datasets.Drivers=map[string]string{"units":"units.csv"}
 a:=engine.Allocation{ID:"exception",Share:"1",DriverDataset:"units",WeightField:"units",NegativePolicy:"ALLOW_SIGNED"}
 s.Stages=[]engine.Stage{{ID:"step",Inputs:[]engine.InputEdge{{From:"money",Share:"1"}},Source:engine.Source{Grouping:map[string]string{"account":"AS_IS","sku":"TOTAL"}},Target:map[string]engine.Target{"account":{Mode:"SOURCE"},"sku":{Mode:"INCLUDE",Include:[]string{"A","B"}}},ConservationKeys:[]string{"account"},Allocations:[]engine.Allocation{a}}}
 if _,e:=engine.Compile(s);e==nil{t.Fatal("ALLOW_SIGNED without reason accepted")}
 s.Stages[0].Allocations[0].SignedDriverReason="Documented credit offset for this cost account"
 if _,e:=engine.Compile(s);e!=nil{t.Fatal(e)}
 s.RoundingPolicy="FIRST_INPUT_ROW"
 if _,e:=engine.Compile(s);e==nil{t.Fatal("unsupported rounding policy accepted")}
 s.RoundingPolicy=engine.RoundingLargestRemainderIdentity
 if _,e:=engine.Compile(s);e!=nil{t.Fatal(e)}
 s.Stages[0].Allocations[0].NegativePolicy="REJECT"
 if _,e:=engine.Compile(s);e==nil{t.Fatal("signed justification without explicit policy accepted")}
}

func TestFinancialThreeStageIndependentRationalOracle(t *testing.T) {
 for _,total:=range []int64{101,-101}{
  t.Run(fmt.Sprint(total),func(t *testing.T){
   dir:=t.TempDir()
   dims:=[]string{"account","s1","s2","s3"}
   s:=engine.Spec{Schema:engine.Schema,Name:"3-stage-independent",Precision:0,Dimensions:dims,Immutable:[]string{"account"},RoundingPolicy:engine.RoundingLargestRemainderIdentity}
   s.Datasets.Amounts=map[string]string{"source":"amounts.csv"}
   s.Datasets.Drivers=map[string]string{"d1":"d1.csv","d2":"d2.csv","d3":"d3.csv"}
   prev:="source"
   for index:=0;index<3;index++{
    group:=map[string]string{"account":"AS_IS"}
    target:=map[string]engine.Target{"account":{Mode:"SOURCE"}}
    conserved:=[]string{"account"}
    for i:=0;i<3;i++{
     axis:=fmt.Sprintf("s%d",i+1)
     switch{
     case i<index:
      group[axis]="AS_IS";target[axis]=engine.Target{Mode:"SOURCE"};conserved=append(conserved,axis)
     case i==index:
      group[axis]="TOTAL";target[axis]=engine.Target{Mode:"INCLUDE",Include:[]string{"A","B","C"}}
     default:
      group[axis]="TOTAL";target[axis]=engine.Target{Mode:"ELEMENT",Value:"U"}
     }
    }
    id:=fmt.Sprintf("step%d",index+1)
    driver:=fmt.Sprintf("d%d",index+1)
    s.Stages=append(s.Stages,engine.Stage{
     ID:id,Inputs:[]engine.InputEdge{{From:prev,Share:"1"}},
     Source:engine.Source{Grouping:group},Target:target,ConservationKeys:conserved,
     Allocations:[]engine.Allocation{{ID:"one-third",Share:"1",DriverDataset:driver,WeightField:"units",NegativePolicy:"REJECT"}},
    })
    prev=id
   }
   save:=func(name,body string){t.Helper();if e:=os.WriteFile(filepath.Join(dir,name),[]byte(body),0600);e!=nil{t.Fatal(e)}}
   save("amounts.csv",fmt.Sprintf("account,s1,s2,s3,amount\nCost-A,U,U,U,%d\n",total))
   save("d1.csv","s1,units\nA,1\nB,1\nC,1\n")
   var d2,d3 strings.Builder
   d2.WriteString("s1,s2,units\n");d3.WriteString("s1,s2,s3,units\n")
   for _,a:=range []string{"A","B","C"}{for _,b:=range []string{"A","B","C"}{
    fmt.Fprintf(&d2,"%s,%s,1\n",a,b)
    for _,c:=range []string{"A","B","C"}{fmt.Fprintf(&d3,"%s,%s,%s,1\n",a,b,c)}
   }}
   save("d2.csv",d2.String());save("d3.csv",d3.String())
   data,e:=json.Marshal(s);if e!=nil{t.Fatal(e)}
   save("rule.json",string(data))
   if _,e:=engine.Compile(s);e!=nil{t.Fatal(e)}
   got:=map[string]map[string]int64{}
   for _,mode:=range []struct{name string;options engine.RunOptions}{
    {"memory",engine.RunOptions{Workers:1}},
    {"disk",engine.RunOptions{Workers:3,DiskPartitions:4}},
   }{
    path:=filepath.Join(dir,mode.name)
    var report engine.Summary
    if mode.options.DiskPartitions==0{report,e=engine.RunWithOptions(filepath.Join(dir,"rule.json"),path,mode.options)}else{report,e=engine.RunDiskWithOptions(filepath.Join(dir,"rule.json"),path,mode.options)}
    if e!=nil||report.Status!="PASS"||report.InputAmount!=fmt.Sprint(total)||report.OutputAmount!=fmt.Sprint(total){t.Fatalf("%s summary %+v err %v",mode.name,report,e)}
    file,e:=os.Open(filepath.Join(path,"results.csv"));if e!=nil{t.Fatal(e)}
    rows,e:=csv.NewReader(file).ReadAll();file.Close();if e!=nil{t.Fatal(e)}
    ix:=map[string]int{};for i,col:=range rows[0]{ix[col]=i}
    results:=map[string]int64{};sum:=int64(0)
    ideal:=new(big.Rat).SetFrac(big.NewInt(total),big.NewInt(27))
    for _,row:=range rows[1:]{
     k:=row[ix["s1"]]+row[ix["s2"]]+row[ix["s3"]]
     value,e:=strconv.ParseInt(row[ix["amount"]],10,64);if e!=nil{t.Fatal(e)}
     if _,exists:=results[k];exists{t.Fatalf("%s duplicate leaf %s",mode.name,k)}
     results[k]=value;sum+=value
     errToIdeal:=new(big.Rat).Sub(new(big.Rat).SetInt64(value),ideal)
     errToIdeal.Abs(errToIdeal)
     if errToIdeal.Cmp(new(big.Rat).SetInt64(3))>=0{t.Fatalf("%s coordinate %s deviation %s >=3",mode.name,k,errToIdeal.RatString())}
    }
    if len(results)!=27||sum!=total{t.Fatalf("%s: %d leaves, sum %d",mode.name,len(results),sum)}
    got[mode.name]=results
   }
   if !reflect.DeepEqual(got["memory"],got["disk"]){t.Fatal("executor changed final coordinate results")}
  })
 }
}

func TestFinancialParseScaledBoundaries(t *testing.T) {
 cases:=[]struct{v string;scale int;want int64;valid bool}{
  {"1.",2,100,true},{"+1.23",2,123,true},{" 1.23 ",2,123,true},{"0001.20",2,120,true},
  {"1.230",2,0,false},{".1",2,0,false},{"1.001",2,0,false},
  {"-9223372036854775808",0,math.MinInt64,true},{"9223372036854775807",0,math.MaxInt64,true},
  {"9223372036854775808",0,0,false},{"-9223372036854775809",0,0,false},
  {strings.Repeat("9",300),2,0,false},{"1",-1,0,false},{"1",7,0,false},
 }
 for _,tc:=range cases{v,e:=engine.ParseScaled(tc.v,tc.scale);if (e==nil)!=tc.valid||(tc.valid&&v!=tc.want){t.Fatalf("input %q scale=%d value=%d error=%v",tc.v,tc.scale,v,e)}}
}

func FuzzFinancialParseScaled(f *testing.F) {
 for _,s:=range []string{"0","-0","+1.23"," 1.23 ","1.",".1","1.230","1.001","-9223372036854775808",strings.Repeat("9",300),"1e3"}{f.Add(s,uint8(2))}
 f.Fuzz(func(t *testing.T,input string,precision uint8){
  scale:=int(precision%8)-1
  actual,e:=engine.ParseScaled(input,scale)
  if e!=nil{return}
  if scale<0||scale>6{t.Fatal("invalid scale accepted")}
  text:=strings.TrimSpace(input)
  if len(text)>128{t.Fatal("overlong input accepted")}
  exact,ok:=new(big.Rat).SetString(text)
  if !ok{t.Fatalf("accepted invalid decimal %q",text)}
  exact.Mul(exact,new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10),big.NewInt(int64(scale)),nil)))
  if !exact.IsInt()||!exact.Num().IsInt64()||actual!=exact.Num().Int64(){t.Fatalf("not exactly represented %q scale=%d got=%d ideal=%s",text,scale,actual,exact.RatString())}
 })
}
