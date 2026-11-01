package studio

import (
	"allocationcore/internal/engine"
	"allocationcore/internal/multi"
	"bufio"
	"container/heap"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type labQuery struct {
	Group, Search, Sort string
	Offset, Limit       int
	Filters             map[string]string
}
type labBucket struct {
	Member string `json:"member"`
	Amount string `json:"amount"`
	Rows   int64  `json:"rows"`
}
type labTable struct {
	Total             string              `json:"total"`
	Count             int64               `json:"count"`
	Rows              []map[string]string `json:"rows"`
	Groups            []labBucket         `json:"groups"`
	SHA256            string              `json:"sha256"`
	Offset            int                 `json:"offset"`
	Limit             int                 `json:"limit"`
	MixedCurrency     bool                `json:"mixed_currency"`
	FileMixedCurrency bool                `json:"file_mixed_currency"`
}

func labWriteCSV(path string, rows [][]string) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	w := csv.NewWriter(f)
	e = w.WriteAll(rows)
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func labDifference(a, b string, p int) string {
	x, e := engine.ParseScaled(a, p)
	if e != nil {
		return "—"
	}
	y, e := engine.ParseScaled(b, p)
	if e != nil {
		return "—"
	}
	return formatEvidenceTotal(new(big.Int).Sub(big.NewInt(x), big.NewInt(y)), p)
}

// Retain at most offset+limit rows for global ordering, never the entire CSV.
type labRow struct {
	values  map[string]string
	amount  int64
	ordinal int64
}
type labHeap struct {
	rows []labRow
	desc bool
}

func (h labHeap) Len() int           { return len(h.rows) }
func (h labHeap) Less(i, j int) bool { return labRowLess(h.rows[j], h.rows[i], h.desc) }
func (h labHeap) Swap(i, j int)      { h.rows[i], h.rows[j] = h.rows[j], h.rows[i] }
func (h *labHeap) Push(v any)        { h.rows = append(h.rows, v.(labRow)) }
func (h *labHeap) Pop() any          { n := len(h.rows); v := h.rows[n-1]; h.rows = h.rows[:n-1]; return v }
func labRowLess(a, b labRow, desc bool) bool {
	if a.amount == b.amount {
		return a.ordinal < b.ordinal
	}
	if desc {
		return a.amount > b.amount
	}
	return a.amount < b.amount
}

func scanLabCSV(r *http.Request, path string, precision int, dims []string, q labQuery) (labTable, error) {
	out := labTable{Rows: []map[string]string{}, Groups: []labBucket{}, Offset: q.Offset, Limit: q.Limit}
	st, e := os.Lstat(path)
	if e != nil {
		return out, e
	}
	if !st.Mode().IsRegular() {
		return out, errors.New("证据必须是普通文件")
	}
	f, e := os.Open(path)
	if e != nil {
		return out, e
	}
	defer f.Close()
	hash := sha256.New()
	cr := csv.NewReader(bufio.NewReaderSize(io.TeeReader(f, hash), 256<<10))
	header, e := cr.Read()
	if e != nil {
		return out, e
	}
	header[0] = strings.TrimPrefix(header[0], "\ufeff")
	cols, e := engine.HeaderPositions(header)
	if e != nil {
		return out, e
	}
	ai, ok := cols["amount"]
	if !ok {
		ai, ok = cols["value"]
	}
	if !ok {
		return out, errors.New("金额字段缺失")
	}
	for _, d := range dims {
		if _, ok := cols[d]; !ok {
			return out, fmt.Errorf("维度缺失：%s", d)
		}
	}
	for k := range q.Filters {
		if _, ok := cols[k]; !ok {
			return out, fmt.Errorf("筛选维度不存在：%s", k)
		}
	}
	gi, ok := cols[q.Group]
	if !ok {
		return out, errors.New("请选择实际存在的汇总维度")
	}
	totals := map[string]*big.Int{}
	counts := map[string]int64{}
	total := new(big.Int)
	h := &labHeap{desc: q.Sort != "asc"}
	firstCurrency := ""
	hasCurrency := false
	fileCurrency := ""
	hasFileCurrency := false
	for ordinal := int64(0); ; ordinal++ {
		if e := r.Context().Err(); e != nil {
			return out, e
		}
		row, e := cr.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return out, e
		}
		if ci, ok := cols["currency"]; ok {
			if !hasFileCurrency {
				fileCurrency = row[ci]
				hasFileCurrency = true
			} else if fileCurrency != row[ci] {
				out.FileMixedCurrency = true
			}
		}
		match := true
		for k, v := range q.Filters {
			if row[cols[k]] != v {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if q.Search != "" && !strings.Contains(strings.ToLower(strings.Join(row, " · ")), strings.ToLower(q.Search)) {
			continue
		}
		n, e := engine.ParseScaled(row[ai], precision)
		if e != nil {
			return out, e
		}
		if ci, ok := cols["currency"]; ok {
			if !hasCurrency {
				firstCurrency = row[ci]
				hasCurrency = true
			} else if firstCurrency != row[ci] {
				out.MixedCurrency = true
			}
		}
		member := row[gi]
		if totals[member] == nil {
			if len(totals) >= 10000 {
				return out, errors.New("汇总成员超过 10,000；请增加筛选或选择较粗维度。未返回不完整金额。")
			}
			totals[member] = new(big.Int)
		}
		totals[member].Add(totals[member], big.NewInt(n))
		counts[member]++
		total.Add(total, big.NewInt(n))
		out.Count++
		v := labRow{amount: n, ordinal: ordinal}
		if h.Len() < q.Offset+q.Limit || labRowLess(v, h.rows[0], h.desc) {
			v.values = map[string]string{}
			for i, k := range header {
				v.values[k] = row[i]
			}
			heap.Push(h, v)
			if h.Len() > q.Offset+q.Limit {
				heap.Pop(h)
			}
		}
	}
	sort.Slice(h.rows, func(i, j int) bool { return labRowLess(h.rows[i], h.rows[j], h.desc) })
	for i := q.Offset; i < len(h.rows); i++ {
		out.Rows = append(out.Rows, h.rows[i].values)
	}
	members := make([]string, 0, len(totals))
	for k := range totals {
		members = append(members, k)
	}
	sort.Slice(members, func(i, j int) bool {
		a, b := new(big.Int).Abs(totals[members[i]]), new(big.Int).Abs(totals[members[j]])
		c := a.Cmp(b)
		if c == 0 {
			return members[i] < members[j]
		}
		return c > 0
	})
	other := new(big.Int)
	var otherRows int64
	for i, k := range members {
		if i < 12 {
			out.Groups = append(out.Groups, labBucket{k, formatEvidenceTotal(totals[k], precision), counts[k]})
		} else {
			other.Add(other, totals[k])
			otherRows += counts[k]
		}
	}
	if len(members) > 12 {
		out.Groups = append(out.Groups, labBucket{"其他（" + strconv.Itoa(len(members)-12) + " 个成员）", formatEvidenceTotal(other, precision), otherRows})
	}
	out.Total = formatEvidenceTotal(total, precision)
	out.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return out, nil
}

func (x *MultiApp) labRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	select {
	case labSlots <- struct{}{}:
		defer func() { <-labSlots }()
	default:
		fail(w, 429, errors.New("证据扫描繁忙，请稍后重试"))
		return
	}
	qv := r.URL.Query()
	name, id, account := qv.Get("name"), qv.Get("job"), qv.Get("account")
	if !multiSafe.MatchString(name) || !safeJobID(id) || account == "" || len(account) > 256 {
		fail(w, 400, errors.New("请选择项目、成功任务和科目"))
		return
	}
	q := labQuery{Group: qv.Get("group"), Search: qv.Get("search"), Sort: qv.Get("sort"), Limit: 50, Filters: map[string]string{"account": account}}
	if len(q.Search) > 128 || len(qv.Get("filters")) > 4096 {
		fail(w, 400, errors.New("筛选条件过长"))
		return
	}
	if v := qv.Get("offset"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 0 || n > 10000 {
			fail(w, 400, errors.New("页偏移范围 0..10000；请缩小筛选范围或下载完整结果"))
			return
		}
		q.Offset = n
	}
	if v := qv.Get("filters"); v != "" {
		if e := json.Unmarshal([]byte(v), &q.Filters); e != nil || q.Filters == nil {
			fail(w, 400, errors.New("无效维度筛选"))
			return
		}
		q.Filters["account"] = account
	}
	root := filepath.Join(x.parent.data, "multi-jobs")
	job, e := readJournal(root, id, true)
	if e != nil || job.Case != name || job.Status != "PASS" {
		fail(w, 404, errors.New("该项目没有这个已完成任务"))
		return
	}
	dir := filepath.Join(root, id)
	raw, e := readSmallRegular(filepath.Join(dir, "run_summary.json"), 32<<20)
	if e != nil {
		fail(w, 422, e)
		return
	}
	var result multi.Result
	if json.Unmarshal(raw, &result) != nil || result.Status != "PASS" {
		fail(w, 422, errors.New("运行摘要无效"))
		return
	}
	var selected *multi.Group
	for i := range result.Engines {
		for _, a := range result.Engines[i].Accounts {
			if a == account {
				if selected != nil {
					fail(w, 422, errors.New("科目属于多个组"))
					return
				}
				selected = &result.Engines[i]
			}
		}
	}
	if selected == nil || selected.Status != "PASS" || !validGroupID(selected.ID) {
		fail(w, 422, errors.New("科目没有保存的规则组"))
		return
	}
	group := filepath.Join(dir, "groups", selected.ID)
	// Reject symlink directories before following any fixed evidence paths.
	for _, p := range []string{dir, filepath.Join(dir, "groups"), group, filepath.Join(group, "output")} {
		st, e := os.Lstat(p)
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			fail(w, 422, errors.New("历史证据目录无效"))
			return
		}
	}
	ruleRaw, e := readSmallRegular(filepath.Join(group, "rule.json"), 4<<20)
	if e != nil {
		fail(w, 422, e)
		return
	}
	spec, e := engine.ParseSpec(ruleRaw)
	if e != nil {
		fail(w, 422, e)
		return
	}
	plan, e := engine.Compile(spec)
	if e != nil {
		fail(w, 422, e)
		return
	}
	if q.Group == "" {
		q.Group = spec.Dimensions[len(spec.Dimensions)-1]
	}
	allowed := map[string]bool{}
	for _, d := range spec.Dimensions {
		allowed[d] = true
	}
	if !allowed[q.Group] {
		fail(w, 400, errors.New("汇总维度不在本次运行契约中"))
		return
	}
	for k := range q.Filters {
		if !allowed[k] {
			fail(w, 400, fmt.Errorf("筛选维度不存在：%s", k))
			return
		}
	}
	summaryRaw, e := readSmallRegular(filepath.Join(group, "output", "run_summary.json"), 4<<20)
	if e != nil {
		fail(w, 422, e)
		return
	}
	var summary engine.Summary
	if json.Unmarshal(summaryRaw, &summary) != nil || summary.Status != "PASS" {
		fail(w, 422, errors.New("Core 摘要无效"))
		return
	}
	hash := sha256.Sum256(ruleRaw)
	if summary.RuleSHA256 != hex.EncodeToString(hash[:]) {
		fail(w, 422, errors.New("历史规则指纹不一致"))
		return
	}
	before, e := scanLabCSV(r, filepath.Join(group, "group_amounts.csv"), spec.Precision, spec.Dimensions, q)
	if e != nil {
		fail(w, 422, e)
		return
	}
	after, e := scanLabCSV(r, filepath.Join(group, "output", "results.csv"), spec.Precision, spec.Dimensions, q)
	if e != nil {
		fail(w, 422, e)
		return
	}
	sourceVerified := false
	drivers := map[string]string{}
	for p, h := range summary.DataSHA256 {
		if filepath.Base(p) == "group_amounts.csv" {
			if h != before.SHA256 {
				fail(w, 422, errors.New("历史来源金额指纹不一致"))
				return
			}
			sourceVerified = true
		} else {
			drivers[filepath.Base(p)] = h
		}
	}
	integrity := "PARTIAL · 旧任务缺少完整证据清单"
	mr, me := readSmallRegular(filepath.Join(group, "output", "evidence_stream_manifest.json"), 1<<20)
	if me == nil {
		var m engine.EvidenceStreamManifest
		if json.Unmarshal(mr, &m) != nil || m.Files["results.csv"].StoredSHA256 != after.SHA256 {
			fail(w, 422, errors.New("最终结果与原始证据清单不一致"))
			return
		}
		summaryHash := sha256.Sum256(summaryRaw)
		if m.Files["run_summary.json"].StoredSHA256 != hex.EncodeToString(summaryHash[:]) {
			fail(w, 422, errors.New("Core 摘要与原始证据清单不一致"))
			return
		}
		if sourceVerified {
			integrity = "PASS · 来源及最终结果文件指纹核对通过"
		}
	} else if !os.IsNotExist(me) {
		fail(w, 422, me)
		return
	}
	report := labReport{Mode: "HISTORICAL", Status: result.Status, Precision: spec.Precision, Dimensions: spec.Dimensions, Input: before.Total, Output: after.Total, Difference: labDifference(after.Total, before.Total, spec.Precision), Before: before, After: after, RuleSHA: summary.RuleSHA256, DriverSHA: drivers, Checks: map[string]string{"conservation": "PASS · Core 原任务逐池检查；当前筛选的净变化不等同守恒结论", "rules": "PASS · 历史 Core 运行校验", "scope": "NOT_TESTED · 未提供原系统完整范围证明", "reference": "NOT_TESTED · 请在独立对账区选择历史报告", "publication": "PASS · Multi 结果已生成；不代表 Vault 发布或业务验收", "integrity": integrity}, Note: "金额卡片和明细仅含所选科目与筛选；阶段输入输出属于整个历史规则组（" + strings.Join(selected.Accounts, "、") + "），不会冒充当前筛选金额。阶段间严格按 inputs 依赖连接，中间金额不计入最终总额。筛选目标维度时来源可能为 0，这表示粒度迁移，不代表资金丢失。"}
	report.ProcessInput, report.ProcessOutput = summary.InputAmount, summary.OutputAmount
	for _, st := range plan.Ordered {
		ls := labStage{ID: st.ID, Operation: st.Operation, Enabled: st.Enabled, Inputs: st.Inputs, Source: st.Source, Target: st.Target, Allocations: st.Allocations, Status: "NOT_TESTED"}
		for _, ss := range summary.Stages {
			if ss.Stage == st.ID {
				ls.Input = ss.Input
				ls.Output = ss.Output
				ls.Status = "PASS"
			}
		}
		report.Stages = append(report.Stages, ls)
	}
	jsonOut(w, 200, report)
}
