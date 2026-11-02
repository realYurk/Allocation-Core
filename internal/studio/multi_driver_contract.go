package studio

// Schema-only Driver onboarding. A matching header does not establish real
// weights, period/source-pool eligibility or financial correctness.
import (
	"allocationcore/internal/engine"
	"encoding/csv"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type driverUse struct {
	Stage          string `json:"stage"`
	Allocation     string `json:"allocation"`
	Share          string `json:"share"`
	NegativePolicy string `json:"negative_policy"`
}

func contractKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func populateDriverContract(need *driverNeed, spec engine.Spec) {
	required, source := map[string]bool{}, map[string]bool{}
	need.Uses = []driverUse{}
	for _, stage := range spec.Stages {
		if !engine.Enabled(stage.Enabled) || (stage.Operation != "" && stage.Operation != "ALLOCATE") {
			continue
		}
		for _, a := range stage.Allocations {
			if !engine.Enabled(a.Enabled) || a.DriverDataset != need.Alias {
				continue
			}
			policy := a.NegativePolicy
			if policy == "" {
				policy = "REJECT"
			}
			need.Uses = append(need.Uses, driverUse{stage.ID, a.ID, a.Share, policy})
			for _, d := range spec.Dimensions {
				target := stage.Target[d]
				if target.Mode == "INCLUDE" || target.Mode == "INCLUDE_EXCLUDE" {
					required[d] = true
				}
				if target.Mode == "SOURCE" {
					source[d] = true
				}
			}
			for d := range a.DriverFilters {
				required[d] = true
			}
		}
	}
	need.RequiredDimensions = contractKeys(required)
	need.SourceDimensions = contractKeys(source)
	need.MissingColumns = []string{}
	need.BroadcastDimensions = []string{}
}

// Read at most a bounded header and one record solely to distinguish an empty
// template from a populated file. Do not return, log or retain any cell values.
func inspectDriverHeader(dir string, need *driverNeed) {
	if len(need.Uses) == 0 {
		need.Status = "UNUSED"
		need.NextStep = "当前启用步骤未引用该 Driver，无需为它编造数据。"
		return
	}
	need.Status = "MISSING_FILE"
	need.NextStep = "从原系统导出真实权重，按必需字段映射后导入；可先下载空表头模板。"
	folder := filepath.Join(dir, "drivers")
	info, e := os.Lstat(folder)
	if os.IsNotExist(e) {
		return
	}
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		need.Status = "INVALID_FILE"
		need.NextStep = "Driver 目录无法安全读取，请检查目录权限及文件类型；不要替换为猜测的权重。"
		return
	}
	file := filepath.Join(folder, need.CurrentFile)
	info, e = os.Lstat(file)
	if os.IsNotExist(e) {
		return
	}
	if e != nil || !info.Mode().IsRegular() {
		need.Status = "INVALID_FILE"
		return
	}
	f, e := os.Open(file)
	if e != nil {
		need.Status = "INVALID_FILE"
		return
	}
	defer f.Close()
	limit := &io.LimitedReader{R: f, N: 64 << 10}
	r := csv.NewReader(limit)
	r.FieldsPerRecord = -1
	head, e := r.Read()
	if e != nil || limit.N == 0 {
		need.Status = "INVALID_HEADER"
		need.NextStep = "CSV 应为 UTF-8，表头不得重复、为空或超过64KiB。"
		return
	}
	pos, e := engine.HeaderPositions(head)
	if e != nil {
		need.Status = "INVALID_HEADER"
		need.NextStep = "修复重复或空列名，再重新导入。"
		return
	}
	for _, d := range append(append([]string{}, need.RequiredDimensions...), need.WeightFields...) {
		if _, ok := pos[d]; !ok {
			need.MissingColumns = append(need.MissingColumns, d)
		}
	}
	for _, d := range need.SourceDimensions {
		if _, ok := pos[d]; !ok {
			need.BroadcastDimensions = append(need.BroadcastDimensions, d)
		}
	}
	if len(need.MissingColumns) > 0 {
		need.Status = "MISSING_COLUMNS"
		need.NextStep = "在字段映射中补齐必需目标维度、Driver过滤维度和权重字段；不能只映射任意一个维度。"
		return
	}
	// ReadDrivers requires at least one model dimension. SOURCE dimensions are
	// optional pool bindings; omission is a broadcast, never inferred as safe.
	if len(need.RequiredDimensions) == 0 && len(need.SourceDimensions) > 0 && len(need.BroadcastDimensions) == len(need.SourceDimensions) {
		need.Status = "DIMENSION_REVIEW"
		need.NextStep = "请映射至少一个业务维度，并确认 Driver 粒度；未映射来源维度可能跨来源池复用。"
		return
	}
	_, e = r.Read()
	if e == io.EOF {
		need.Status = "HEADER_ONLY"
		need.NextStep = "当前只有表头，还没有真实权重；请从原系统导出并导入 Driver 明细。"
		return
	}
	if e != nil || limit.N == 0 {
		need.Status = "DATA_REVIEW"
		need.NextStep = "首条记录无法完成结构检查；请执行完整体检查看数据问题。"
		return
	}
	need.Status = "SCHEMA_READY"
	need.NextStep = "字段已准备；权重按六位定点检查，不自动舍入。继续完整体检、实际分摊和独立参考对账。"
	if len(need.BroadcastDimensions) > 0 {
		need.Status = "SCOPE_REVIEW"
		need.NextStep = "部分来源维度未提供：Core 会跨这些来源坐标复用权重。请确认期间、科目等粒度是否确实通用，再做完整体检。"
	}
}

func writeDriverHeaderTemplate(w http.ResponseWriter, r *http.Request, version string, needs []driverNeed) {
	if r.URL.Query().Get("base_sha256") != version {
		fail(w, 409, errors.New("规则已变化，请刷新 Driver 清单后重新下载模板"))
		return
	}
	var selected *driverNeed
	for i := range needs {
		if needs[i].Alias == r.URL.Query().Get("alias") {
			selected = &needs[i]
			break
		}
	}
	if selected == nil || len(selected.Uses) == 0 {
		fail(w, 422, errors.New("请选择启用步骤实际使用的 Driver"))
		return
	}
	columns := []string{}
	seen := map[string]bool{}
	// Include all source bindings conservatively; the operator must explicitly
	// decide whether a global/broadcast Driver is intended before omitting them.
	for _, group := range [][]string{selected.SourceDimensions, selected.RequiredDimensions, selected.WeightFields} {
		for _, col := range group {
			if !seen[col] {
				seen[col] = true
				columns = append(columns, col)
			}
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	name := strings.TrimSuffix(selected.CurrentFile, ".csv") + "-header-only.csv"
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
	writer := csv.NewWriter(w)
	_ = writer.Write(columns)
	writer.Flush()
}
