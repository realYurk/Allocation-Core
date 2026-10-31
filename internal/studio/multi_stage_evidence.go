package studio

// Historical per-account stage/factor evidence is read exclusively from the
// immutable, successfully published Multi job. It never re-runs the engine or
// consults the current project's mutable rules and Driver CSV files.
import (
	"allocationcore/internal/engine"
	"allocationcore/internal/multi"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type stageEvidenceLine struct {
	Stage           string              `json:"stage"`
	Allocation      string              `json:"allocation"`
	DriverDataset   string              `json:"driver_dataset,omitempty"`
	WeightField     string              `json:"weight_field,omitempty"`
	OutputRows      int64               `json:"output_rows"`
	AllocatedAmount string              `json:"allocated_amount"`
	SamplePools     []map[string]string `json:"sample_pools"`
	// A zero count means "no ledger records for this filter", NOT an error.
}

type stageEvidenceReport struct {
	JobID            string              `json:"job_id"`
	Account          string              `json:"account"`
	Period           string              `json:"period,omitempty"`
	GroupID          string              `json:"group_id"`
	Template         string              `json:"template"`
	RuleSHA256       string              `json:"rule_sha256"`
	LedgerSHA256     string              `json:"ledger_sha256"`
	ManifestVerified bool                `json:"manifest_verified"`
	TotalRows        int64               `json:"total_ledger_rows"`
	Lines            []stageEvidenceLine `json:"lines"`
	Scope            string              `json:"scope"`
}

// readSmallRegular keeps every untrusted pathname a fixed, server-computed path
// and rejects links, devices and unexpectedly large metadata.
func readSmallRegular(path string, limit int64) ([]byte, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("invalid evidence file type or size")
	}
	return os.ReadFile(path)
}
func validGroupID(s string) bool {
	if len(s) != 20 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
func formatEvidenceTotal(n *big.Int, precision int) string {
	base := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
	a := new(big.Int).Abs(n)
	whole, part := new(big.Int).QuoRem(a, base, new(big.Int))
	s := whole.String()
	if precision > 0 {
		s += "." + fmt.Sprintf("%0*s", precision, part.String())
	}
	if n.Sign() < 0 {
		s = "-" + s
	}
	return s
}

func (x *MultiApp) stageEvidence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	name, id, account := r.URL.Query().Get("name"), r.URL.Query().Get("job"), r.URL.Query().Get("account")
	period := r.URL.Query().Get("period")
	if !multiSafe.MatchString(name) || !safeJobID(id) || account == "" || len(account) > 256 || len(period) > 128 {
		fail(w, 400, errors.New("请选择有效的项目、成功任务和科目"))
		return
	}
	jobRoot := filepath.Join(x.parent.data, "multi-jobs")
	job, err := readJournal(jobRoot, id, true)
	if err != nil || job.Case != name || job.Status != "PASS" {
		fail(w, 404, errors.New("找不到该项目已成功发布的历史任务"))
		return
	}
	dir := filepath.Join(jobRoot, id)
	raw, err := readSmallRegular(filepath.Join(dir, "run_summary.json"), 32<<20)
	if err != nil {
		fail(w, 422, err)
		return
	}
	var result multi.Result
	if err = json.Unmarshal(raw, &result); err != nil || result.Status != "PASS" {
		fail(w, 422, errors.New("历史运行摘要无效或不是成功任务"))
		return
	}
	var selected *multi.Group
	for i := range result.Engines {
		g := &result.Engines[i]
		for _, a := range g.Accounts {
			if a == account {
				if selected != nil {
					fail(w, 422, errors.New("科目在历史结果中属于多个规则组"))
					return
				}
				selected = g
			}
		}
	}
	if selected == nil || selected.Status != "PASS" || !validGroupID(selected.ID) {
		fail(w, 404, errors.New("该科目没有可核验的历史规则组"))
		return
	}
	groupDir := filepath.Join(dir, "groups", selected.ID)
	ruleRaw, err := readSmallRegular(filepath.Join(groupDir, "rule.json"), 4<<20)
	if err != nil {
		fail(w, 422, err)
		return
	}
	var spec engine.Spec
	if err = json.Unmarshal(ruleRaw, &spec); err != nil || spec.Precision < 0 || spec.Precision > 6 {
		fail(w, 422, errors.New("历史生效规则无效"))
		return
	}
	accountIndex, periodIndex := -1, -1
	for i, d := range spec.Dimensions {
		if d == "account" {
			accountIndex = i
		}
		if d == "period" {
			periodIndex = i
		}
	}
	if accountIndex < 0 || (period != "" && periodIndex < 0) {
		fail(w, 422, errors.New("历史规则缺少所需业务维度"))
		return
	}
	groupSummary, err := readSmallRegular(filepath.Join(groupDir, "output", "run_summary.json"), 4<<20)
	if err != nil {
		fail(w, 422, err)
		return
	}
	var summary struct {
		Status     string `json:"status"`
		RuleSHA256 string `json:"rule_sha256"`
	}
	if err = json.Unmarshal(groupSummary, &summary); err != nil || summary.Status != "PASS" {
		fail(w, 422, errors.New("规则组尚无已发布的 Core 结果"))
		return
	}
	ruleHash := sha256.Sum256(ruleRaw)
	if summary.RuleSHA256 != hex.EncodeToString(ruleHash[:]) {
		fail(w, 422, errors.New("历史规则指纹与 Core 结果不一致"))
		return
	}
	report, err := summarizeStageLedger(r, groupDir, spec, account, period)
	if err != nil {
		fail(w, 422, err)
		return
	}
	report.JobID = id
	report.Account = account
	report.Period = period
	report.GroupID = selected.ID
	report.Template = selected.Template
	report.RuleSHA256 = summary.RuleSHA256
	report.Scope = "仅根据本次成功任务保存的 Core ledger 汇总各阶段/因子的已分摊金额。不同阶段可能重复流转同一金额，切勿跨阶段相加；不证明与参考系统一致。"
	if r.URL.Query().Get("download") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=stage_evidence.csv")
		wr := csv.NewWriter(w)
		_ = wr.Write([]string{"job_id", "account", "period", "stage", "allocation", "driver_dataset", "weight_field", "ledger_rows", "allocated_amount", "ledger_sha256", "manifest_verified"})
		for _, v := range report.Lines {
			_ = wr.Write([]string{id, account, period, v.Stage, v.Allocation, v.DriverDataset, v.WeightField, strconv.FormatInt(v.OutputRows, 10), v.AllocatedAmount, report.LedgerSHA256, strconv.FormatBool(report.ManifestVerified)})
		}
		wr.Flush()
		return
	}
	jsonOut(w, 200, report)
}

