package multi

// Source-pool Driver coverage is a read-only advisory check, not a financial run.
// It checks a single direct-amounts root stage, including the root of a
// multi-stage graph. Downstream stages are ALWAYS explicitly unverified.
// Unsupported paths, incomplete scans and source changes remain UNVERIFIED.
import (
 "bufio"
 "context"
 "encoding/csv"
 "encoding/json"
 "fmt"
 "io"
 "math/big"
 "os"
 "path/filepath"
 "sort"
 "strings"
 "allocationcore/internal/engine"
)

const (
 coverageMaxGroups=128
 coverageMaxPools=50000
 coverageMaxTargets=100000
 coverageMaxFindings=60
)
type CoverageFinding struct {
 Level string `json:"level"`
 Account string `json:"account"`
 Stage string `json:"stage"`
 Driver string `json:"driver,omitempty"`
 Field string `json:"field,omitempty"`
 Pool map[string]string `json:"source_pool,omitempty"`
 Reason string `json:"reason"`
 Advice string `json:"advice"`
}
type CoverageAccount struct {
 Account string `json:"account"`
 Status string `json:"status"`
 CheckedPairs int `json:"checked_pool_factors"`
 Problems int `json:"problems"`
}
type CoverageReport struct {
 Status string `json:"status"`
 Scope string `json:"scope"`
 CheckedPairs int `json:"checked_pool_factors"`
 BlockedPairs int `json:"blocked_pool_factors"`
 UnverifiedAccounts int `json:"unverified_accounts"`
 Findings []CoverageFinding `json:"findings"`
 Accounts []CoverageAccount `json:"accounts"`
 TruncatedFindings int `json:"truncated_findings"`
 Stale bool `json:"source_changed_during_inspection"`
 Invalidated bool `json:"diagnosis_invalidated"`
}
type coveragePool struct {coord map[string]string;sum big.Int}
type coveragePlan struct {group *Group;rule engine.Spec;stage engine.Stage;complete bool;pools map[string]*coveragePool}
type coverageDriverSum struct {sum big.Int;rows int;negative bool}
type coverageDriverIndex struct {bound []string;byKey map[string]*coverageDriverSum}

func (r *CoverageReport) add(f CoverageFinding) {
 if len(r.Findings)<coverageMaxFindings {r.Findings=append(r.Findings,f)} else {r.TruncatedFindings++}
}
func coverageKey(c map[string]string,dims []string) string {
 v:=make([]string,len(dims));for i,d:=range dims {v[i]=c[d]}
 b,_:=json.Marshal(v);return string(b)
}
func coverageReview(accounts []CoverageAccount) bool {
 for _,a:=range accounts {if a.Status=="REVIEW" {return true}}
 return false
}
// coverageSupported never simulates stage-to-stage routing.
// A unique full-share direct-input root can be checked, even in a multi-stage
// graph. Subsequent stages remain explicitly UNVERIFIED.
func coverageSupported(s engine.Spec) (engine.Stage,string,bool) {
 if len(s.Datasets.Amounts)!=1 {return engine.Stage{},"包含多个金额来源，无法仅靠来源池证明匹配",false}
 roots:=[]engine.Stage{}
 for _,st:=range s.Stages {
  for _,input:=range st.Inputs {
   if s.Datasets.Amounts[input.From]!="" {roots=append(roots,st);break}
  }
 }
 if len(roots)!=1 {return engine.Stage{},"需要唯一的直接金额来源起始阶段；多来源图仍须运行 Core",false}
 st:=roots[0]
 if !engine.Enabled(st.Enabled)||st.Operation=="MERGE"||st.Operation=="PASS" {return st,"直接金额来源的起始阶段不是启用的分摊阶段",false}
 if len(st.Inputs)!=1||(st.Inputs[0].Share!=""&&st.Inputs[0].Share!="1") {return st,"起始阶段含多个来源或部分来源份额",false}
 if len(s.Hierarchies)!=0||len(st.Source.Filters)!=0 {return st,"起始阶段含层级或来源筛选",false}
 for _,a:=range st.Allocations {if len(a.DriverFilters)!=0 {return st,"起始阶段含 Driver 筛选",false}}
 return st,"",len(s.Stages)==1
}

