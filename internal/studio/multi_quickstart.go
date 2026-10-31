package studio

// Read-only view of a compiled rule, for finance users choosing a template.
// No allocation arithmetic or financial input is loaded by this endpoint.
import (
 "allocationcore/internal/engine"
 "fmt"
 "net/http"
 "strings"
)
type quickDriver struct {
 Name string `json:"name"`
 Dataset string `json:"dataset"`
 Field string `json:"field"`
 Percent string `json:"percent"`
 NegativePolicy string `json:"negative_policy"`
 SignedReason string `json:"signed_reason,omitempty"`
}
type quickStage struct {
 ID string `json:"id"`
 Operation string `json:"operation"`
 Sources []string `json:"sources"`
 Targets []string `json:"targets"`
 Conserved []string `json:"conserved"`
 Drivers []quickDriver `json:"drivers"`
}
type quickTemplate struct {
 Template string `json:"template"`
 Title string `json:"title"`
 Precision int `json:"precision"`
 Dimensions []string `json:"dimensions"`
 RoundingPolicy string `json:"rounding_policy"`
 Stages []quickStage `json:"stages"`
 Warnings []string `json:"warnings"`
 Note string `json:"note"`
}
func exactPercent(raw string)(string,error){
 if raw=="" {raw="1"}
 n,err:=engine.ParseScaled(raw,6)
 if err!=nil||n<0||n>1000000 {return "",fmt.Errorf("invalid share %q",raw)}
 frac:=strings.TrimRight(fmt.Sprintf("%04d",n%10000),"0")
 if frac!="" {frac="."+frac}
 return fmt.Sprintf("%d%s%%",n/10000,frac),nil
}
func explainTemplateForQuickstart(filename string,spec engine.Spec)(quickTemplate,error){
 out:=quickTemplate{Template:filename,Title:spec.Name,Precision:spec.Precision,Dimensions:append([]string(nil),spec.Dimensions...),
 RoundingPolicy:spec.RoundingPolicy,Stages:[]quickStage{},Warnings:[]string{},
 Note:"仅说明当前模板；科目独立覆盖可能改变最终规则。请在保存后查看生效规则，再执行体检、计算和独立参考对账。"}
 if out.RoundingPolicy=="" {out.RoundingPolicy=engine.RoundingLargestRemainderIdentity}
 plan,err:=engine.Compile(spec);if err!=nil{return out,err}
 for _,stage:=range plan.Ordered{
  if !engine.Enabled(stage.Enabled){continue}
  op:=stage.Operation;if op==""{op="ALLOCATE"}
  item:=quickStage{ID:stage.ID,Operation:op,Sources:[]string{},Targets:[]string{},Conserved:append([]string(nil),stage.ConservationKeys...),Drivers:[]quickDriver{}}
  for _,input:=range stage.Inputs{item.Sources=append(item.Sources,input.From)}
  for _,dim:=range spec.Dimensions{
   target:=stage.Target[dim]
   if target.Mode!="SOURCE"{item.Targets=append(item.Targets,dim+"（"+target.Mode+"）")}
  }
  if op=="ALLOCATE"{
   for _,alloc:=range stage.Allocations{
    if !engine.Enabled(alloc.Enabled){continue}
    percent,e:=exactPercent(alloc.Share);if e!=nil{return out,e}
    policy:=alloc.NegativePolicy;if policy==""{policy="REJECT"}
    item.Drivers=append(item.Drivers,quickDriver{Name:alloc.ID,Dataset:alloc.DriverDataset,Field:alloc.WeightField,Percent:percent,NegativePolicy:policy,SignedReason:alloc.SignedDriverReason})
    if policy=="ALLOW_SIGNED"{out.Warnings=append(out.Warnings,"阶段「"+stage.ID+"」的因子「"+alloc.ID+"」允许负权重："+alloc.SignedDriverReason)}
   }
  }
  out.Stages=append(out.Stages,item)
 }
 if len(out.Stages)>1 {out.Warnings=append(out.Warnings,"该模板包含多个阶段，请核对每一阶段的来源、目标与守恒维度。")}
 return out,nil
}
func (x *MultiApp) templateGuide(w http.ResponseWriter,r *http.Request){
 if r.Method!=http.MethodGet{w.WriteHeader(http.StatusMethodNotAllowed);return}
 x.mu.Lock();defer x.mu.Unlock()
 dir,err:=x.projectDir(r.URL.Query().Get("name"));if err!=nil{fail(w,http.StatusNotFound,err);return}
 name:=r.URL.Query().Get("template")
 _,spec,err:=readProjectTemplate(dir,name);if err!=nil{fail(w,http.StatusUnprocessableEntity,err);return}
 guide,err:=explainTemplateForQuickstart(name,spec);if err!=nil{fail(w,http.StatusUnprocessableEntity,err);return}
 jsonOut(w,http.StatusOK,guide)
}