func summarizeStageLedger(r *http.Request, dir string, spec engine.Spec, account, period string) (stageEvidenceReport, error) {
	report := stageEvidenceReport{Lines: []stageEvidenceLine{}}
	accountIx, periodIx := -1, -1
	for i, d := range spec.Dimensions {
		if d == "account" {
			accountIx = i
		}
		if d == "period" {
			periodIx = i
		}
	}
	if accountIx < 0 {
		return report, errors.New("missing account dimension")
	}
	byKey := map[string]int{}
	totals := []*big.Int{}
	for _, stage := range spec.Stages {
		for _, a := range stage.Allocations {
			k := stage.ID + "\x00" + a.ID
			if _, exists := byKey[k]; exists {
				return report, errors.New("duplicate stage/allocation in saved rule")
			}
			if len(byKey) >= 4096 {
				return report, errors.New("too many stage allocation rows")
			}
			byKey[k] = len(report.Lines)
			report.Lines = append(report.Lines, stageEvidenceLine{Stage: stage.ID, Allocation: a.ID, DriverDataset: a.DriverDataset, WeightField: a.WeightField, AllocatedAmount: "0", SamplePools: []map[string]string{}})
			totals = append(totals, new(big.Int))
		}
	}
	outputDir := filepath.Join(dir, "output")
	storedName := "ledger.csv"
	if st, e := os.Lstat(filepath.Join(outputDir, "ledger.csv.gz")); e == nil {
		if !st.Mode().IsRegular() {
			return report, errors.New("invalid compressed ledger file")
		}
		storedName = "ledger.csv.gz"
	}
	path := filepath.Join(outputDir, storedName)
	st, err := os.Lstat(path)
	if err != nil {
		return report, err
	}
	if !st.Mode().IsRegular() {
		return report, errors.New("invalid ledger file type")
	}
	f, err := os.Open(path)
	if err != nil {
		return report, err
	}
	defer f.Close()
	hash := sha256.New()
	var n int64
	buffer := make([]byte, 256<<10)
	for {
		if err := r.Context().Err(); err != nil {
			return report, err
		}
		read, readErr := f.Read(buffer)
		if read > 0 {
			_, _ = hash.Write(buffer[:read])
			n += int64(read)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return report, readErr
		}
	}
	report.LedgerSHA256 = hex.EncodeToString(hash.Sum(nil))
	if manifestRaw, e := readSmallRegular(filepath.Join(outputDir, "evidence_stream_manifest.json"), 1<<20); e == nil {
		var manifest struct {
			Files map[string]struct {
				StoredName   string `json:"stored_name"`
				StoredSHA256 string `json:"stored_sha256"`
				StoredBytes  int64  `json:"stored_bytes"`
			} `json:"files"`
		}
		if json.Unmarshal(manifestRaw, &manifest) != nil {
			return report, errors.New("invalid evidence manifest")
		}
		record, ok := manifest.Files["ledger.csv"]
		if !ok || record.StoredName != storedName || record.StoredSHA256 != report.LedgerSHA256 || record.StoredBytes != n {
			return report, errors.New("ledger 文件与原始证据清单不一致，请核对归档完整性")
		}
		report.ManifestVerified = true
	} else if !os.IsNotExist(e) {
		return report, e
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return report, err
	}
	var reader io.Reader = f
	if storedName == "ledger.csv.gz" {
		gz, e := gzip.NewReader(f)
		if e != nil {
			return report, e
		}
		defer gz.Close()
		reader = gz
	}
	cr := csv.NewReader(bufio.NewReaderSize(reader, 256<<10))
	headers, err := cr.Read()
	if err != nil {
		return report, err
	}
	cols, err := engine.HeaderPositions(headers)
	if err != nil {
		return report, err
	}
	expected := []string{"stage", "rule", "pool", "target", "target_row_id", "pool_amount", "share_amount", "driver_weight", "driver_denominator", "allocated_amount"}
	if len(cols) != len(expected) {
		return report, errors.New("unexpected ledger schema")
	}
	for _, c := range expected {
		if _, ok := cols[c]; !ok {
			return report, fmt.Errorf("ledger missing %s", c)
		}
	}
	for {
		if err = r.Context().Err(); err != nil {
			return report, err
		}
		row, e := cr.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return report, fmt.Errorf("invalid ledger CSV: %w", e)
		}
		ix, ok := byKey[row[cols["stage"]]+"\x00"+row[cols["rule"]]]
		if !ok {
			return report, errors.New("ledger references unknown stage/allocation")
		}
		var members []string
		if json.Unmarshal([]byte(row[cols["pool"]]), &members) != nil || len(members) != len(spec.Dimensions) {
			return report, errors.New("ledger contains invalid pool coordinates")
		}
		if members[accountIx] != account {
			continue
		}
		if period != "" && members[periodIx] != period {
			continue
		}
		amount, e := engine.ParseScaled(row[cols["allocated_amount"]], spec.Precision)
		if e != nil {
			return report, fmt.Errorf("invalid ledger amount: %w", e)
		}
		line := &report.Lines[ix]
		if line.OutputRows == int64(^uint64(0)>>1) {
			return report, errors.New("ledger row count overflow")
		}
		line.OutputRows++
		report.TotalRows++
		totals[ix].Add(totals[ix], big.NewInt(amount))
		if len(line.SamplePools) < 3 {
			sample := make(map[string]string, len(members))
			for i, d := range spec.Dimensions {
				sample[d] = members[i]
			}
			duplicated := false
			for _, prior := range line.SamplePools {
				if maps.Equal(prior, sample) {
					duplicated = true
					break
				}
			}
			if !duplicated {
				line.SamplePools = append(line.SamplePools, sample)
			}
		}
	}
	for i := range report.Lines {
		report.Lines[i].AllocatedAmount = formatEvidenceTotal(totals[i], spec.Precision)
	}
	return report, nil
}
