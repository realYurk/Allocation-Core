// Local Review is a generic, offline, read-only allocation reconciliation tool.
// Coarse placeholders mean a legitimate posting grain, never a missing model.
// It is not an allocation engine and does not infer proprietary fallback rules.
package review

import (
	"bufio"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ValueColumn          string              `json:"value_column"`
	AccountColumn        string              `json:"account_column"`
	SourceColumn         string              `json:"source_column"`
	TargetSource         string              `json:"target_source"`
	ScenarioColumns      []string            `json:"scenario_columns"`
	CoarseMarkers        map[string][]string `json:"coarse_markers"`
	DriverAccounts       []string            `json:"driver_accounts"`
	VerifiedRuleAccounts []string            `json:"independently_verified_rule_accounts"`
	Notes                map[string]string   `json:"account_notes"`
	Tolerance            string              `json:"amount_tolerance"`
}
type Summary struct {
	Status         string   `json:"status"`
	Scope          string   `json:"scope"`
	BeforeRows     int      `json:"before_rows"`
	AfterRows      int      `json:"after_rows"`
	CoreRows       int      `json:"core_rows,omitempty"`
	Accounts       int      `json:"accounts"`
	TotalEqual     int      `json:"total_equal"`
	TotalUnequal   int      `json:"total_unequal"`
	Drivers        int      `json:"drivers"`
	ExactMatched   int      `json:"exact_matched"`
	ExactDifferent int      `json:"exact_different"`
	BeforeSHA      string   `json:"before_sha256"`
	AfterSHA       string   `json:"after_sha256"`
	CoreSHA        string   `json:"core_sha256,omitempty"`
	CheckSeconds   float64  `json:"check_seconds"`
	CoreSeconds    *float64 `json:"core_seconds,omitempty"`
	LegacySeconds  *float64 `json:"legacy_seconds,omitempty"`
	Warning        []string `json:"warnings"`
}
type Ledger struct{ Account, Before, After, Difference, State, Grain, Note string }
type Diff struct{ Account, Coordinates, Legacy, New, Difference, State string }
type readResult struct {
	Rows      int
	Headers   []string
	Sums      map[string]*big.Rat
	ByAccount map[string]*big.Rat
	Coarse    map[string]int
	SHA       string
	Scope     string
}