// InspectDriverCoverage does not create output files and does not publish results.
// SOURCE_MATCHED means eligible first-stage pool/weight matching only.
func InspectDriverCoverage(ctx context.Context,projectDir string) (r CoverageReport) {
 r=CoverageReport{Status:"PARTIAL",Scope:"检查唯一直接金额来源起始阶段的 Driver 匹配；多阶段仅检查第一阶段，后续阶段、最终金额和参考系统对账均未验证。",Findings:[]CoverageFinding{},Accounts:[]CoverageAccount{}}
 dir,e:=filepath.Abs(projectDir)
 if e!=nil {r.add(CoverageFinding{Level:"UNVERIFIED",Reason:e.Error()});return r}
 p:=&project{dir:dir,ctx:ctx}
 if e=p.initialize();e!=nil {r.add(CoverageFinding{Level:"UNVERIFIED",Reason:"项目规则无法加载："+e.Error()});return r}
 // Hash input bytes before and after scanning, so a changed source cannot PASS.
 sourceFiles:=map[string]bool{filepath.Join(dir,p.cfg.Amounts):true}
 for i,g:=range p.groups {
  if i>=coverageMaxGroups {break}
  spec,err:=engine.ParseSpec([]byte(g.Rule))
  if err!=nil {r.add(CoverageFinding{Level:"UNVERIFIED",Reason:err.Error()});return r}
  for _,path:=range spec.Datasets.Drivers {sourceFiles[path]=true}
 }
 beforeHashes:=map[string]string{}
 for path:=range sourceFiles {
  hash,err:=fileDigest(path)
  if err!=nil {r.add(CoverageFinding{Level:"UNVERIFIED",Reason:"无法检查输入来源："+err.Error()});return r}
  beforeHashes[path]=hash
 }
 base:=InspectProject(ctx,dir)
 if base.Status!="PASS" {
  r.add(CoverageFinding{Level:"UNVERIFIED",Reason:"请先修复输入体检："+strings.Join(base.Problems,"；")})
  return r
 }
 statuses:=map[string]*CoverageAccount{}
 for _,a:=range sortedAccounts(p.assign) {statuses[a]=&CoverageAccount{Account:a,Status:"UNVERIFIED"}}
 defer func(){
  for path,old:=range beforeHashes {
   current,err:=fileDigest(path)
   if err!=nil||current!=old {
    r.Stale=true
    r.Invalidated=true
    r.add(CoverageFinding{Level:"UNVERIFIED",Reason:"检查期间源文件发生变化；本次报告作废",Advice:"固定输入快照后重新检查。"})
    for _,a:=range statuses {a.Status="UNVERIFIED"}
    break
   }
  }
  for _,a:=range sortedAccounts(p.assign) {
   r.Accounts=append(r.Accounts,*statuses[a])
   if statuses[a].Status=="UNVERIFIED"||statuses[a].Status=="ROOT_MATCHED" {r.UnverifiedAccounts++}
  }
  if r.Invalidated {
   r.CheckedPairs=0
   r.BlockedPairs=0
   for i:=range r.Findings {
    if r.Findings[i].Level=="BLOCK"||r.Findings[i].Level=="REVIEW" {
     r.Findings[i].Level="UNVERIFIED"
     r.Findings[i].Reason="输入或规则版本变化，原检查证据已失效："+r.Findings[i].Reason
    }
   }
   r.Status="PARTIAL"
  } else if r.BlockedPairs>0 {r.Status="BLOCK"} else if r.UnverifiedAccounts==0&&r.TruncatedFindings==0&&r.CheckedPairs>0&&!coverageReview(r.Accounts) {r.Status="SOURCE_MATCHED"}
 }()
 plans:=map[string]*coveragePlan{}
 for i,g:=range p.groups {
  if i>=coverageMaxGroups {r.add(CoverageFinding{Level:"UNVERIFIED",Reason:"超过规则组安全检查上限，其余组未核对"});break}
  s,err:=engine.ParseSpec([]byte(g.Rule))
  if err!=nil {r.add(CoverageFinding{Level:"UNVERIFIED",Reason:err.Error()});continue}
  st,why,complete:=coverageSupported(s)
  if why!="" {r.add(CoverageFinding{Level:"UNVERIFIED",Account:g.Accounts[0],Stage:st.ID,Reason:why,Advice:"运行 Core 并独立对账。"});continue}
  plans[g.ID]=&coveragePlan{group:g,rule:s,stage:st,complete:complete,pools:map[string]*coveragePool{}}
  matchedStatus:="ROOT_MATCHED"
  if complete {matchedStatus="SOURCE_MATCHED"}
  for _,a:=range g.Accounts {statuses[a].Status=matchedStatus}
  if !complete {
   r.add(CoverageFinding{Level:"UNVERIFIED",Account:g.Accounts[0],Stage:st.ID,Reason:fmt.Sprintf("已选择直接金额起始阶段作来源池检查；后续 %d 个阶段不能在不计算时证明匹配",len(s.Stages)-1),Advice:"先核对本阶段的 Driver 结果，再用 Core 执行完整规则并做独立参考对账。"})
  }
 }
 if len(plans)==0 {return r}
 source,e:=regularWithin(dir,p.cfg.Amounts)
 if e!=nil {r.add(CoverageFinding{Level:"UNVERIFIED",Reason:e.Error()});for _,a:=range statuses {a.Status="UNVERIFIED"};return r}
 file,e:=os.Open(source)
 if e!=nil {r.add(CoverageFinding{Level:"UNVERIFIED",Reason:e.Error()});for _,a:=range statuses {a.Status="UNVERIFIED"};return r}
 defer file.Close()
 rd:=csv.NewReader(bufio.NewReaderSize(file,256*1024));rd.FieldsPerRecord=-1
 header,e:=rd.Read()
 if e!=nil {r.add(CoverageFinding{Level:"UNVERIFIED",Reason:e.Error()});for _,a:=range statuses {a.Status="UNVERIFIED"};return r}
 pos,e:=requiredColumns(header,"account","amount")
 if e!=nil {r.add(CoverageFinding{Level:"UNVERIFIED",Reason:e.Error()});for _,a:=range statuses {a.Status="UNVERIFIED"};return r}
 totalPools:=0
 for {
  if e=ctx.Err();e!=nil {break}
  var rec []string;rec,e=rd.Read()
  if e==io.EOF {e=nil;break}
  if e!=nil {break}
  pl:=plans[p.effective[get(rec,pos["account"])]]
  if pl==nil {continue}
  pool:=map[string]string{}
  for _,d:=range pl.rule.Dimensions {
   if pl.stage.Source.Grouping[d]=="AS_IS" {pool[d]=rec[pos[d]]} else {pool[d]="__POOL__"}
  }
  key:=coverageKey(pool,pl.rule.Dimensions)
  b:=pl.pools[key]
  if b==nil {
   totalPools++
   if totalPools>coverageMaxPools {e=fmt.Errorf("来源池超过 %d 个安全上限",coverageMaxPools);break}
   b=&coveragePool{coord:pool};pl.pools[key]=b
  }
  n,err:=engine.ParseScaled(get(rec,pos["amount"]),pl.rule.Precision)
  if err!=nil {e=err;break}
  b.sum.Add(&b.sum,big.NewInt(n))
 }
 if e!=nil {
  r.add(CoverageFinding{Level:"UNVERIFIED",Reason:"金额来源池未完整读取："+e.Error(),Advice:"缩小切面后重试。"})
  for _,a:=range statuses {a.Status="UNVERIFIED"};return r
 }
 ids:=make([]string,0,len(plans));for id:=range plans {ids=append(ids,id)};sort.Strings(ids)
 for _,id:=range ids {
  pl:=plans[id]
  allocs:=[]engine.Allocation{};shares:=[]int64{};names:=[]string{}
  for _,a:=range pl.stage.Allocations {
   if !engine.Enabled(a.Enabled) {continue}
   sh:=a.Share;if sh=="" {sh="1"}
   value,err:=engine.ParseScaled(sh,6)
   if err!=nil {e=err;break}
   allocs=append(allocs,a);shares=append(shares,value);names=append(names,a.ID)
  }
  if e!=nil {break}
  poolKeys:=make([]string,0,len(pl.pools));for key:=range pl.pools {poolKeys=append(poolKeys,key)};sort.Strings(poolKeys)
  for ai,a:=range allocs {
   if shares[ai]==0 {continue}
   sums,err:=coverageReadDriver(ctx,pl,a)
   if err!=nil {
    r.add(CoverageFinding{Level:"UNVERIFIED",Account:pl.group.Accounts[0],Stage:pl.stage.ID,Reason:err.Error()})
    for _,name:=range pl.group.Accounts {statuses[name].Status="UNVERIFIED"}
    continue
   }
   for _,key:=range poolKeys {
    pool:=pl.pools[key]
    if !pool.sum.IsInt64() {e=fmt.Errorf("来源池金额超过 int64");break}
    parts,err:=engine.Apportion(pool.sum.Int64(),shares,names,false)
    if err!=nil {e=err;break}
    if parts[ai]==0 {continue}
    sum:=sums.byKey[coverageKey(pool.coord,sums.bound)]
    account:=pool.coord["account"];entry:=statuses[account]
    if entry==nil {continue}
    entry.CheckedPairs++;r.CheckedPairs++
    if sum==nil||sum.rows==0||sum.sum.Sign()<=0 {
     entry.Status="BLOCK";entry.Problems++;r.BlockedPairs++
     reason:="没有符合当前来源池和目标成员的 Driver"
     if sum!=nil&&sum.rows>0 {reason="已有匹配 Driver，但有效权重合计不大于零"}
     r.add(CoverageFinding{Level:"BLOCK",Account:account,Stage:pl.stage.ID,Driver:filepath.Base(pl.rule.Datasets.Drivers[a.DriverDataset]),Field:a.WeightField,Pool:pool.coord,Reason:reason,Advice:"检查期间、部门、目标成员和 Driver 权重。"})
    } else if sum.negative {
     if a.NegativePolicy!="ALLOW_SIGNED" {
      entry.Status="BLOCK";entry.Problems++;r.BlockedPairs++
      r.add(CoverageFinding{Level:"BLOCK",Account:account,Stage:pl.stage.ID,Driver:filepath.Base(pl.rule.Datasets.Drivers[a.DriverDataset]),Field:a.WeightField,Pool:pool.coord,Reason:"匹配的 Driver 含负权重，但此规则未启用冲销；Core 运行将拒绝该数据",Advice:"检查原始 Driver 是否录入错误；确需使用负权重时，按冲销业务流程明确声明用途。"})
     } else if entry.Status!="BLOCK" {
      entry.Status="REVIEW"
      r.add(CoverageFinding{Level:"REVIEW",Account:account,Stage:pl.stage.ID,Driver:filepath.Base(pl.rule.Datasets.Drivers[a.DriverDataset]),Field:a.WeightField,Pool:pool.coord,Reason:"已启用负权重冲销，需核对业务用途及最终分摊方向",Advice:"确认该科目的冲销政策，运行 Core 并按目标坐标对账。"})
     }
    }
   }
   if e!=nil {break}
  }
  if e!=nil {break}
 }
 if e!=nil {
  r.add(CoverageFinding{Level:"UNVERIFIED",Reason:e.Error()})
  for _,a:=range statuses {if a.Status!="BLOCK" {a.Status="UNVERIFIED"}}
 }
 if err:=p.verifyControls();err!=nil {
  r.Invalidated=true
  r.add(CoverageFinding{Level:"UNVERIFIED",Reason:"规则检查期间变化："+err.Error()})
  for _,a:=range statuses {a.Status="UNVERIFIED"}
 }
 return r
}
func coverageReadDriver(ctx context.Context,pl *coveragePlan,a engine.Allocation) (coverageDriverIndex,error) {
 result:=coverageDriverIndex{byKey:map[string]*coverageDriverSum{}}
 path:=pl.rule.Datasets.Drivers[a.DriverDataset]
 file,e:=os.Open(path);if e!=nil {return result,e};defer file.Close()
 rd:=csv.NewReader(bufio.NewReaderSize(file,256*1024));rd.FieldsPerRecord=-1
 header,e:=rd.Read();if e!=nil {return result,e}
 pos,e:=requiredColumns(header,a.WeightField);if e!=nil {return result,e}
 for _,d:=range pl.rule.Dimensions {if pl.stage.Target[d].Mode=="SOURCE" {if _,ok:=pos[d];ok {result.bound=append(result.bound,d)}}}
 for _,d:=range pl.rule.Dimensions {
  t:=pl.stage.Target[d]
  if t.Mode=="INCLUDE"||t.Mode=="INCLUDE_EXCLUDE" {
   if _,ok:=pos[d];!ok {return result,fmt.Errorf("Driver 缺少目标维度 %s",d)}
  }
 }
 wanted:=map[string]bool{}
 for _,pool:=range pl.pools {wanted[coverageKey(pool.coord,result.bound)]=true}
 targetCount:=0
 for {
  if err:=ctx.Err();err!=nil {return result,err}
  rec,err:=rd.Read()
  if err==io.EOF {break};if err!=nil {return result,err}
  if len(rec)!=len(header) {return result,fmt.Errorf("Driver CSV 列数不一致")}
  coord:=map[string]string{}
  for _,d:=range pl.rule.Dimensions {if ix,ok:=pos[d];ok {coord[d]=rec[ix]}}
  key:=coverageKey(coord,result.bound)
  if !wanted[key] {continue}
  allowed:=true
  for _,d:=range pl.rule.Dimensions {
   t:=pl.stage.Target[d]
   if t.Mode!="INCLUDE"&&t.Mode!="INCLUDE_EXCLUDE" {continue}
   present:=false;for _,v:=range t.Include {if v==coord[d] {present=true;break}}
   for _,v:=range t.Exclude {if v==coord[d] {present=false;break}}
   if !present {allowed=false;break}
  }
  if !allowed {continue}
  value,err:=engine.ParseScaled(rec[pos[a.WeightField]],6);if err!=nil {return result,err}
  got:=result.byKey[key]
  if got==nil {
   targetCount++;if targetCount>coverageMaxTargets {return result,fmt.Errorf("匹配索引超过 %d 个来源键",coverageMaxTargets)}
   got=&coverageDriverSum{};result.byKey[key]=got
  }
  got.sum.Add(&got.sum,big.NewInt(value));got.rows++;if value<0 {got.negative=true}
 }
 return result,nil
}
