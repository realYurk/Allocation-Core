package multi

// Read-only business explanation derived from the same effective plan that
// Multi runs. Never exposes absolute local driver paths or evaluates amounts.
import (
 "allocationcore/internal/engine"
 "crypto/sha256"
 "encoding/hex"
 "errors"
 "fmt"
 "path/filepath"
 "strings"
)
type ExplainedDimension struct{
 Name string `json:"name"`
 Grouping string `json:"grouping"`
 TargetMode string `json:"target_mode"`
 TargetValue string `json:"target_value,omitempty"`
 Include []string `json:"include,omitempty"`
 Exclude []string `json:"exclude,omitempty"`
 Conserved bool `json:"conserved"`
}
type ExplainedDriver struct{
 Allocation string `json:"allocation"`
 Dataset string `json:"dataset"`
 File string `json:"file"`
 WeightField string `json:"weight_field"`
 Share string `json:"share"`
 NegativePolicy string `json:"negative_policy"`
 Enabled bool `json:"enabled"`
 Filters map[string]engine.Filter `json:"filters,omitempty"`
}
type ExplainedStage struct{
 ID string `json:"id"`
 Operation string `json:"operation"`
 Enabled bool `json:"enabled"`
 Inputs []engine.InputEdge `json:"inputs"`
 ConservationKeys []string `json:"conservation_keys"`
 SourceFilters map[string]engine.Filter `json:"source_filters,omitempty"`
 Dimensions []ExplainedDimension `json:"dimensions"`
 Drivers []ExplainedDriver `json:"drivers"`
}
type AccountExplanation struct{
 Account string `json:"account"`
 Template string `json:"template"`
 RuleName string `json:"rule_name"`
 RuleSHA256 string `json:"rule_sha256"`
 GroupID string `json:"effective_group_id"`
 Precision int `json:"precision"`
 Dimensions []string `json:"dimensions"`
 ImmutableDimensions []string `json:"immutable_dimensions"`
 Stages []ExplainedStage `json:"stages"`
 Note string `json:"note"`
}
func onOrDefault(flag *bool) bool{return flag==nil||*flag}

// Describe only the current SAVED rule. The authoritative project.initialize()
// applies every account override, validates per-account contracts and assigns
// an effective group ID shared with the actual Multi execution path.
func ExplainAccount(projectDir, account string)(AccountExplanation,error){
 out:=AccountExplanation{}
 if account==""||len(account)>1024||strings.ContainsRune(account,'\x00'){return out,errors.New("invalid account")}
 p:=&project{dir:projectDir}
 if err:=p.initialize();err!=nil{return out,fmt.Errorf("cannot prepare effective rules: %w",err)}
 groupID,ok:=p.effective[account];if !ok{return out,errors.New("account is not assigned")}
 for _,group:=range p.groups{
  if group.ID!=groupID{continue}
  spec,err:=engine.ParseSpec([]byte(group.Rule))
  if err!=nil{return out,fmt.Errorf("cannot parse effective rule: %w",err)}
  plan,err:=engine.Compile(spec)
  if err!=nil{return out,fmt.Errorf("invalid effective stage graph: %w",err)}
  hash:=sha256.Sum256([]byte(group.Rule))
  out=AccountExplanation{
   Account:account,Template:p.assign[account],RuleName:spec.Name,
   RuleSHA256:hex.EncodeToString(hash[:]),GroupID:groupID,
   Precision:spec.Precision,Dimensions:append([]string{},spec.Dimensions...),
   ImmutableDimensions:append([]string{},spec.Immutable...),
   Stages:make([]ExplainedStage,0,len(plan.Ordered)),
   Note:"Current saved effective rule including account overrides. Not a completed allocation, driver coverage check, or reference-system acceptance.",
  }
  for _,stage:=range plan.Ordered{
   operation:=stage.Operation
   if operation==""{operation="ALLOCATE"}
   item:=ExplainedStage{
    ID:stage.ID,Operation:operation,Enabled:onOrDefault(stage.Enabled),
    Inputs:append([]engine.InputEdge{},stage.Inputs...),
    ConservationKeys:append([]string{},stage.ConservationKeys...),
    SourceFilters:stage.Source.Filters,
    Dimensions:make([]ExplainedDimension,0,len(spec.Dimensions)),
    Drivers:make([]ExplainedDriver,0,len(stage.Allocations)),
   }
   conserved:=map[string]bool{}
   for _,dim:=range stage.ConservationKeys{conserved[dim]=true}
   for _,dim:=range spec.Immutable{conserved[dim]=true}
   for _,dim:=range spec.Dimensions{
    target:=stage.Target[dim]
    item.Dimensions=append(item.Dimensions,ExplainedDimension{
     Name:dim,Grouping:stage.Source.Grouping[dim],TargetMode:target.Mode,
     TargetValue:target.Value,Include:target.Include,Exclude:target.Exclude,
     Conserved:conserved[dim],
    })
   }
   for _,allocation:=range stage.Allocations{
    share:=allocation.Share;if share==""{share="1"}
    policy:=allocation.NegativePolicy;if policy==""{policy="REJECT"}
    item.Drivers=append(item.Drivers,ExplainedDriver{
     Allocation:allocation.ID,Dataset:allocation.DriverDataset,
     File:filepath.Base(spec.Datasets.Drivers[allocation.DriverDataset]),
     WeightField:allocation.WeightField,Share:share,NegativePolicy:policy,
     Enabled:onOrDefault(allocation.Enabled),Filters:allocation.DriverFilters,
    })
   }
   out.Stages=append(out.Stages,item)
  }
  return out,nil
 }
 return AccountExplanation{},errors.New("effective rule group missing")
}