func parseDecimal(s string) (*big.Rat, error) {
	s = strings.TrimSpace(s)
	r := new(big.Rat)
	if _, ok := r.SetString(s); !ok {
		return nil, fmt.Errorf("invalid decimal %q", s)
	}
	return r, nil
}
func add(m map[string]*big.Rat, k string, v *big.Rat) {
	if m[k] == nil {
		m[k] = new(big.Rat)
	}
	m[k].Add(m[k], v)
}
func val(m map[string]*big.Rat, k string) *big.Rat {
	if r := m[k]; r != nil {
		return r
	}
	return new(big.Rat)
}
func diff(a, b *big.Rat) *big.Rat { return new(big.Rat).Sub(a, b) }
func abs(a *big.Rat) *big.Rat     { return new(big.Rat).Abs(a) }
func money(a *big.Rat) string     { return a.FloatString(2) }
func strSet(ss []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range ss {
		m[x] = true
	}
	return m
}
func colKey(h []string, cfg Config) []string {
	r := []string{}
	for _, s := range h {
		if s != cfg.ValueColumn && s != cfg.SourceColumn {
			r = append(r, s)
		}
	}
	sort.Strings(r)
	return r
}
func readTable(path string, cfg Config, target bool, baseline []string) (readResult, error) {
	r := readResult{Sums: map[string]*big.Rat{}, ByAccount: map[string]*big.Rat{}, Coarse: map[string]int{}}
	f, e := os.Open(path)
	if e != nil {
		return r, e
	}
	defer f.Close()
	h := sha256.New()
	buf := bufio.NewReader(io.TeeReader(f, h))
	if bom, err := buf.Peek(3); err == nil && string(bom) == "\xef\xbb\xbf" {
		_, _ = buf.Discard(3)
	}
	reader := csv.NewReader(buf)
	reader.FieldsPerRecord = -1
	head, e := reader.Read()
	if e != nil {
		return r, e
	}
	if len(head) > 0 {
		head[0] = strings.TrimPrefix(head[0], "\ufeff")
	}
	idx := map[string]int{}
	for i, k := range head {
		if _, exists := idx[k]; exists {
			return r, fmt.Errorf("duplicate header %q", k)
		}
		idx[k] = i
	}
	required := append([]string{cfg.AccountColumn, cfg.ValueColumn}, cfg.ScenarioColumns...)
	if !target {
		required = append(required, cfg.SourceColumn)
	}
	for _, k := range required {
		if _, ok := idx[k]; !ok {
			return r, fmt.Errorf("%s missing column %q", filepath.Base(path), k)
		}
	}
	cols := colKey(head, cfg)
	if baseline != nil && strings.Join(cols, "\x00") != strings.Join(baseline, "\x00") {
		return r, fmt.Errorf("%s has inconsistent coordinate columns", filepath.Base(path))
	}
	r.Headers = cols
	seenScope := map[string]bool{}
	for n := 2; ; n++ {
		fields, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return r, fmt.Errorf("%s row %d: %w", path, n, err)
		}
		if target {
			if ix, ok := idx[cfg.SourceColumn]; ok && ix < len(fields) && strings.TrimSpace(fields[ix]) != cfg.TargetSource {
				continue
			}
		}
		get := func(k string) string {
			i, ok := idx[k]
			if !ok || i >= len(fields) {
				return ""
			}
			return strings.TrimSpace(fields[i])
		}
		if get(cfg.ValueColumn) == "" {
			return r, fmt.Errorf("%s row %d has empty amount", path, n)
		}
		v, e := parseDecimal(get(cfg.ValueColumn))
		if e != nil {
			return r, fmt.Errorf("%s row %d: %w", path, n, e)
		}
		account := get(cfg.AccountColumn)
		if account == "" {
			return r, fmt.Errorf("%s row %d empty account", path, n)
		}
		scope := make([]string, len(cfg.ScenarioColumns))
		for i, k := range cfg.ScenarioColumns {
			scope[i] = get(k)
		}
		seenScope[strings.Join(scope, " / ")] = true
		parts := make([]string, len(cols))
		for i, k := range cols {
			parts[i] = get(k)
		}
		key := strings.Join(parts, "\x1f")
		add(r.Sums, key, v)
		add(r.ByAccount, account, v)
		r.Rows++
		coarse := []string{}
		for field, values := range cfg.CoarseMarkers {
			x := get(field)
			for _, mark := range values {
				if x == mark {
					coarse = append(coarse, field)
					break
				}
			}
		}
		if len(coarse) > 0 {
			sort.Strings(coarse)
			r.Coarse[account]++
		}
	}
	if len(seenScope) != 1 {
		return r, fmt.Errorf("%s must have exactly one scenario/version/year; got %d", path, len(seenScope))
	}
	for s := range seenScope {
		r.Scope = s
	}
	r.SHA = hex.EncodeToString(h.Sum(nil))
	return r, nil
}
func safeWrite(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	temp := path + ".tmp"
	if e := os.WriteFile(temp, b, 0600); e != nil {
		return e
	}
	return os.Rename(temp, path)
}
func writeCSV(path string, head []string, rows [][]string) error {
	f, e := os.Create(path + ".tmp")
	if e != nil {
		return e
	}
	w := csv.NewWriter(f)
	_ = w.Write(head)
	for _, r := range rows {
		if e = w.Write(r); e != nil {
			_ = f.Close()
			return e
		}
	}
	w.Flush()
	e = errors.Join(w.Error(), f.Close())
	if e != nil {
		return e
	}
	return os.Rename(path+".tmp", path)
}
func index(cfg Config, report Summary, ledgers []Ledger, issues []Diff) string {
	esc := html.EscapeString
	badge := func(s string) string {
		switch s {
		case "TOTAL_MATCH":
			return "<span class='ok'>金额一致</span>"
		case "DRIVER_NO_TOTAL_ASSERTION":
			return "<span class='muted'>驱动指标</span>"
		default:
			return "<span class='warn'>需查规则</span>"
		}
	}
	b := strings.Builder{}
	b.WriteString(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>分摊核验｜业务首页</title><style>body{font-family:'Segoe UI','Microsoft YaHei',sans-serif;background:#f4f7fc;color:#18283e;margin:0;padding:35px;line-height:1.6}main{max-width:1150px;margin:auto}h1{font-size:26px}h2{font-size:18px}section{background:#fff;padding:22px;border:1px solid #e4eaf5;border-radius:14px;margin:18px 0}table{border-collapse:collapse;width:100%;font-size:13px}td,th{padding:11px;border-bottom:1px solid #edf1f8;text-align:left}th{color:#607086}small{color:#596e85}.stats{display:flex;gap:14px;flex-wrap:wrap}.stat{background:#edf4ff;border-radius:10px;padding:14px;min-width:150px}.stat strong{display:block;font-size:23px;color:#1265bd}.ok{color:#087651;font-weight:600}.warn{color:#b35c0b;font-weight:600}.muted{color:#62748a}.alert{border-left:4px solid #eb961d;padding:12px;background:#fff9ec}code{background:#edf4ff;padding:2px 5px}</style><main>`)
	b.WriteString("<h1>分摊核验 · 业务首页</h1><p>场景：<strong>" + esc(report.Scope) + "</strong></p>")
	b.WriteString("<section><div class='alert'><b>结论边界：</b>分摊前 Source 忽略后合并；分摊后只取指定结果 Source。<b>No Model / No Line 表示合法的记账粒度，不是数据丢失。</b>总额不等只提示规则或目标范围待核查，不自动认定引擎错误。已做独立规则复算与否，需要另行查证。</div></section>")
	b.WriteString("<section><h2>一眼看懂</h2><div class='stats'>")
	for _, x := range [][2]string{{"分摊前记录", strconv.Itoa(report.BeforeRows)}, {"分摊后记录", strconv.Itoa(report.AfterRows)}, {"总额相等科目", strconv.Itoa(report.TotalEqual)}, {"总额待解释科目", strconv.Itoa(report.TotalUnequal)}, {"驱动科目", strconv.Itoa(report.Drivers)}} {
		b.WriteString("<div class='stat'><small>" + x[0] + "</small><strong>" + esc(x[1]) + "</strong></div>")
	}
	b.WriteString("</div></section>")
	b.WriteString("<section><h2>新引擎与旧系统对账</h2>")
	if report.CoreSHA == "" {
		b.WriteString("<p class='warn'>未提供新引擎结果：尚不能宣称新引擎与旧系统 100% 一致。</p>")
	} else {
		b.WriteString(fmt.Sprintf("<p>一致业务坐标：<b>%d</b>，差异或缺失坐标：<b>%d</b>。请看 <code>engine_differences.csv</code>。</p>", report.ExactMatched, report.ExactDifferent))
	}
	b.WriteString("</section><section><h2>分摊前后金额（仅作总量诊断）</h2><table><thead><tr><th>科目</th><th>分摊前</th><th>分摊后</th><th>差额（后－前）</th><th>结论</th><th>原始粒度/说明</th></tr></thead><tbody>")
	for _, r := range ledgers {
		b.WriteString("<tr><td>" + esc(r.Account) + "</td><td>" + esc(r.Before) + "</td><td>" + esc(r.After) + "</td><td>" + esc(r.Difference) + "</td><td>" + badge(r.State) + "</td><td>" + esc(r.Grain+" "+r.Note) + "</td></tr>")
	}
	b.WriteString("</tbody></table></section>")
	b.WriteString("<section><h2>耗时（不可混用）</h2><p>检查器：" + fmt.Sprintf("%.3f", report.CheckSeconds) + " 秒；旧系统实际计算：")
	if report.LegacySeconds == nil {
		b.WriteString("未提供")
	} else {
		b.WriteString(fmt.Sprintf("%.3f 秒", *report.LegacySeconds))
	}
	b.WriteString("；新引擎实际计算：")
	if report.CoreSeconds == nil {
		b.WriteString("未提供")
	} else {
		b.WriteString(fmt.Sprintf("%.3f 秒", *report.CoreSeconds))
	}
	b.WriteString("。</p></section>")
	if len(issues) > 0 {
		b.WriteString("<section><h2>差异示例（最多展示前 20 条）</h2><table><tr><th>科目</th><th>坐标</th><th>旧系统</th><th>新引擎</th><th>差额</th></tr>")
		for i, r := range issues {
			if i >= 20 {
				break
			}
			b.WriteString("<tr><td>" + esc(r.Account) + "</td><td>" + esc(r.Coordinates) + "</td><td>" + esc(r.Legacy) + "</td><td>" + esc(r.New) + "</td><td>" + esc(r.Difference) + "</td></tr>")
		}
		b.WriteString("</table></section>")
	}
	b.WriteString("<section><small>仅在你的电脑上生成，不联网、不上传数据。数据文件不要提交至 GitHub。</small></section></main></html>")
	return b.String()
}
func execute(cfg Config, before, after, core, out string, coreSeconds, legacySeconds float64) (Summary, error) {
	start := time.Now()
	r := Summary{Warning: []string{"coarse placeholders are posting grain, not evidence of missing drivers", "amount total differences require rule-source scope and fallback logic review"}}
	if cfg.ValueColumn == "" || cfg.AccountColumn == "" || cfg.SourceColumn == "" || cfg.TargetSource == "" {
		return r, fmt.Errorf("config requires value/account/source/target_source")
	}
	tol, e := parseDecimal(cfg.Tolerance)
	if e != nil {
		return r, e
	}
	b, e := readTable(before, cfg, false, nil)
	if e != nil {
		return r, e
	}
	a, e := readTable(after, cfg, true, b.Headers)
	if e != nil {
		return r, e
	}
	if b.Scope != a.Scope {
		return r, fmt.Errorf("scope mismatch: before=%s after=%s", b.Scope, a.Scope)
	}
	r.Scope = b.Scope
	r.BeforeRows = b.Rows
	r.AfterRows = a.Rows
	r.BeforeSHA = b.SHA
	r.AfterSHA = a.SHA
	if coreSeconds >= 0 {
		r.CoreSeconds = &coreSeconds
	}
	if legacySeconds >= 0 {
		r.LegacySeconds = &legacySeconds
	}
	driver := strSet(cfg.DriverAccounts)
	verified := strSet(cfg.VerifiedRuleAccounts)
	accountSet := map[string]bool{}
	for k := range b.ByAccount {
		accountSet[k] = true
	}
	for k := range a.ByAccount {
		accountSet[k] = true
	}
	keys := []string{}
	for k := range accountSet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ledgers := []Ledger{}
	for _, k := range keys {
		bv, av := val(b.ByAccount, k), val(a.ByAccount, k)
		d := diff(av, bv)
		state := "TOTAL_MATCH"
		if driver[k] {
			state = "DRIVER_NO_TOTAL_ASSERTION"
			r.Drivers++
		} else if abs(d).Cmp(tol) > 0 {
			state = "RULE_SCOPE_REVIEW"
			r.TotalUnequal++
		} else {
			r.TotalEqual++
		}
		grain := ""
		if b.Coarse[k] > 0 {
			grain = fmt.Sprintf("%d 条原始记录停留在上层记账粒度（正常）", b.Coarse[k])
		}
		note := cfg.Notes[k]
		if verified[k] {
			note = "历史独立阶段复算有证据；" + note
		}
		if state == "RULE_SCOPE_REVIEW" {
			note = "差额非自动判错；需核对跨维度转移、规则筛选、兜底均摊或中间结果。" + note
		}
		ledgers = append(ledgers, Ledger{k, money(bv), money(av), money(d), state, grain, note})
	}
	r.Accounts = len(keys)
	issues := []Diff{}
	if core != "" {
		c, e := readTable(core, cfg, true, a.Headers)
		if e != nil {
			return r, e
		}
		if c.Scope != a.Scope {
			return r, fmt.Errorf("engine scope mismatch")
		}
		r.CoreRows = c.Rows
		r.CoreSHA = c.SHA
		ix := map[string]int{}
		for i, k := range a.Headers {
			ix[k] = i
		}
		all := map[string]bool{}
		for k := range a.Sums {
			all[k] = true
		}
		for k := range c.Sums {
			all[k] = true
		}
		keys2 := []string{}
		for k := range all {
			keys2 = append(keys2, k)
		}
		sort.Strings(keys2)
		for _, k := range keys2 {
			d := diff(val(c.Sums, k), val(a.Sums, k))
			if abs(d).Cmp(tol) <= 0 {
				r.ExactMatched++
				continue
			}
			r.ExactDifferent++
			p := strings.Split(k, "\x1f")
			account := ""
			if i, ok := ix[cfg.AccountColumn]; ok && i < len(p) {
				account = p[i]
			}
			coords := []string{}
			for i, f := range a.Headers {
				if i < len(p) {
					coords = append(coords, f+"="+p[i])
				}
			}
			issues = append(issues, Diff{account, strings.Join(coords, " | "), money(val(a.Sums, k)), money(val(c.Sums, k)), money(d), "MISMATCH"})
		}
	}
	r.CheckSeconds = time.Since(start).Seconds()
	r.Status = "TOTAL_DIAGNOSTIC_ONLY"
	if core != "" {
		r.Status = "ENGINE_MATCH"
		if r.ExactDifferent > 0 {
			r.Status = "ENGINE_DIFFERENCES"
		}
	}
	if e := os.MkdirAll(out, 0700); e != nil {
		return r, e
	}
	rows := [][]string{}
	for _, x := range ledgers {
		rows = append(rows, []string{x.Account, x.Before, x.After, x.Difference, x.State, x.Grain, x.Note})
	}
	if e = writeCSV(filepath.Join(out, "account_review.csv"), []string{"account", "before", "after", "difference_after_minus_before", "status", "posting_grain", "notes"}, rows); e != nil {
		return r, e
	}
	if core != "" {
		rows = [][]string{}
		for _, x := range issues {
			rows = append(rows, []string{x.Account, x.Coordinates, x.Legacy, x.New, x.Difference, x.State})
		}
		if e = writeCSV(filepath.Join(out, "engine_differences.csv"), []string{"account", "coordinate", "legacy_amount", "engine_amount", "difference", "status"}, rows); e != nil {
			return r, e
		}
	}
	raw, _ := json.MarshalIndent(r, "", "  ")
	if e = safeWrite(filepath.Join(out, "summary.json"), raw); e != nil {
		return r, e
	}
	if e = safeWrite(filepath.Join(out, "index.html"), []byte(index(cfg, r, ledgers, issues))); e != nil {
		return r, e
	}
	return r, nil
}
func CLI() {
	var cfgPath, before, after, core, out string
	var cs, ls float64
	flag.StringVar(&cfgPath, "config", "local_config.json", "private field mappings")
	flag.StringVar(&before, "before", "input/before.csv", "pre-allocation file")
	flag.StringVar(&after, "after", "input/after.csv", "legacy system post-allocation file")
	flag.StringVar(&core, "engine-after", "", "optional NEW engine 14-column mapped result")
	flag.StringVar(&out, "out", "output", "local private output folder")
	flag.Float64Var(&cs, "engine-seconds", -1, "actual measured engine compute seconds; negative means unavailable")
	flag.Float64Var(&ls, "legacy-seconds", -1, "actual measured legacy compute seconds; negative means unavailable")
	flag.Parse()
	raw, e := os.ReadFile(cfgPath)
	if e != nil {
		fmt.Fprintln(os.Stderr, "CONFIG:", e)
		os.Exit(2)
	}
	cfg := Config{}
	if e = json.Unmarshal(raw, &cfg); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	report, e := execute(cfg, before, after, core, out, cs, ls)
	if e != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", e)
		os.Exit(2)
	}
	fmt.Printf("status=%s scope=%s accounts=%d totals_equal=%d totals_to_review=%d driver_accounts=%d engine_diff_coordinates=%d duration=%.3fs\n", report.Status, report.Scope, report.Accounts, report.TotalEqual, report.TotalUnequal, report.Drivers, report.ExactDifferent, report.CheckSeconds)
	if report.ExactDifferent > 0 {
		os.Exit(1)
	}
}
