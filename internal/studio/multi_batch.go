package studio

// Studio never manipulates vault records directly: it invokes the same local
// Multi batch CLI used by operators, and returns the verified JSON response.
import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var batchUIName = regexp.MustCompile(`^[A-Za-z0-9_.+-]{1,100}$`)
var batchUIID = regexp.MustCompile(`^[A-Za-z0-9_]{20,80}$`)

func (x *MultiApp) vaultPath() string { return filepath.Join(x.parent.data, "multi-batch-vault") }
func (x *MultiApp) runVault(args ...string) (any, error) {
	params := append([]string{"multi", "batch"}, args...)
	cmd := exec.Command(x.program, params...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("batch vault operation rejected: %v: %s", err, string(output))
	}
	var value any
	if err = json.Unmarshal(output, &value); err != nil {
		return nil, fmt.Errorf("invalid batch vault JSON: %w", err)
	}
	return value, nil
}
func (x *MultiApp) batchStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	scenario, version := r.URL.Query().Get("scenario"), r.URL.Query().Get("version")
	if !batchUIName.MatchString(scenario) || !batchUIName.MatchString(version) {
		fail(w, 422, errors.New("场景与版本仅允许安全英文字符"))
		return
	}
	v, e := x.runVault("status", x.vaultPath(), scenario, version)
	if e != nil {
		fail(w, 422, e)
		return
	}
	jsonOut(w, 200, v)
}
func (x *MultiApp) batchRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	v, e := x.runVault("list", x.vaultPath())
	if e != nil {
		fail(w, 500, e)
		return
	}
	jsonOut(w, 200, v)
}
func (x *MultiApp) batchAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct{ Job, Scenario, Version string }
	if e := multiRequest(w, r, &req, 2048); e != nil {
		fail(w, 400, e)
		return
	}
	if !batchUIID.MatchString(req.Job) || !batchUIName.MatchString(req.Scenario) || !batchUIName.MatchString(req.Version) {
		fail(w, 422, errors.New("任务 ID / 场景 / 版本不合法"))
		return
	}
	x.mu.Lock()
	job := x.jobs[req.Job]
	var status, output string
	if job != nil {
		status = job.Status
		output = job.Output
	}
	x.mu.Unlock()
	if status != "PASS" {
		fail(w, 409, errors.New("仅允许将已完成且 PASS 的完整任务登记为正式版本候选"))
		return
	}
	// Do not accept a caller-supplied directory; only the matching Studio job.
	v, e := x.runVault("add", x.vaultPath(), req.Scenario, req.Version, output)
	if e != nil {
		fail(w, 422, e)
		return
	}
	jsonOut(w, 200, v)
}
func (x *MultiApp) batchPublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct{ Scenario, Version, ID, Expected string }
	if e := multiRequest(w, r, &req, 2048); e != nil {
		fail(w, 400, e)
		return
	}
	if !batchUIName.MatchString(req.Scenario) || !batchUIName.MatchString(req.Version) || !batchUIID.MatchString(req.ID) || (req.Expected != "none" && !batchUIID.MatchString(req.Expected)) {
		fail(w, 422, errors.New("非法版本或旧版校验 ID"))
		return
	}
	v, e := x.runVault("publish", x.vaultPath(), req.Scenario, req.Version, req.ID, "--expect", req.Expected)
	if e != nil {
		fail(w, 409, e)
		return
	}
	jsonOut(w, 200, v)
}
func (x *MultiApp) batchRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct{ ID string }
	if e := multiRequest(w, r, &req, 2048); e != nil {
		fail(w, 400, e)
		return
	}
	if !batchUIID.MatchString(req.ID) {
		fail(w, 422, errors.New("非法批次 ID"))
		return
	}
	path := filepath.Join(x.parent.data, "multi-restores", req.ID+"-"+time.Now().UTC().Format("20060102T150405.000000000"))
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		fail(w, 500, e)
		return
	}
	v, e := x.runVault("restore", x.vaultPath(), req.ID, path)
	if e != nil {
		fail(w, 422, e)
		return
	}
	jsonOut(w, 200, v)
}
func (x *MultiApp) batchPrunePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	if strings.TrimSpace(r.URL.RawQuery) != "" {
		fail(w, 400, errors.New("预览使用默认安全保留策略，其他策略通过命令行调整"))
		return
	}
	v, e := x.runVault("prune", x.vaultPath(), "--keep-previous", "1", "--older-than-days", "30")
	if e != nil {
		fail(w, 422, e)
		return
	}
	jsonOut(w, 200, v)
}
