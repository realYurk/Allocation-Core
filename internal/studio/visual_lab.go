package studio

// Visual Lab is a bounded presentation adapter. All allocation is performed by
// engine.RunWithOptions; it never writes a business project or publishes a job.
import (
	"allocationcore/internal/engine"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

var labSlots = make(chan struct{}, 2)

type labRequest struct {
	Example string   `json:"example"`
	Amount  string   `json:"amount"`
	Amount2 string   `json:"amount2"`
	Weights []string `json:"weights"`
	Targets []bool   `json:"targets"`
	Fault   string   `json:"fault"`
	Group   string   `json:"group"`
}
type labStage struct {
	ID          string                   `json:"id"`
	Operation   string                   `json:"operation"`
	Enabled     *bool                    `json:"enabled"`
	Inputs      []engine.InputEdge       `json:"inputs"`
	Source      engine.Source            `json:"source"`
	Target      map[string]engine.Target `json:"target"`
	Allocations []engine.Allocation      `json:"allocations"`
	Input       string                   `json:"input,omitempty"`
	Output      string                   `json:"output,omitempty"`
	Status      string                   `json:"status"`
}
type labReport struct {
	Mode          string              `json:"mode"`
	Status        string              `json:"status"`
	Precision     int                 `json:"precision"`
	Dimensions    []string            `json:"dimensions"`
	Input         string              `json:"input"`
	Output        string              `json:"output"`
	Difference    string              `json:"difference"`
	ProcessInput  string              `json:"process_input"`
	ProcessOutput string              `json:"process_output"`
	Stages        []labStage          `json:"stages"`
	Before        labTable            `json:"before"`
	After         labTable            `json:"after"`
	Ledger        []map[string]string `json:"ledger,omitempty"`
	Checks        map[string]string   `json:"checks"`
	RuleSHA       string              `json:"rule_sha256"`
	DriverSHA     map[string]string   `json:"driver_sha256"`
	Note          string              `json:"note"`
}

func (x *MultiApp) registerLab(m *http.ServeMux) {
	m.HandleFunc("/lab", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		b, e := publicFS.ReadFile("public/lab.html")
		if e != nil {
			fail(w, 500, e)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	m.HandleFunc("/api/lab/demo", x.labDemo)
	m.HandleFunc("/api/lab/run", x.labRun)
}

func (x *MultiApp) labDemo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	select {
	case labSlots <- struct{}{}:
		defer func() { <-labSlots }()
	default:
		fail(w, 429, errors.New("实验室繁忙，请稍后重试"))
		return
	}
	var req labRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&req); e != nil {
		fail(w, 400, e)
		return
	}
	if e := dec.Decode(new(any)); e != io.EOF {
		fail(w, 400, errors.New("只接受一个实验参数对象"))
		return
	}
	dir, e := os.MkdirTemp("", "allocation-visual-lab-")
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer os.RemoveAll(dir)
	spec, e := buildLabDemo(dir, req)
	if e != nil {
		fail(w, 422, e)
		return
	}
	if e = r.Context().Err(); e != nil {
		return
	}
	raw, _ := json.MarshalIndent(spec, "", "  ")
	if e = os.WriteFile(filepath.Join(dir, "rule.json"), raw, 0600); e != nil {
		fail(w, 500, e)
		return
	}
	summary, e := engine.RunWithOptions(filepath.Join(dir, "rule.json"), filepath.Join(dir, "output"), engine.RunOptions{Workers: 1})
	if e != nil {
		jsonOut(w, 422, map[string]any{"error": e.Error(), "status": "BLOCK", "mode": "SYNTHETIC", "published": false})
		return
	}
	group := req.Group
	if group == "" {
		group = "model"
	}
	if group != "model" && group != "sku" && group != "account" {
		fail(w, 400, errors.New("未知演示汇总维度"))
		return
	}
	q := labQuery{Group: group, Limit: 50, Filters: map[string]string{}}
	before, e := scanLabCSV(r, filepath.Join(dir, "before.csv"), spec.Precision, spec.Dimensions, q)
	if e != nil {
		fail(w, 500, e)
		return
	}
	after, e := scanLabCSV(r, filepath.Join(dir, "output", "results.csv"), spec.Precision, spec.Dimensions, q)
	if e != nil {
		fail(w, 500, e)
		return
	}
	ledger, e := readCSV(filepath.Join(dir, "output", "ledger.csv"), 100)
	if e != nil {
		fail(w, 500, e)
		return
	}
	report := labReport{Mode: "SYNTHETIC", Status: summary.Status, Precision: spec.Precision, Dimensions: spec.Dimensions, Input: summary.InputAmount, Output: summary.OutputAmount, Difference: labDifference(summary.OutputAmount, summary.InputAmount, spec.Precision), Before: before, After: after, Ledger: ledger, RuleSHA: summary.RuleSHA256, DriverSHA: summary.DataSHA256, Checks: map[string]string{"conservation": "PASS", "rules": "PASS · Core 校验和计算通过", "scope": "PASS · 仅合成样例定义的范围", "reference": "NOT_TESTED · 本次未运行独立参考对账", "publication": "UNPUBLISHED · 合成数据 / 未发布"}, Note: "所有金额由本机 Core 计算。阶段金额为中间流转，不与最终金额相加。样例不代表企业业务规则，也不代表生产验收通过。"}
	report.ProcessInput, report.ProcessOutput = summary.InputAmount, summary.OutputAmount
	report.DriverSHA = map[string]string{}
	for path, hash := range summary.DataSHA256 {
		report.DriverSHA[filepath.Base(path)] = hash
	}
	if summary.PoolChecks == 0 && summary.InputAmount != "0.00" {
		report.Status = "PARTIAL"
		report.Checks["scope"] = "PARTIAL · 没有命中分摊来源池；金额原样保留，尚未完成预期分摊"
		report.Note += " 来源过滤没有命中时，Core 将原金额透传到最终结果。守恒不代表目标分摊已发生，请检查来源范围。"
	}
	for _, st := range spec.Stages {
		s := labStage{ID: st.ID, Operation: st.Operation, Enabled: st.Enabled, Inputs: st.Inputs, Source: st.Source, Target: st.Target, Allocations: st.Allocations, Status: "PASS"}
		for _, ss := range summary.Stages {
			if ss.Stage == st.ID {
				s.Input = ss.Input
				s.Output = ss.Output
			}
		}
		report.Stages = append(report.Stages, s)
	}
	jsonOut(w, 200, report)
}

func buildLabDemo(dir string, req labRequest) (engine.Spec, error) {
	s := engine.Spec{Schema: engine.Schema, Name: "Visual Lab · 合成数据 / 未发布", Precision: 2, Dimensions: []string{"account", "period", "currency", "model", "sku"}, Immutable: []string{"account", "period", "currency"}}
	if req.Example != "A" && req.Example != "B" && req.Example != "C" && req.Example != "D" {
		return s, errors.New("未知合成样例")
	}
	if req.Fault != "" && req.Fault != "zero" && req.Fault != "target" && req.Fault != "scope" {
		return s, errors.New("未知异常场景")
	}
	if len(req.Weights) != 6 || len(req.Targets) != 2 {
		return s, errors.New("需要 6 个权重和 2 个目标开关")
	}
	for _, v := range []string{req.Amount, req.Amount2} {
		n, e := engine.ParseScaled(v, 2)
		if e != nil {
			return s, e
		}
		if n > 100000000000 || n < -100000000000 {
			return s, errors.New("合成实验金额范围为 ±10 亿元")
		}
	}
	for _, v := range req.Weights {
		n, e := engine.ParseScaled(v, 6)
		if e != nil {
			return s, e
		}
		if n < 0 || n > 1000000000 {
			return s, errors.New("样例权重须在 0..1000，最多 6 位小数")
		}
	}
	s.Datasets.Amounts = map[string]string{"expenses": "before.csv"}
	s.Datasets.Drivers = map[string]string{}
	amounts := [][]string{{"row_id", "account", "period", "currency", "model", "sku", "amount"}, {"S1", "E1", "2026-01", "CNY", "未分配", "未分配", req.Amount}}
	if req.Example == "C" {
		amounts = append(amounts, []string{"S2", "E2", "2026-01", "CNY", "未分配", "未分配", req.Amount2})
	}
	if e := labWriteCSV(filepath.Join(dir, "before.csv"), amounts); e != nil {
		return s, e
	}
	add := func(id, from, alias, dim, account string, targets []string) engine.Stage {
		st := engine.Stage{ID: id, Inputs: []engine.InputEdge{{From: from, Share: "1"}}, Source: engine.Source{Grouping: map[string]string{}, Filters: map[string]engine.Filter{}}, Target: map[string]engine.Target{}, ConservationKeys: []string{"account", "period", "currency"}, Allocations: []engine.Allocation{{ID: alias, Share: "1", DriverDataset: alias, WeightField: "weight", NegativePolicy: "REJECT"}}}
		for _, d := range s.Dimensions {
			st.Source.Grouping[d] = "AS_IS"
			st.Target[d] = engine.Target{Mode: "SOURCE"}
		}
		st.Source.Grouping[dim] = "TOTAL"
		st.Target[dim] = engine.Target{Mode: "INCLUDE", Include: targets}
		if account != "" {
			st.Source.Filters["account"] = engine.Filter{Include: []string{account}}
		}
		s.Datasets.Drivers[alias] = alias + ".csv"
		return st
	}
	targets := []string{}
	for i, t := range []string{"车型 A", "车型 B"} {
		if req.Targets[i] {
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		return s, errors.New("目标集合为空：至少选择一个参与目标")
	}
	w := append([]string(nil), req.Weights...)
	if req.Example == "D" && req.Fault == "zero" {
		w[0] = "0"
		w[1] = "0"
	}
	if req.Example == "D" && req.Fault == "target" {
		targets = []string{"不存在的车型"}
	}
	st := add("车型分摊", "expenses", "工时", "model", "", targets)
	if req.Example == "C" {
		st.Source.Filters["account"] = engine.Filter{Include: []string{"E1"}}
	}
	if req.Example == "D" && req.Fault == "scope" {
		st.Source.Filters["account"] = engine.Filter{Include: []string{"不存在的科目"}}
	}
	s.Stages = append(s.Stages, st)
	if e := labWriteCSV(filepath.Join(dir, "工时.csv"), [][]string{{"model", "weight"}, {"车型 A", w[0]}, {"车型 B", w[1]}}); e != nil {
		return s, e
	}
	if req.Example == "B" {
		st = add("细分车型", "车型分摊", "销量", "sku", "", []string{"A1", "A2", "B1", "B2"})
		s.Stages = append(s.Stages, st)
		if e := labWriteCSV(filepath.Join(dir, "销量.csv"), [][]string{{"model", "sku", "weight"}, {"车型 A", "A1", w[2]}, {"车型 A", "A2", w[3]}, {"车型 B", "B1", w[4]}, {"车型 B", "B2", w[5]}}); e != nil {
			return s, e
		}
	}
	if req.Example == "C" {
		// Distinct datasets avoid routing a source twice; the two accounts have
		// independent rules and driver semantics, exactly as in Multi.
		s.Datasets.Amounts = map[string]string{"expenses": "e1.csv", "expenses2": "e2.csv"}
		for i, name := range []string{"e1.csv", "e2.csv"} {
			if e := labWriteCSV(filepath.Join(dir, name), [][]string{amounts[0], amounts[i+1]}); e != nil {
				return s, e
			}
		}
		st = add("E2 按销量", "expenses2", "销量", "model", "E2", targets)
		s.Stages = append(s.Stages, st)
		if e := labWriteCSV(filepath.Join(dir, "销量.csv"), [][]string{{"model", "weight"}, {"车型 A", w[2]}, {"车型 B", w[3]}}); e != nil {
			return s, e
		}
	}
	// Core preserves unmatched source rows. The view must distinguish that
	// conserved pass-through from a successful allocation to intended targets.
	if _, e := engine.Compile(s); e != nil {
		return s, fmt.Errorf("Core 规则检查：%w", e)
	}
	return s, nil
}
