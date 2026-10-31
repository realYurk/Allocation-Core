package multi

// Diagnostics are derived from the actual failed run's rule group, never from a later edit.
import (
 "encoding/json"
 "errors"
 "fmt"
 "os"
 "path/filepath"
 "regexp"
 "strings"
)
type FailureDiagnosis struct {
 Code string `json:"code"`
 Title string `json:"title"`
 Scope string `json:"scope"`
 GroupID string `json:"group_id"`
 Template string `json:"template,omitempty"`
 Accounts []string `json:"accounts,omitempty"`
 AccountCount int `json:"account_count"`
 Stage string `json:"stage,omitempty"`
 Allocation string `json:"allocation,omitempty"`
 DriverDataset string `json:"driver_dataset,omitempty"`
 DriverFile string `json:"driver_file,omitempty"`
 WeightField string `json:"weight_field,omitempty"`
 Pool map[string]string `json:"pool,omitempty"`
 NextSteps []string `json:"next_steps"`
 Limitations string `json:"limitations"`
}
type RunFailure struct { Cause error; Diagnosis FailureDiagnosis }
func (f *RunFailure) Error() string { return f.Cause.Error() }
func (f *RunFailure) Unwrap() error { return f.Cause }
func DiagnosisFromError(err error) (FailureDiagnosis,bool) {
 var f *RunFailure
 if errors.As(err,&f) {return f.Diagnosis,true}
 return FailureDiagnosis{},false
}
var failedGroupPattern=regexp.MustCompile(`^rule group ([0-9a-f]{20}):`)
var failedPoolPattern=regexp.MustCompile(`stage (.+?) rule (.+?) pool (\[)`)
func (p *project) explainExecutionFailure(err error) error {
 if err==nil{return nil}
 m:=failedGroupPattern.FindStringSubmatch(err.Error())
 if len(m)!=2{return err}
 var group *Group
 for _,g:=range p.groups {if g.ID==m[1] {group=g;break}}
 if group==nil{return err}
 d:=FailureDiagnosis{
  Code:"ENGINE_FAILED",Title:"分摊规则组运行失败",Scope:"RULE_GROUP",
  GroupID:group.ID,Template:group.Template,AccountCount:len(group.Accounts),
  NextSteps:[]string{"检查当前规则所用 Driver 数据和权重字段。","修正后重新体检、运行并独立对账；禁止发布本次失败任务。"},
  Limitations:"只能确认规则组范围，不能据此断定组内所有科目均有同样错误。",
 }
 n:=len(group.Accounts);if n>20{n=20}
 d.Accounts=append([]string(nil),group.Accounts[:n]...)
 raw:=group.Error;if raw=="" {raw=err.Error()}
 switch {
 case strings.Contains(raw,"has no eligible drivers"):
  d.Code="DRIVER_NO_MATCH";d.Title="当前金额池没有匹配的分摊因子"
  d.NextSteps=[]string{"核对 Driver 是否有同期间、同工厂/部门及目标产品的数据。","检查规则筛选条件、目标维度与成员名称。","修正后重新体检和运行。"}
 case strings.Contains(raw,"driver denominator must be positive"):
  d.Code="DRIVER_DENOMINATOR";d.Title="分摊因子合计不大于零"
  d.NextSteps=[]string{"检查该来源池对应的 Driver 是否全部为零或相互抵消。","核对权重字段、期间和业务范围，不要随意填充权重绕过校验。"}
 case strings.Contains(raw,"negative driver requires ALLOW_SIGNED"):
  d.Code="NEGATIVE_DRIVER";d.Title="当前规则不允许负分摊因子"
  d.NextSteps=[]string{"确认是否存在负权重录入错误或冲销记录。","只有业务确认冲销用途后才能配置 ALLOW_SIGNED 与用途说明。"}
 case strings.Contains(raw,"missing weight")||strings.Contains(raw,"missing CSV column"):
  d.Code="DRIVER_COLUMN";d.Title="Driver 缺少规则需要的权重字段"
  d.NextSteps=[]string{"核对 Driver CSV 表头和规则字段。","原始列名不同请使用字段映射重新导入。"}
 }
 if indexes:=failedPoolPattern.FindStringSubmatchIndex(raw);len(indexes)>=8 {
  d.Stage=raw[indexes[2]:indexes[3]];d.Allocation=raw[indexes[4]:indexes[5]]
  dec:=json.NewDecoder(strings.NewReader(raw[indexes[6]:]))
  var members []string
  if dec.Decode(&members)==nil&&len(members)==len(p.canonicalHeader) {
   d.Pool=map[string]string{}
   for i,k:=range p.canonicalHeader {d.Pool[k]=members[i]}
   d.Scope="EXACT_POOL"
   if account:=d.Pool["account"];account!=""&&account!="__POOL__" {
    for _,known:=range group.Accounts {if known==account {d.Accounts=[]string{account};d.AccountCount=1;break}}
   }
  }
 }
 if d.Stage!=""&&d.Allocation!="" {
  var rule struct {Stages []struct {
   ID string `json:"id"`
   Allocations []struct {
    ID string `json:"id"`
    DriverDataset string `json:"driver_dataset"`
    WeightField string `json:"weight_field"`
   } `json:"allocations"`
  } `json:"stages"` }
  if b,e:=os.ReadFile(group.Rule);e==nil&&json.Unmarshal(b,&rule)==nil {
   for _,s:=range rule.Stages {if s.ID!=d.Stage{continue};for _,a:=range s.Allocations{
    if a.ID==d.Allocation{d.DriverDataset=a.DriverDataset;d.WeightField=a.WeightField}
   }}
  }
  var template struct {Datasets struct {Drivers map[string]string `json:"drivers"`} `json:"datasets"`}
  if b,e:=os.ReadFile(filepath.Join(p.dir,"templates",group.Template));e==nil&&json.Unmarshal(b,&template)==nil {
   if name:=template.Datasets.Drivers[d.DriverDataset];name!=""&&safeFile(name) {
    d.DriverFile=filepath.ToSlash(filepath.Join("drivers",name))
   }
  }
 }
 if d.Scope=="EXACT_POOL" {d.Limitations="坐标来自本次失败的来源金额池；这不是目标金额正确性的证明，仍需修复后重新计算并独立对账。"}
 return &RunFailure{Cause:err,Diagnosis:d}
}
const diagnosisMarker="ALLOCATION_FAILURE_V1:"
func EmitFailureDiagnosis(err error) {
 if d,ok:=DiagnosisFromError(err);ok {if b,e:=json.Marshal(d);e==nil {_,_=fmt.Fprintln(os.Stderr,diagnosisMarker+string(b))}}
}
