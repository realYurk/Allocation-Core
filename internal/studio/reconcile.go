package studio

import (
	"allocationcore/internal/acceptance"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// reconcile compares a completed, immutable job result with a private file in
// the associated local project folder. It never modifies published amounts.
func (x *MultiApp) reconcile(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name      string `json:"name"`
		Job       string `json:"job"`
		Precision int    `json:"precision"`
		Tolerance string `json:"tolerance"`
	}
	if e := multiRequest(w, r, &req, 2048); e != nil {
		fail(w, 400, e)
		return
	}
	if !multiSafe.MatchString(req.Name) || len(req.Job) != 24 || strings.Trim(req.Job, "0123456789abcdef") != "" {
		fail(w, 422, errors.New("invalid project or job identifier"))
		return
	}
	dir, e := x.projectDir(req.Name)
	if e != nil {
		fail(w, 404, e)
		return
	}
	// Only the single fixed reference filename may be used (no arbitrary path from HTTP).
	ref := filepath.Join(dir, "reference.csv")
	st, e := os.Lstat(ref)
	if e != nil {
		fail(w, 422, fmt.Errorf("请将参考结果保存为项目目录里的 reference.csv: %w", e))
		return
	}
	if !st.Mode().IsRegular() {
		fail(w, 422, errors.New("reference.csv must be a regular file, not a link"))
		return
	}
	x.mu.Lock()
	job, ok := x.jobs[req.Job]
	var cp Job
	if ok {
		cp = *job
	}
	x.mu.Unlock()
	if !ok {
		cp, e = readJournal(filepath.Join(x.parent.data, "multi-jobs"), req.Job, true)
		if e != nil {
			fail(w, 404, errors.New("job not found"))
			return
		}
	}
	if cp.Status != "PASS" || cp.Case != req.Name {
		fail(w, 409, errors.New("只能对同项目、已通过计算的任务进行参考对账"))
		return
	}
	actual := filepath.Join(x.parent.data, "multi-jobs", req.Job, "results.csv")
	st, e = os.Lstat(actual)
	if e != nil || !st.Mode().IsRegular() {
		fail(w, 409, errors.New("已发布结果不存在或不是普通文件"))
		return
	}
	// A fresh report directory prevents overwriting historical evidence.
	out := filepath.Join(x.parent.data, "multi-jobs", req.Job, "acceptance-"+time.Now().UTC().Format("20060102T150405.000000000"))
	report, e := acceptance.Compare(r.Context(), actual, ref, out, acceptance.Options{Precision: req.Precision, Tolerance: req.Tolerance})
	if e != nil {
		fail(w, 422, e)
		return
	}
	jsonOut(w, 200, map[string]any{"report": report, "report_directory": out, "note": "MATCH proves coordinate-level agreement only for these two snapshots and the configured precision/tolerance"})
}
