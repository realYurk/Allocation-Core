package multi

// Business-facing findings are derived only from read-only input inspection.
// They never infer target-level Driver eligibility or change allocation policy.
import (
    "fmt"
    "sort"
    "strings"
)

// DriverWeightSignal describes a whole CSV column, not per-pool eligibility.
type DriverWeightSignal struct {
    Field string `json:"field"`
    PositiveRows int64 `json:"positive_rows"`
    ZeroRows int64 `json:"zero_rows"`
    NegativeRows int64 `json:"negative_rows"`
    AffectedAccounts int `json:"affected_accounts"`
    AccountExamples []string `json:"account_examples"`
}
type BusinessFinding struct {
    Level string `json:"level"`
    Code string `json:"code"`
    Title string `json:"title"`
    Detail string `json:"detail"`
    NextStep string `json:"next_step"`
    Driver string `json:"driver,omitempty"`
    Field string `json:"field,omitempty"`
    AffectedAccounts int `json:"affected_accounts,omitempty"`
    AccountExamples []string `json:"account_examples,omitempty"`
}
type AccountReadiness struct {
    Account string `json:"account"`
    Template string `json:"template"`
    InputRows int64 `json:"input_rows"`
    Status string `json:"status"` // INPUT_OK, CHECK_DRIVER, BLOCK
    Notes []string `json:"notes"`
}
func businessAdvice(problem string) (string, string) {
    p:=strings.ToLower(problem)
    switch {
    case strings.Contains(p,"no assigned rule"):
        return "金额文件包含未分派规则的科目", "在第三步核对科目编码，为该科目选择模板后重新体检。"
    case strings.Contains(p,"has a rule but no input amount"):
        return "已配置规则的科目没有金额明细", "检查金额来源的科目编码、期间筛选或删去不需要的科目分派。"
    case strings.Contains(p,"missing csv column"),strings.Contains(p,"missing dimension"),strings.Contains(p,"column count"),strings.Contains(p,"missing weight"):
        return "数据字段不符合当前规则", "打开金额或 Driver 字段映射，检查原始列名与必需字段，然后重新导入。"
    case strings.Contains(p,"invalid decimal"),strings.Contains(p,"decimal scale"),strings.Contains(p,"invalid number"),strings.Contains(p,"overflow"):
        return "金额或因子数字格式不合法", "查看报错行及字段；使用明确的小数格式，检查精度和极端数值。"
    case strings.Contains(p,"configuration changed"):
        return "检查期间配置被其他操作修改", "刷新项目重新体检，不要使用过期的规则或数据快照。"
    case strings.Contains(p,"driver"),strings.Contains(p,"no such file"):
        return "Driver 文件或规则引用不可用", "核对 Driver 文件是否存在、是否已正确映射，并检查所选模板。"
    case strings.Contains(p,"canceled"):
        return "体检已取消", "重新执行体检；不能将不完整报告视作通过。"
    default:
        return "输入或配置未通过检查", "查看原始错误详情，修复数据或规则后重新体检。"
    }
}
func sortedSamples(set map[string]bool, limit int) ([]string,int) {
    keys:=make([]string,0,len(set))
    for k:=range set {keys=append(keys,k)}
    sort.Strings(keys)
    count:=len(keys)
    if len(keys)>limit {keys=keys[:limit]}
    return keys,count
}
// addBusinessDiagnostics runs on every exit, including early BLOCK failures.
// CHECK is an input signal, not an assertion that an allocation will fail.
func addBusinessDiagnostics(report *PreflightReport, p *project, inputCounts map[string]int64) {
    report.Findings=make([]BusinessFinding,0)
    report.AccountDetails=make([]AccountReadiness,0)
    report.DataMatchVerified=false
    for _,problem:=range report.Problems {
        title,action:=businessAdvice(problem)
        report.Findings=append(report.Findings,BusinessFinding{
            Level:"BLOCK",Code:"INPUT_OR_RULE_ERROR",Title:title,Detail:problem,NextStep:action,
        })
    }
    accountWarnings:=map[string][]string{}
    for _,ds:=range report.Drivers {
        for _,sig:=range ds.WeightSignals {
            if sig.PositiveRows==0 && sig.NegativeRows==0 && sig.ZeroRows>0 {
                note:=fmt.Sprintf("Driver %s 的 %s 字段全部为零，实际使用时可能出现无有效权重",ds.Name,sig.Field)
                report.Findings=append(report.Findings,BusinessFinding{
                    Level:"CHECK",Code:"ALL_ZERO_DRIVER_FIELD",Title:"因子字段全为零",Detail:note,
                    NextStep:"检查原始业务数量、期间和筛选范围；调整 Driver 后重新体检并执行真实分摊。",
                    Driver:ds.Name,Field:sig.Field,AffectedAccounts:sig.AffectedAccounts,AccountExamples:sig.AccountExamples,
                })
                for account:=range ds.allAffected[sig.Field] {
                    accountWarnings[account]=append(accountWarnings[account],"全零因子："+ds.Name+" / "+sig.Field)
                }
            }
            if sig.NegativeRows>0 {
                note:=fmt.Sprintf("Driver %s 的 %s 字段有 %d 行负权重；文件级检查无法确认这些行会被哪些规则选中",ds.Name,sig.Field,sig.NegativeRows)
                report.Findings=append(report.Findings,BusinessFinding{
                    Level:"CHECK",Code:"NEGATIVE_DRIVER_FIELD",Title:"发现负权重，需要核对业务政策",Detail:note,
                    NextStep:"确认是否属于冲销或抵扣。普通费用规则应拒绝负权重；确需使用时，在规则中声明用途，再执行分摊验证。",
                    Driver:ds.Name,Field:sig.Field,AffectedAccounts:sig.AffectedAccounts,AccountExamples:sig.AccountExamples,
                })
                for account:=range ds.allAffected[sig.Field] {
                    accountWarnings[account]=append(accountWarnings[account],"存在负权重："+ds.Name+" / "+sig.Field)
                }
            }
        }
    }
    if p!=nil && len(p.assign)>0 {
        for _,account:=range sortedAccounts(p.assign) {
            notes:=accountWarnings[account]
            status:="INPUT_OK"
            if len(notes)>0 {status="CHECK_DRIVER";report.ReviewAccounts++}
            if inputCounts!=nil && inputCounts[account]==0 {
                status="BLOCK"
                notes=append(notes,"未找到该科目的金额数据")
            }
            if status=="BLOCK" {report.BlockedAccounts++}
            report.AccountDetails=append(report.AccountDetails,AccountReadiness{
                Account:account,Template:p.assign[account],InputRows:inputCounts[account],Status:status,Notes:notes,
            })
        }
    }
    for _,f:=range report.Findings {if f.Level=="CHECK" {report.CheckCount++}}
}
