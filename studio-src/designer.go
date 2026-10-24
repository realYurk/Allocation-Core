package main

// Editable rule design is an isolated Studio feature. Allocation Core is untouched.
// Templates are read-only; edits always go to an independent user-data directory.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var safeFile = regexp.MustCompile(`^[A-Za-z0-9_-]+\.csv$`)
var safeName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,48}$`)

func (a *Application) registerDesigner(m *http.ServeMux) {
	m.HandleFunc("/designer", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "method", 405)
			return
		}
		b, e := publicFS.ReadFile("public/designer.html")
		if e != nil {
			fail(w, 500, e)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	m.HandleFunc("/api/designer/clone", a.designerClone)
	m.HandleFunc("/api/designer/rule", a.designerRule)
	m.HandleFunc("/api/designer/check", a.designerCheck)
	m.HandleFunc("/api/designer/dataset", a.designerDataset)
}
func parseBody(w http.ResponseWriter, r *http.Request, limit int64, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var other any
	if e := d.Decode(&other); e != io.EOF {
		return errors.New("JSON body must contain exactly one object")
	}
	return nil
}
func (a *Application) designerClone(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	var req struct {
		From string `json:"from"`
		Name string `json:"name"`
	}
	if e := parseBody(w, r, 4096, &req); e != nil {
		fail(w, 400, e)
		return
	}
	if !safeName.MatchString(req.Name) {
		fail(w, 400, errors.New("项目标识请使用 1–48 位英文字母、数字或下划线/短横线"))
		return
	}
	rule, src, _, e := a.readCase(req.From)
	if e != nil {
		fail(w, 400, e)
		return
	}
	_ = rule
	dst := filepath.Join(a.custom, req.Name)
	if _, e = os.Lstat(dst); e == nil {
		fail(w, 409, errors.New("项目已存在，请换一个名称"))
		return
	}
	temp := filepath.Join(a.custom, ".pending-"+randomID())
	if e = os.Mkdir(temp, 0700); e != nil {
		fail(w, 500, e)
		return
	}
	defer os.RemoveAll(temp)
	entries, e := os.ReadDir(src)
	if e != nil {
		fail(w, 500, e)
		return
	}
	for _, ent := range entries {
		if ent.IsDir() || !ent.Type().IsRegular() {
			continue
		}
		name := ent.Name()
		if name != "rule.json" && !safeFile.MatchString(name) {
			continue
		}
		st, e := os.Stat(filepath.Join(src, name))
		if e != nil || st.Size() > 100*1024*1024 {
			fail(w, 400, errors.New("模板含有过大的数据文件"))
			return
		}
		data, e := os.ReadFile(filepath.Join(src, name))
		if e != nil {
			fail(w, 500, e)
			return
		}
		if e = os.WriteFile(filepath.Join(temp, name), data, 0600); e != nil {
			fail(w, 500, e)
			return
		}
	}
	if e = os.Rename(temp, dst); e != nil {
		fail(w, 500, e)
		return
	}
	jsonOut(w, 201, map[string]string{"case": "user_" + req.Name, "message": "已建立独立规则副本；内置示例保持只读"})
}
func (a *Application) caseBusy(id string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, j := range a.jobs {
		if j.Case == id && (j.Status == "QUEUED" || j.Status == "RUNNING") {
			return true
		}
	}
	return false
}
func (a *Application) userDir(id string) (string, error) {
	if !strings.HasPrefix(id, "user_") || !safeName.MatchString(strings.TrimPrefix(id, "user_")) {
		return "", errors.New("只能修改已复制的个人项目")
	}
	d, k, e := a.locate(id)
	if e != nil {
		return "", e
	}
	if k != "custom" {
		return "", errors.New("示例是只读的")
	}
	return d, nil
}
func (a *Application) designerRule(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		id := r.URL.Query().Get("case")
		_, dir, kind, e := a.readCase(id)
		if e != nil {
			fail(w, 404, e)
			return
		}
		b, e := os.ReadFile(filepath.Join(dir, "rule.json"))
		if e != nil {
			fail(w, 500, e)
			return
		}
		var rule any
		if e = json.Unmarshal(b, &rule); e != nil {
			fail(w, 500, e)
			return
		}
		fingerprint, _ := a.designerFingerprint(id)
		jsonOut(w, 200, map[string]any{"rule": rule, "editable": kind == "custom", "case": id, "sha256": fingerprint})
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	var req struct {
		Case    string          `json:"case"`
		Rule    json.RawMessage `json:"rule"`
		BaseSHA string          `json:"base_sha"`
	}
	if e := parseBody(w, r, 512*1024, &req); e != nil {
		fail(w, 400, e)
		return
	}
	dir, e := a.userDir(req.Case)
	if e != nil {
		fail(w, 403, e)
		return
	}
	if a.caseBusy(req.Case) {
		fail(w, 409, errors.New("该场景正在计算，请等任务完成后修改规则"))
		return
	}
	path := filepath.Join(dir, "rule.json")
	current, e := os.ReadFile(path)
	if e != nil {
		fail(w, 500, e)
		return
	}
	sha := sha256.Sum256(current)
	if req.BaseSHA == "" || req.BaseSHA != hex.EncodeToString(sha[:]) {
		fail(w, 409, errors.New("规则已被另一窗口修改，请重新加载后再编辑"))
		return
	}
	cleaned, e := validateRuleBytes(req.Rule)
	if e != nil {
		fail(w, 422, e)
		return
	}
	// No core writes until the new rule has passed the authoritative doctor.
	output, e := a.checkRuleFile(dir, cleaned)
	if e != nil {
		jsonOut(w, 422, map[string]any{"error": "后端规则检查未通过", "detail": output})
		return
	}
	tmp, e := os.CreateTemp(dir, ".rule-change-")
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer os.Remove(tmp.Name())
	if _, e = tmp.Write(cleaned); e != nil {
		tmp.Close()
		fail(w, 500, e)
		return
	}
	if e = tmp.Sync(); e != nil {
		tmp.Close()
		fail(w, 500, e)
		return
	}
	if e = tmp.Close(); e != nil {
		fail(w, 500, e)
		return
	}
	if e = os.Rename(tmp.Name(), path); e != nil {
		fail(w, 500, e)
		return
	}
	newHash := sha256.Sum256(cleaned)
	jsonOut(w, 200, map[string]any{"saved": true, "sha256": hex.EncodeToString(newHash[:]), "doctor": output})
}
func validateRuleBytes(raw json.RawMessage) ([]byte, error) {
	if len(raw) > 480*1024 || len(raw) == 0 {
		return nil, errors.New("规则不能为空或超过 480KB")
	}
	var rule map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	if e := dec.Decode(&rule); e != nil {
		return nil, e
	}
	var extra any
	if e := dec.Decode(&extra); e != io.EOF {
		return nil, errors.New("规则只能包含一个 JSON 对象")
	}
	if len(rule) == 0 {
		return nil, errors.New("规则必须是对象")
	}
	var schema string
	if e := json.Unmarshal(rule["schema"], &schema); e != nil || schema != "allocation-ir/2.0" {
		return nil, errors.New("请保留规则格式 allocation-ir/2.0")
	}
	var ds struct {
		Amounts map[string]string `json:"amounts"`
		Drivers map[string]string `json:"drivers"`
	}
	if e := json.Unmarshal(rule["datasets"], &ds); e != nil {
		return nil, errors.New("无效数据集定义")
	}
	for _, group := range []map[string]string{ds.Amounts, ds.Drivers} {
		for _, name := range group {
			if !safeFile.MatchString(name) {
				return nil, fmt.Errorf("数据文件必须是项目目录中的英文 CSV 文件: %q", name)
			}
		}
	}
	var stages []any
	if e := json.Unmarshal(rule["stages"], &stages); e != nil || len(stages) == 0 || len(stages) > 100 {
		return nil, errors.New("至少配置一个分摊阶段，最多100个")
	}
	return json.MarshalIndent(rule, "", "  ")
}
func (a *Application) checkRuleFile(dir string, raw []byte) (string, error) {
	tmp, e := os.CreateTemp(dir, ".draft-check-*.json")
	if e != nil {
		return "", e
	}
	defer os.Remove(tmp.Name())
	if _, e = tmp.Write(raw); e != nil {
		tmp.Close()
		return "", e
	}
	if e = tmp.Close(); e != nil {
		return "", e
	}
	// 10-second guard for malformed huge CSV inputs; production validation can run separately.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, a.engine, "doctor", tmp.Name())
	cmd.Dir = a.home
	out, e := cmd.CombinedOutput()
	if len(out) > 16000 {
		out = out[:16000]
	}
	return string(out), e
}
func (a *Application) designerCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	var req struct {
		Case string          `json:"case"`
		Rule json.RawMessage `json:"rule"`
	}
	if e := parseBody(w, r, 512*1024, &req); e != nil {
		fail(w, 400, e)
		return
	}
	_, dir, _, e := a.readCase(req.Case)
	if e != nil {
		fail(w, 404, e)
		return
	}
	raw, e := validateRuleBytes(req.Rule)
	if e != nil {
		fail(w, 422, e)
		return
	}
	log, e := a.checkRuleFile(dir, raw)
	if e != nil {
		jsonOut(w, 422, map[string]any{"status": "FAILED", "detail": log})
		return
	}
	jsonOut(w, 200, map[string]any{"status": "PASS", "detail": log})
}
func (a *Application) designerDataset(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		id := r.URL.Query().Get("case")
		file := r.URL.Query().Get("file")
		if !safeFile.MatchString(file) {
			fail(w, 400, errors.New("不安全的文件名"))
			return
		}
		rule, dir, _, e := a.readCase(id)
		if e != nil {
			fail(w, 404, e)
			return
		}
		found := false
		for _, m := range []map[string]string{rule.Datasets.Amounts, rule.Datasets.Drivers} {
			for _, v := range m {
				if v == file {
					found = true
				}
			}
		}
		if !found {
			fail(w, 404, errors.New("文件没有在规则中声明"))
			return
		}
		stat, e := os.Lstat(filepath.Join(dir, file))
		if e != nil || !stat.Mode().IsRegular() {
			fail(w, 404, errors.New("数据文件不存在"))
			return
		}
		if stat.Size() > 1024*1024 {
			jsonOut(w, 200, map[string]any{"file": file, "truncated": true, "size": stat.Size()})
			return
		}
		b, e := os.ReadFile(filepath.Join(dir, file))
		if e != nil {
			fail(w, 500, e)
			return
		}
		jsonOut(w, 200, map[string]any{"file": file, "text": string(b), "truncated": false, "size": len(b)})
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	var req struct {
		Case    string `json:"case"`
		File    string `json:"file"`
		Content string `json:"content"`
	}
	if e := parseBody(w, r, 2*1024*1024, &req); e != nil {
		fail(w, 400, e)
		return
	}
	if !safeFile.MatchString(req.File) {
		fail(w, 400, errors.New("只支持英文文件名 .csv"))
		return
	}
	dir, e := a.userDir(req.Case)
	if e != nil {
		fail(w, 403, e)
		return
	}
	rule, _, _, e := a.readCase(req.Case)
	if e != nil {
		fail(w, 422, e)
		return
	}
	referenced := false
	for _, m := range []map[string]string{rule.Datasets.Amounts, rule.Datasets.Drivers} {
		for _, v := range m {
			if v == req.File {
				referenced = true
			}
		}
	}
	if !referenced {
		fail(w, 422, errors.New("请先在规则数据集中登记这个 CSV 文件名"))
		return
	}
	if a.caseBusy(req.Case) {
		fail(w, 409, errors.New("该场景正在计算，请等任务完成后修改数据"))
		return
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(req.Content, "\ufeff")))
	header, e := reader.Read()
	if e != nil || len(header) < 2 {
		fail(w, 422, errors.New("CSV 表头至少需要两列"))
		return
	}
	seen := map[string]bool{}
	for _, h := range header {
		if strings.TrimSpace(h) == "" || seen[h] {
			fail(w, 422, errors.New("CSV 表头不能为空或重复"))
			return
		}
		seen[h] = true
	}
	if len(req.Content) > 1024*1024 {
		fail(w, 413, errors.New("可视化编辑单次上限1MB，大文件请使用数据接入工具"))
		return
	}
	// CSV updates are atomic; old file untouched until all checks pass.
	target := filepath.Join(dir, req.File)
	if st, e := os.Lstat(target); e == nil && (!st.Mode().IsRegular()) {
		fail(w, 400, errors.New("禁止覆盖非普通文件"))
		return
	}
	temp, e := os.CreateTemp(dir, ".csv-write-*.tmp")
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer os.Remove(temp.Name())
	if _, e = temp.WriteString(req.Content); e != nil {
		temp.Close()
		fail(w, 500, e)
		return
	}
	if e = temp.Sync(); e != nil {
		temp.Close()
		fail(w, 500, e)
		return
	}
	temp.Close()
	if e = os.Rename(temp.Name(), target); e != nil {
		fail(w, 500, e)
		return
	}
	jsonOut(w, 200, map[string]any{"saved": true, "file": req.File, "bytes": len(req.Content)})
}

// Stable audit fingerprint for the UI; this only indicates whether another editor changed the rule.
func (a *Application) designerFingerprint(id string) (string, error) {
	_, dir, _, e := a.readCase(id)
	if e != nil {
		return "", e
	}
	b, e := os.ReadFile(filepath.Join(dir, "rule.json"))
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func sortedKeys(m map[string]string) []string {
	a := make([]string, 0, len(m))
	for k := range m {
		a = append(a, k)
	}
	sort.Strings(a)
	return a
}

var _ = time.Now

func (a *Application) designerImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	var req struct {
		Case    string `json:"case"`
		Kind    string `json:"kind"`
		Alias   string `json:"alias"`
		File    string `json:"file"`
		Content string `json:"content"`
		BaseSHA string `json:"base_sha"`
	}
	if e := parseBody(w, r, 2*1024*1024, &req); e != nil {
		fail(w, 400, e)
		return
	}
	if req.Kind != "amounts" && req.Kind != "drivers" {
		fail(w, 400, errors.New("数据类型只能是 amounts 或 drivers"))
		return
	}
	if !safeName.MatchString(req.Alias) || !safeFile.MatchString(req.File) {
		fail(w, 400, errors.New("数据集 ID 与文件名必须使用英文/数字/下划线"))
		return
	}
	if len(req.Content) > 1024*1024 {
		fail(w, 413, errors.New("浏览器导入限制 1 MB；海量 CSV 请走现有批量数据接入"))
		return
	}
	rd := csv.NewReader(strings.NewReader(strings.TrimPrefix(req.Content, "\ufeff")))
	head, e := rd.Read()
	if e != nil || len(head) < 2 {
		fail(w, 422, errors.New("CSV 表头必须至少两列"))
		return
	}
	seen := map[string]bool{}
	for _, h := range head {
		if h == "" || seen[h] {
			fail(w, 422, errors.New("CSV 表头不能为空或重复"))
			return
		}
		seen[h] = true
	}
	for {
		_, e = rd.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			fail(w, 422, fmt.Errorf("CSV 格式错误: %w", e))
			return
		}
	}
	dir, e := a.userDir(req.Case)
	if e != nil {
		fail(w, 403, e)
		return
	}
	if a.caseBusy(req.Case) {
		fail(w, 409, errors.New("该场景正在计算，请等任务完成后导入数据"))
		return
	}
	path := filepath.Join(dir, "rule.json")
	old, e := os.ReadFile(path)
	if e != nil {
		fail(w, 500, e)
		return
	}
	h := sha256.Sum256(old)
	if req.BaseSHA != "" && req.BaseSHA != hex.EncodeToString(h[:]) {
		fail(w, 409, errors.New("规则已更新，请重新加载"))
		return
	}
	var rule map[string]any
	if e = json.Unmarshal(old, &rule); e != nil {
		fail(w, 500, e)
		return
	}
	ds, ok := rule["datasets"].(map[string]any)
	if !ok {
		fail(w, 422, errors.New("规则缺少 datasets"))
		return
	}
	group, ok := ds[req.Kind].(map[string]any)
	if !ok {
		group = map[string]any{}
		ds[req.Kind] = group
	}
	if _, ok = group[req.Alias]; ok {
		fail(w, 409, errors.New("数据集 ID 已存在"))
		return
	}
	dst := filepath.Join(dir, req.File)
	if _, e = os.Lstat(dst); e == nil {
		fail(w, 409, errors.New("目标 CSV 文件名已经存在"))
		return
	}
	group[req.Alias] = req.File
	// Imported data is a separate file; rule changes remain a distinct validated user action.
	// The rule is not auto-committed because a new driver may need allocation wiring first.
	if e = os.WriteFile(dst, []byte(req.Content), 0600); e != nil {
		fail(w, 500, e)
		return
	}
	jsonOut(w, 201, map[string]any{"file": req.File, "alias": req.Alias, "kind": req.Kind, "rule": rule, "message": "数据已导入；请在规则中选择该数据集并点击保存"})
}
