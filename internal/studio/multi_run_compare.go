package studio

// Compare two immutable, successfully published Multi jobs without rerunning
// allocation formulas. Every attribution is evidence of change, not a claim
// that any individual changed driver/rule caused a particular result delta.
import (
	"allocationcore/internal/acceptance"
	"allocationcore/internal/multi"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// Limit concurrent potentially large partitioned comparisons per Studio process.
var runCompareSlots = make(chan struct{}, 2)

type runInputChange struct {
	File         string `json:"file"`
	Type         string `json:"type"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	AfterSHA256  string `json:"after_sha256,omitempty"`
}
type runRuleChange struct {
	Account      string `json:"account"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	AfterSHA256  string `json:"after_sha256,omitempty"`
}
type runComparison struct {
	ID                   string            `json:"id"`
	Project              string            `json:"project"`
	BeforeJob            string            `json:"before_job"`
	AfterJob             string            `json:"after_job"`
	InputChanges         []runInputChange  `json:"input_changes"`
	RuleChanges          []runRuleChange   `json:"rule_changes"`
	CoordinateComparison acceptance.Report `json:"coordinate_comparison"`
	DifferencesSHA256    string            `json:"differences_sha256"`
	Scope                string            `json:"scope"`
}

func classifyRunFile(path string) string {
	switch {
	case strings.HasPrefix(path, "drivers/"):
		return "DRIVER"
	case strings.HasPrefix(path, "templates/") || path == "assignments.csv" || path == "overrides.csv" || path == "project.json":
		return "CONFIGURATION"
	case strings.HasSuffix(path, ".csv"):
		return "AMOUNTS_OR_OTHER_CSV"
	default:
		return "OTHER_INPUT"
	}
}
func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && strings.ToLower(s) == s
}
func readRunInputHashes(dir string) (map[string]string, error) {
	b, e := readSmallRegular(filepath.Join(dir, "configuration", "input_sha256.json"), 4<<20)
	if e != nil {
		return nil, e
	}
	var hashes map[string]string
	if e = json.Unmarshal(b, &hashes); e != nil {
		return nil, e
	}
	if len(hashes) == 0 || len(hashes) > 10000 {
		return nil, errors.New("invalid saved input fingerprint count")
	}
	for k, v := range hashes {
		clean := filepath.ToSlash(filepath.Clean(k))
		if k == "" || filepath.IsAbs(k) || clean != k || clean == ".." || strings.HasPrefix(clean, "../") || !validSHA256(v) {
			return nil, errors.New("invalid saved input fingerprint")
		}
	}
	return hashes, nil
}
func compareInputHashes(before, after map[string]string) []runInputChange {
	names := map[string]bool{}
	for k := range before {
		names[k] = true
	}
	for k := range after {
		names[k] = true
	}
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]runInputChange, 0)
	for _, k := range keys {
		if before[k] != after[k] {
			out = append(out, runInputChange{File: k, Type: classifyRunFile(k), BeforeSHA256: before[k], AfterSHA256: after[k]})
		}
	}
	return out
}
func readRunRules(dir string) (map[string]string, int, error) {
	raw, e := readSmallRegular(filepath.Join(dir, "run_summary.json"), 32<<20)
	if e != nil {
		return nil, 0, e
	}
	var result multi.Result
	if e = json.Unmarshal(raw, &result); e != nil || result.Status != "PASS" {
		return nil, 0, errors.New("saved Multi summary is not PASS")
	}
	out := make(map[string]string)
	precision := -1
	for _, group := range result.Engines {
		if group.Status != "PASS" || !validGroupID(group.ID) {
			return nil, 0, errors.New("invalid saved rule group")
		}
		groupDir := filepath.Join(dir, "groups", group.ID)
		ruleRaw, er := readSmallRegular(filepath.Join(groupDir, "rule.json"), 4<<20)
		if er != nil {
			return nil, 0, er
		}
		var rule struct {
			Precision int `json:"precision"`
		}
		if er = json.Unmarshal(ruleRaw, &rule); er != nil || rule.Precision < 0 || rule.Precision > 6 {
			return nil, 0, errors.New("invalid saved group precision")
		}
		if precision != -1 && precision != rule.Precision {
			return nil, 0, errors.New("inconsistent group precision")
		}
		precision = rule.Precision
		summaryRaw, er := readSmallRegular(filepath.Join(groupDir, "output", "run_summary.json"), 4<<20)
		if er != nil {
			return nil, 0, er
		}
		var coreSummary struct {
			Status     string `json:"status"`
			RuleSHA256 string `json:"rule_sha256"`
		}
		if er = json.Unmarshal(summaryRaw, &coreSummary); er != nil || coreSummary.Status != "PASS" {
			return nil, 0, errors.New("saved Core summary is not PASS")
		}
		sha := sha256.Sum256(ruleRaw)
		hash := hex.EncodeToString(sha[:])
		if hash != coreSummary.RuleSHA256 {
			return nil, 0, errors.New("historical rule hash mismatch")
		}
		for _, account := range group.Accounts {
			if account == "" {
				return nil, 0, errors.New("empty saved account")
			}
			if _, exists := out[account]; exists {
				return nil, 0, errors.New("duplicate saved account")
			}
			out[account] = hash
		}
	}
	if len(out) == 0 || precision < 0 {
		return nil, 0, errors.New("no saved business accounts")
	}
	return out, precision, nil
}
func compareRuleHashes(before, after map[string]string) []runRuleChange {
	all := map[string]bool{}
	for k := range before {
		all[k] = true
	}
	for k := range after {
		all[k] = true
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]runRuleChange, 0)
	for _, name := range keys {
		if before[name] != after[name] {
			result = append(result, runRuleChange{Account: name, BeforeSHA256: before[name], AfterSHA256: after[name]})
		}
	}
	return result
}
func digestFile(ctx context.Context, path string) (string, error) {
	st, e := os.Lstat(path)
	if e != nil {
		return "", e
	}
	if !st.Mode().IsRegular() {
		return "", errors.New("historical result is not a regular file")
	}
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 256<<10)
	for {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		n, er := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			return "", er
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// stableComparisonID is content-addressed, not just a pair of job IDs.
// If either archived result, saved input fingerprint or effective rule changes,
// it produces a different report ID rather than reusing stale evidence. JSON
// sorts map keys, so independent processes derive the same ID.
func stableComparisonID(project, beforeJob, afterJob, beforeResultSHA, afterResultSHA string, beforeInputs, afterInputs, beforeRules, afterRules map[string]string) (string, error) {
	payload := struct {
		Project, BeforeJob, AfterJob, BeforeResultSHA, AfterResultSHA string
		BeforeInputs, AfterInputs, BeforeRules, AfterRules            map[string]string
	}{project, beforeJob, afterJob, beforeResultSHA, afterResultSHA, beforeInputs, afterInputs, beforeRules, afterRules}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sha := sha256.Sum256(data)
	return hex.EncodeToString(sha[:12]), nil
}

// An already-published comparison is reused only after its complete coordinate
// difference file is verified. Corrupted evidence blocks instead of silently
// recomputing and hiding the tamper. Legacy random-ID reports remain readable.
func readReusableRunComparison(ctx context.Context, root, id, project, beforeJob, afterJob, beforeHash, afterHash string, inputs []runInputChange, rules []runRuleChange) (runComparison, bool, error) {
	var report runComparison
	dir := filepath.Join(root, id)
	raw, err := readSmallRegular(filepath.Join(dir, "comparison.json"), 32<<20)
	if os.IsNotExist(err) {
		return report, false, nil
	}
	if err != nil {
		return report, true, err
	}
	if err = json.Unmarshal(raw, &report); err != nil {
		return report, true, err
	}
	if report.ID != id || report.Project != project || report.BeforeJob != beforeJob || report.AfterJob != afterJob ||
		report.CoordinateComparison.ReferenceSHA256 != beforeHash || report.CoordinateComparison.ActualSHA256 != afterHash ||
		!validSHA256(report.DifferencesSHA256) ||
		!reflect.DeepEqual(report.InputChanges, inputs) || !reflect.DeepEqual(report.RuleChanges, rules) ||
		(report.CoordinateComparison.Status != "MATCH" && report.CoordinateComparison.Status != "DIFFERENCES") {
		return report, true, errors.New("已保存的历史对比指纹不一致，不能复用")
	}
	sha, err := digestFile(ctx, filepath.Join(dir, "differences.csv"))
	if err != nil {
		return report, true, err
	}
	if sha != report.DifferencesSHA256 {
		return report, true, errors.New("已保存的坐标差异文件被修改，不能复用")
	}
	return report, true, nil
}

func (x *MultiApp) compareRuns(w http.ResponseWriter, r *http.Request) {
	root := filepath.Join(x.parent.data, "multi-run-comparisons")
	if r.Method == http.MethodGet {
		name, id := r.URL.Query().Get("name"), r.URL.Query().Get("id")
		if !multiSafe.MatchString(name) || !safeJobID(id) {
			fail(w, 400, errors.New("invalid project or comparison"))
			return
		}
		dir := filepath.Join(root, id)
		raw, e := readSmallRegular(filepath.Join(dir, "comparison.json"), 32<<20)
		if e != nil {
			fail(w, 404, e)
			return
		}
		var record runComparison
		if e = json.Unmarshal(raw, &record); e != nil || record.Project != name || record.ID != id || !validSHA256(record.DifferencesSHA256) {
			fail(w, 404, errors.New("comparison not found for this project"))
			return
		}
		if r.URL.Query().Get("download") == "csv" {
			path := filepath.Join(dir, "differences.csv")
			current, er := digestFile(r.Context(), path)
			if er != nil || current != record.DifferencesSHA256 {
				fail(w, 422, errors.New("comparison difference file was changed"))
				return
			}
			w.Header().Set("Content-Disposition", "attachment; filename=run_differences.csv")
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			http.ServeFile(w, r, path)
			return
		}
		jsonOut(w, 200, record)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name   string `json:"name"`
		Before string `json:"before"`
		After  string `json:"after"`
	}
	if e := multiRequest(w, r, &req, 2048); e != nil {
		fail(w, 400, e)
		return
	}
	if !multiSafe.MatchString(req.Name) || !safeJobID(req.Before) || !safeJobID(req.After) || req.Before == req.After {
		fail(w, 422, errors.New("请选择同项目的两个不同历史成功任务"))
		return
	}
	select {
	case runCompareSlots <- struct{}{}:
		defer func() { <-runCompareSlots }()
	case <-r.Context().Done():
		fail(w, 408, r.Context().Err())
		return
	}
	jobRoot := filepath.Join(x.parent.data, "multi-jobs")
	for _, id := range []string{req.Before, req.After} {
		job, e := readJournal(jobRoot, id, true)
		if e != nil || job.Case != req.Name || job.Status != "PASS" {
			fail(w, 409, errors.New("只能对比同项目的两个已成功发布任务"))
			return
		}
	}
	beforeDir, afterDir := filepath.Join(jobRoot, req.Before), filepath.Join(jobRoot, req.After)
	beforeInputs, e := readRunInputHashes(beforeDir)
	if e != nil {
		fail(w, 422, fmt.Errorf("旧任务输入快照: %w", e))
		return
	}
	afterInputs, e := readRunInputHashes(afterDir)
	if e != nil {
		fail(w, 422, fmt.Errorf("新任务输入快照: %w", e))
		return
	}
	beforeRules, oldPrecision, e := readRunRules(beforeDir)
	if e != nil {
		fail(w, 422, fmt.Errorf("旧任务规则: %w", e))
		return
	}
	afterRules, newPrecision, e := readRunRules(afterDir)
	if e != nil {
		fail(w, 422, fmt.Errorf("新任务规则: %w", e))
		return
	}
	if oldPrecision != newPrecision {
		fail(w, 422, errors.New("两个任务的金额精度不同，不能直接比较"))
		return
	}
	beforeResult, afterResult := filepath.Join(beforeDir, "results.csv"), filepath.Join(afterDir, "results.csv")
	// A single streaming SHA pass is cheaper than repeatedly partitioning two
	// potentially multi-gigabyte CSVs or storing duplicate difference reports.
	beforeHash, e := digestFile(r.Context(), beforeResult)
	if e != nil {
		fail(w, 422, e)
		return
	}
	afterHash, e := digestFile(r.Context(), afterResult)
	if e != nil {
		fail(w, 422, e)
		return
	}
	comparisonID, e := stableComparisonID(req.Name, req.Before, req.After, beforeHash, afterHash, beforeInputs, afterInputs, beforeRules, afterRules)
	if e != nil {
		fail(w, 500, e)
		return
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		fail(w, 500, e)
		return
	}
	if saved, found, er := readReusableRunComparison(r.Context(), root, comparisonID, req.Name, req.Before, req.After, beforeHash, afterHash, compareInputHashes(beforeInputs, afterInputs), compareRuleHashes(beforeRules, afterRules)); found {
		if er != nil {
			fail(w, 422, fmt.Errorf("历史对比记录不可复用: %w", er))
			return
		}
		jsonOut(w, 200, saved)
		return
	}
	tmp, e := os.MkdirTemp(root, ".compare-")
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer os.RemoveAll(tmp)
	// The shared comparison engine partitions both result files and preserves
	// exact integer amounts. It writes a private report, never business output.
	compared, e := acceptance.Compare(r.Context(), afterResult, beforeResult, filepath.Join(tmp, "coordinates"), acceptance.Options{Precision: oldPrecision, Tolerance: "0"})
	if e != nil {
		fail(w, 422, e)
		return
	}
	// Reject changes during or immediately following scan rather than creating
	// an apparently immutable report from a moving historical CSV.
	verifyBefore, e := digestFile(r.Context(), beforeResult)
	if e != nil {
		fail(w, 422, e)
		return
	}
	verifyAfter, e := digestFile(r.Context(), afterResult)
	if e != nil {
		fail(w, 422, e)
		return
	}
	if verifyBefore != beforeHash || verifyAfter != afterHash || beforeHash != compared.ReferenceSHA256 || afterHash != compared.ActualSHA256 {
		fail(w, 409, errors.New("历史结果在对比过程中发生变化，请重试"))
		return
	}

	changeFile := filepath.Join(tmp, "coordinates", "differences.csv")
	changesHash, e := digestFile(r.Context(), changeFile)
	if e != nil {
		fail(w, 500, e)
		return
	}
	record := runComparison{ID: comparisonID, Project: req.Name, BeforeJob: req.Before, AfterJob: req.After,
		InputChanges: compareInputHashes(beforeInputs, afterInputs), RuleChanges: compareRuleHashes(beforeRules, afterRules), CoordinateComparison: compared, DifferencesSHA256: changesHash,
		Scope: "仅比较两个成功任务的已保存输入文件 SHA-256、生效规则 SHA-256 和最终完整业务坐标；文件变化与结果变化均为事实，但不能据此推断某一项是唯一根因。多阶段流水不得跨阶段求和。"}
	metadata, e := json.MarshalIndent(record, "", "  ")
	if e != nil {
		fail(w, 500, e)
		return
	}
	if e = os.WriteFile(filepath.Join(tmp, "comparison.json"), append(metadata, '\n'), 0600); e != nil {
		fail(w, 500, e)
		return
	}
	// Keep only stable public compare outputs; do not leave an extra layer of
	// transient references or re-run the computation to create this evidence.
	if e = os.Rename(changeFile, filepath.Join(tmp, "differences.csv")); e != nil {
		fail(w, 500, e)
		return
	}
	if e = os.RemoveAll(filepath.Join(tmp, "coordinates")); e != nil {
		fail(w, 500, e)
		return
	}
	if e = os.Rename(tmp, filepath.Join(root, comparisonID)); e != nil {
		// Another comparison of the same immutable snapshots may have won the
		// atomic publication race. Never overwrite it; validate and reuse.
		if saved, found, er := readReusableRunComparison(r.Context(), root, comparisonID, req.Name, req.Before, req.After, beforeHash, afterHash, compareInputHashes(beforeInputs, afterInputs), compareRuleHashes(beforeRules, afterRules)); found {
			if er != nil {
				fail(w, 422, fmt.Errorf("并发发布记录不完整: %w", er))
				return
			}
			jsonOut(w, 200, saved)
			return
		}
		fail(w, 500, e)
		return
	}

	jsonOut(w, 201, record)
}
