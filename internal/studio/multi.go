package studio

// Multi-account Studio is strictly local. It never implements financial formulas;
// it invokes allocation_multi, which in turn runs the authoritative alloc binary.
import (
	"allocationcore/internal/multi"
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
	"sync"
	"time"
)

type MultiApp struct {
	parent  *Application
	root    string
	program string
	mu      sync.Mutex
	jobs    map[string]*Job
	busy    map[string]bool
	slots   chan struct{}
	sem     chan struct{}
	cancels map[string]context.CancelFunc
}

var multiSafe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,48}$`)
var multiCSV = regexp.MustCompile(`^[A-Za-z0-9_-]+\.csv$`)

func (a *Application) registerMulti(m *http.ServeMux) *MultiApp {
	program, err := os.Executable()
	if err != nil {
		panic(err)
	}
	x := &MultiApp{parent: a, root: filepath.Join(a.data, "multi"), program: program, jobs: map[string]*Job{}, busy: map[string]bool{}, slots: make(chan struct{}, 8), sem: make(chan struct{}, 2), cancels: map[string]context.CancelFunc{}}
	if err := os.MkdirAll(x.root, 0700); err != nil {
		panic(err)
	}
	recovered, err := recoverJournals(filepath.Join(a.data, "multi-jobs"), true)
	if err != nil {
		panic(fmt.Errorf("multi job recovery: %w", err))
	}
	x.jobs = recovered
	m.HandleFunc("/multi", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		b, e := publicFS.ReadFile("public/multi.html")
		if e != nil {
			fail(w, 500, e)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
	m.HandleFunc("/api/multi/projects", x.projects)
	m.HandleFunc("/api/multi/create", x.create)
	m.HandleFunc("/api/multi/onboard", x.createFromAmountsCSV)
	m.HandleFunc("/api/multi/visual-draft", x.createVisualDraft)
	m.HandleFunc("/api/multi/driver-needs", x.driverNeeds)
	m.HandleFunc("/api/multi/driver-import", x.importMappedDriver)
	m.HandleFunc("/api/multi/mapping-profiles", x.mappingProfiles)
	m.HandleFunc("/api/multi/import-template", x.importTemplateFromDesigner)
	m.HandleFunc("/api/multi/project", x.project)
	m.HandleFunc("/api/multi/template-guide", x.templateGuide)
	m.HandleFunc("/api/multi/preflight", x.preflight)
	m.HandleFunc("/api/multi/driver-coverage", x.driverCoverage)
	m.HandleFunc("/api/multi/reconcile", x.reconcile)
	m.HandleFunc("/api/multi/reference", x.referenceStatus)
	m.HandleFunc("/api/multi/reference-upload", x.uploadReference)
	m.HandleFunc("/api/multi/acceptance", x.acceptanceEvidence)
	m.HandleFunc("/api/multi/cache", x.cacheScope)
	m.HandleFunc("/api/multi/assign", x.assign)
	m.HandleFunc("/api/multi/factor", x.factor)
	m.HandleFunc("/api/multi/account-plan", x.accountPlan)
	m.HandleFunc("/api/multi/upload", x.upload)
	m.HandleFunc("/api/multi/run", x.run)
	m.HandleFunc("/api/multi/jobs", x.jobList)
	m.HandleFunc("/api/multi/stage-evidence", x.stageEvidence)
	m.HandleFunc("/api/multi/compare-runs", x.compareRuns)
	m.HandleFunc("/api/multi/jobs/", x.jobDetail)
	m.HandleFunc("/api/multi/batch/status", x.batchStatus)
	m.HandleFunc("/api/multi/batch/records", x.batchRecords)
	m.HandleFunc("/api/multi/batch/add", x.batchAdd)
	m.HandleFunc("/api/multi/batch/publish", x.batchPublish)
	m.HandleFunc("/api/multi/batch/restore", x.batchRestore)
	m.HandleFunc("/api/multi/batch/prune-preview", x.batchPrunePreview)
	return x
}
func (x *MultiApp) projectDir(name string) (string, error) {
	if !multiSafe.MatchString(name) {
		return "", errors.New("项目名称只允许英文数字下划线横线，最长48字符")
	}
	path := filepath.Join(x.root, name)
	st, e := os.Lstat(path)
	if e != nil {
		return "", e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("非法项目目录")
	}
	return path, nil
}
func (x *MultiApp) projects(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	dirs, e := os.ReadDir(x.root)
	if e != nil {
		fail(w, 500, e)
		return
	}
	names := []string{}
	for _, d := range dirs {
		if d.IsDir() && multiSafe.MatchString(d.Name()) {
			names = append(names, d.Name())
		}
	}
	jsonOut(w, 200, map[string]any{"projects": names})
}
func multiRequest(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return errors.New("请求只能包含一个 JSON 对象")
	}
	return nil
}
func copyFixture(from, to string) error {
	return filepath.Walk(from, func(src string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		rel, er := filepath.Rel(from, src)
		if er != nil {
			return er
		}
		dest := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		if !info.Mode().IsRegular() {
			return errors.New("fixture must be regular files")
		}
		data, er := os.ReadFile(src)
		if er != nil {
			return er
		}
		return os.WriteFile(dest, data, 0600)
	})
}
func (x *MultiApp) create(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if e := multiRequest(w, r, &req, 2048); e != nil {
		fail(w, 400, e)
		return
	}
	if !multiSafe.MatchString(req.Name) {
		fail(w, 422, errors.New("项目名只能使用英文数字下划线横线"))
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	dest := filepath.Join(x.root, req.Name)
	if _, e := os.Lstat(dest); e == nil {
		fail(w, 409, errors.New("项目已存在"))
		return
	}
	tmp, e := os.MkdirTemp(x.root, ".new-")
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer os.RemoveAll(tmp)
	if e = copyFixture(filepath.Join(x.parent.data, "testdata", "multi_sample"), tmp); e != nil {
		fail(w, 500, e)
		return
	}
	if e = os.Rename(tmp, dest); e != nil {
		fail(w, 500, e)
		return
	}
	jsonOut(w, 201, map[string]any{"project": req.Name})
}

// preflight validates all assigned accounts and the distinct driver inputs.
// It is read-only, does not publish results, and never modifies rule files.
func (x *MultiApp) preflight(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := multiRequest(w, r, &req, 2048); err != nil {
		fail(w, 400, err)
		return
	}
	dir, err := x.projectDir(req.Name)
	if err != nil {
		fail(w, 404, err)
		return
	}
	// A checked response is useful even when BLOCK is found. The page displays
	// exact issues rather than treating the response as an opaque HTTP error.
	jsonOut(w, 200, multi.InspectProject(r.Context(), dir))
}

// driverCoverage checks first-source-stage Driver matching without executing a batch.
func (x *MultiApp) driverCoverage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := multiRequest(w, r, &req, 2048); err != nil {
		fail(w, 400, err)
		return
	}
	dir, err := x.projectDir(req.Name)
	if err != nil {
		fail(w, 404, err)
		return
	}
	jsonOut(w, 200, multi.InspectDriverCoverage(r.Context(), dir))
}

func readAssignments(path string) ([]map[string]string, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	rd := csv.NewReader(f)
	all, e := rd.ReadAll()
	if e != nil {
		return nil, e
	}
	if len(all) == 0 || len(all[0]) != 2 || strings.TrimPrefix(all[0][0], "\ufeff") != "account" || all[0][1] != "template" {
		return nil, errors.New("assignments.csv 表头必须为 account,template")
	}
	out := []map[string]string{}
	used := map[string]bool{}
	for _, a := range all[1:] {
		if len(a) != 2 || a[0] == "" || a[1] == "" || used[a[0]] {
			return nil, errors.New("科目分派空值或重复")
		}
		used[a[0]] = true
		out = append(out, map[string]string{"account": a[0], "template": a[1]})
	}
	return out, nil
}
func (x *MultiApp) project(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	// Snapshot both the visible assignments and their edit token under
	// the same lock as rule writes. A second Studio tab must not silently
	// overwrite a newer assignment decision.
	x.mu.Lock()
	defer x.mu.Unlock()
	dir, e := x.projectDir(r.URL.Query().Get("name"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	assignmentPath := filepath.Join(dir, "assignments.csv")
	assigned, e := readAssignments(assignmentPath)
	if e != nil {
		fail(w, 422, e)
		return
	}
	assignmentBytes, e := os.ReadFile(assignmentPath)
	if e != nil {
		fail(w, 500, e)
		return
	}
	assignmentHash := sha256.Sum256(assignmentBytes)
	ents, e := os.ReadDir(filepath.Join(dir, "templates"))
	if e != nil {
		fail(w, 422, e)
		return
	}
	options := []string{}
	for _, f := range ents {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".json") {
			options = append(options, f.Name())
		}
	}
	sort.Strings(options)
	templateColumns := map[string][]string{}
	templatePrecisions := map[string]int{}
	for _, name := range options {
		precision, dimensions, err := readOnboardingTemplate(dir, name)
		if err == nil {
			templateColumns[name] = append(append([]string{}, dimensions...), "amount")
			templatePrecisions[name] = precision
		}
	}
	files := []map[string]any{}
	for _, n := range []string{"amounts.csv", "assignments.csv", "overrides.csv"} {
		if st, e := os.Stat(filepath.Join(dir, n)); e == nil {
			files = append(files, map[string]any{"name": n, "bytes": st.Size()})
		}
	}
	cfgBytes, cfgErr := os.ReadFile(filepath.Join(dir, "project.json"))
	cfgScope := "run"
	reuseWorkers := false
	cfgHash := ""
	if cfgErr == nil {
		var cfg struct {
			DriverCacheScope string `json:"driver_cache_scope"`
			DriverDiskIndex  bool   `json:"driver_disk_index"`
			ReuseWorkers     bool   `json:"reuse_workers"`
		}
		if json.Unmarshal(cfgBytes, &cfg) == nil && cfg.DriverCacheScope == "project" && cfg.DriverDiskIndex {
			cfgScope = "project"
		}
		reuseWorkers = cfg.ReuseWorkers
		sum := sha256.Sum256(cfgBytes)
		cfgHash = hex.EncodeToString(sum[:])
	}
	jsonOut(w, 200, map[string]any{"reuse_workers": reuseWorkers, "driver_cache_scope": cfgScope, "project_sha256": cfgHash, "assignments_sha256": hex.EncodeToString(assignmentHash[:]), "name": r.URL.Query().Get("name"), "assignments": assigned, "templates": options, "template_columns": templateColumns, "template_precisions": templatePrecisions, "files": files, "count": len(assigned), "manualDataDir": dir})
}

// cacheScope changes only generic execution settings, never financial rules.
// Optimistic hash and project-level mutex prevent edit-vs-run conflicts.
func (x *MultiApp) cacheScope(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Name         string `json:"name"`
		Scope        string `json:"scope"`
		BaseSHA      string `json:"base_sha256"`
		ReuseWorkers *bool  `json:"reuse_workers,omitempty"`
	}
	if e := multiRequest(w, r, &req, 4096); e != nil {
		fail(w, 400, e)
		return
	}
	if req.Scope != "project" && req.Scope != "run" {
		fail(w, 422, errors.New("cache scope must be project or run"))
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	dir, e := x.projectDir(req.Name)
	if e != nil {
		fail(w, 404, e)
		return
	}
	if x.busy[req.Name] {
		fail(w, 409, errors.New("不能修改正在运行的项目 / Cannot edit running project"))
		return
	}
	path := filepath.Join(dir, "project.json")
	old, e := os.ReadFile(path)
	if e != nil {
		fail(w, 500, e)
		return
	}
	sha := sha256.Sum256(old)
	if hex.EncodeToString(sha[:]) != req.BaseSHA {
		fail(w, 409, errors.New("project.json has changed / 项目配置已被修改，请刷新"))
		return
	}
	var cfg map[string]any
	if e = json.Unmarshal(old, &cfg); e != nil {
		fail(w, 422, e)
		return
	}
	cfg["driver_cache_scope"] = req.Scope
	if req.ReuseWorkers != nil {
		cfg["reuse_workers"] = *req.ReuseWorkers
	}
	if req.Scope == "project" {
		cfg["driver_disk_index"] = true
		if n, ok := cfg["disk_partitions"].(float64); !ok || n < 2 {
			cfg["disk_partitions"] = 8
		}
	}
	raw, e := json.MarshalIndent(cfg, "", "  ")
	if e != nil {
		fail(w, 500, e)
		return
	}
	staged := path + ".part"
	if e = os.WriteFile(staged, raw, 0600); e != nil {
		fail(w, 500, e)
		return
	}
	if e = os.Rename(staged, path); e != nil {
		os.Remove(staged)
		fail(w, 500, e)
		return
	}
	jsonOut(w, 200, map[string]any{"scope": req.Scope, "status": "OK"})
}
func (x *MultiApp) assign(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Name       string `json:"name"`
		BaseSHA256 string `json:"base_sha256"`
		Updates    []struct {
			Account  string `json:"account"`
			Template string `json:"template"`
		} `json:"updates"`
	}
	if e := multiRequest(w, r, &req, 1<<20); e != nil {
		fail(w, 400, e)
		return
	}
	if len(req.Updates) == 0 || len(req.Updates) > 2000 {
		fail(w, 400, errors.New("每次修改1-2000个科目"))
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.busy[req.Name] {
		fail(w, 409, errors.New("当前项目正在计算，不允许修改规则"))
		return
	}
	dir, e := x.projectDir(req.Name)
	if e != nil {
		fail(w, 404, e)
		return
	}
	assignmentPath := filepath.Join(dir, "assignments.csv")
	data, e := readAssignments(assignmentPath)
	if e != nil {
		fail(w, 422, e)
		return
	}
	previous, e := os.ReadFile(assignmentPath)
	if e != nil {
		fail(w, 500, e)
		return
	}
	currentHash := sha256.Sum256(previous)
	if req.BaseSHA256 == "" || req.BaseSHA256 != hex.EncodeToString(currentHash[:]) {
		fail(w, 409, errors.New("科目规则分派已被其他操作修改；请刷新项目并重新核对更改 / assignments changed, reload before saving"))
		return
	}
	positions := map[string]int{}
	for i, v := range data {
		positions[v["account"]] = i
	}
	for _, up := range req.Updates {
		ix, ok := positions[up.Account]
		if !ok {
			fail(w, 422, fmt.Errorf("未知科目 %q，需先把它加入 assignments.csv", up.Account))
			return
		}
		if !multiSafe.MatchString(strings.TrimSuffix(up.Template, ".json")) || !strings.HasSuffix(up.Template, ".json") {
			fail(w, 422, errors.New("非法模板名"))
			return
		}
		info, e := os.Lstat(filepath.Join(dir, "templates", up.Template))
		if e != nil || !info.Mode().IsRegular() {
			fail(w, 422, errors.New("模板不存在"))
			return
		}
		data[ix]["template"] = up.Template
	}
	tmp, e := os.CreateTemp(dir, ".assignments-")
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer os.Remove(tmp.Name())
	wr := csv.NewWriter(tmp)
	_ = wr.Write([]string{"account", "template"})
	for _, v := range data {
		_ = wr.Write([]string{v["account"], v["template"]})
	}
	wr.Flush()
	if e = wr.Error(); e == nil {
		e = tmp.Sync()
	}
	if er := tmp.Close(); e == nil {
		e = er
	}
	if e != nil {
		fail(w, 500, e)
		return
	}
	if e = os.Rename(tmp.Name(), filepath.Join(dir, "assignments.csv")); e != nil {
		fail(w, 500, e)
		return
	}
	jsonOut(w, 200, map[string]any{"saved": len(req.Updates)})
}
func (x *MultiApp) upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Name    string `json:"name"`
		File    string `json:"file"`
		Content string `json:"content"`
	}
	if e := multiRequest(w, r, &req, 7<<20); e != nil {
		fail(w, 400, e)
		return
	}
	if len(req.Content) > 5<<20 || !multiCSV.MatchString(req.File) {
		fail(w, 422, errors.New("只接受最多5MB CSV；更大文件请放入项目数据目录"))
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.busy[req.Name] {
		fail(w, 409, errors.New("计算中禁止修改数据"))
		return
	}
	dir, e := x.projectDir(req.Name)
	if e != nil {
		fail(w, 404, e)
		return
	}
	if req.File != "amounts.csv" && req.File != "overrides.csv" {
		fail(w, 422, errors.New("高级上传仅允许 amounts.csv / overrides.csv；科目分派请走冲突校验接口，参考文件请走流式上传接口"))
		return
	}
	rd := csv.NewReader(strings.NewReader(strings.TrimPrefix(req.Content, "\ufeff")))
	if _, e = rd.ReadAll(); e != nil {
		fail(w, 422, e)
		return
	}
	tmp, e := os.CreateTemp(dir, ".input-")
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer os.Remove(tmp.Name())
	_, e = tmp.WriteString(req.Content)
	if e == nil {
		e = tmp.Sync()
	}
	if er := tmp.Close(); e == nil {
		e = er
	}
	if e != nil {
		fail(w, 500, e)
		return
	}
	if e = os.Rename(tmp.Name(), filepath.Join(dir, req.File)); e != nil {
		fail(w, 500, e)
		return
	}
	jsonOut(w, 200, map[string]any{"saved": req.File})
}
func (x *MultiApp) run(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if e := multiRequest(w, r, &req, 2048); e != nil {
		fail(w, 400, e)
		return
	}
	x.mu.Lock()
	dir, e := x.projectDir(req.Name)
	if e != nil {
		x.mu.Unlock()
		fail(w, 404, e)
		return
	}
	if x.busy[req.Name] {
		x.mu.Unlock()
		fail(w, 409, errors.New("这个项目已经有任务在运行"))
		return
	}
	st, e := os.Stat(x.program)
	if e != nil || st.IsDir() {
		x.mu.Unlock()
		fail(w, 500, errors.New("统一程序文件不可用，请从完整发行包启动 AllocationCore.exe"))
		return
	}
	// Explicit bounded queue (including running jobs); never spawn an unbounded
	// backlog of goroutines when Studio receives repeated submit requests.
	select {
	case x.slots <- struct{}{}:
	default:
		x.mu.Unlock()
		fail(w, http.StatusTooManyRequests, errors.New("多科目任务队列已满（最多8个待执行/执行中任务），请稍后重试"))
		return
	}
	id := randomID()
	ctx, cancel := context.WithCancel(context.Background())
	job := &Job{ID: id, Case: req.Name, Status: "QUEUED", Start: time.Now().Format(time.RFC3339), Output: filepath.Join(x.parent.data, "multi-jobs", id)}
	if e := atomicJobUpdate(job, true); e != nil {
		cancel()
		<-x.slots
		x.mu.Unlock()
		fail(w, 500, fmt.Errorf("cannot persist queued multi job: %w", e))
		return
	}
	x.busy[req.Name] = true
	x.jobs[id] = job
	x.cancels[id] = cancel
	x.mu.Unlock()
	posted := *job
	go x.execute(id, dir, ctx)
	jsonOut(w, 202, posted)
}
func (x *MultiApp) execute(id, dir string, ctx context.Context) {
	defer func() { <-x.slots }()
	x.mu.Lock()
	j := x.jobs[id]
	x.mu.Unlock()
	// A canceled queued job exits before starting an external process.
	select {
	case x.sem <- struct{}{}:
		defer func() { <-x.sem }()
	case <-ctx.Done():
		x.complete(j, "CANCELED", "queued job canceled", "")
		return
	}
	if ctx.Err() != nil {
		x.complete(j, "CANCELED", "queued job canceled", "")
		return
	}
	x.mu.Lock()
	j.Status = "RUNNING"
	saveErr := atomicJobUpdate(j, true)
	x.mu.Unlock()
	if saveErr != nil {
		x.complete(j, "FAILED", "cannot persist RUNNING: "+saveErr.Error(), "")
		return
	}
	if err := os.MkdirAll(filepath.Dir(j.Output), 0700); err != nil {
		x.complete(j, "FAILED", err.Error(), "")
		return
	}
	// Do not kill Multi's process directly: it owns Core children. Instead a
	// file marker makes Multi cancel their CommandContext, including on Windows.
	cancelFile := filepath.Join(filepath.Dir(j.Output), ".cancel-"+id)
	defer os.Remove(cancelFile)
	// A per-job heartbeat prevents an orphan Multi process from continuing
	// indefinitely if the Studio process is killed without a normal shutdown.
	lease := filepath.Join(filepath.Dir(j.Output), ".lease-"+id)
	if err := os.WriteFile(lease, []byte("alive\n"), 0600); err != nil {
		x.complete(j, "FAILED", err.Error(), "")
		return
	}
	leaseStopped := make(chan struct{})
	var leaseWG sync.WaitGroup
	leaseWG.Add(1)
	go func() {
		defer leaseWG.Done()
		tick := time.NewTicker(3 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-leaseStopped:
				return
			case <-tick.C:
				_ = os.WriteFile(lease, []byte("alive\n"), 0600)
			}
		}
	}()
	defer func() { close(leaseStopped); leaseWG.Wait(); _ = os.Remove(lease) }()
	cmd := exec.Command(x.program, "multi", "-project", dir, "-out", j.Output, "-engine", x.parent.engine, "-cancel-file", cancelFile, "-studio-lease", lease)
	out, err := cmd.CombinedOutput()
	if len(out) > 12000 {
		out = out[:12000]
	}
	if err != nil {
		if ctx.Err() != nil {
			x.complete(j, "CANCELED", "user canceled running job", string(out))
		} else {
			x.complete(j, "FAILED", err.Error(), string(out))
		}
		return
	}
	// Successful Core publication wins a last-millisecond cancellation race.
	x.complete(j, "PASS", "", string(out))
}
func (x *MultiApp) complete(j *Job, status, reason, stdout string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	j.Status = status
	j.Error = reason
	j.Log = stdout
	if status == "FAILED" {
		j.Diagnosis, j.Log = extractMultiFailure([]byte(stdout))
	}
	j.End = time.Now().Format(time.RFC3339)
	delete(x.busy, j.Case)
	delete(x.cancels, j.ID)
	_ = os.Remove(filepath.Join(x.parent.data, "multi-jobs", ".cancel-"+j.ID))
	if status != "PASS" {
		_ = os.MkdirAll(j.Output, 0700)
	}
	if err := atomicJobUpdate(j, true); err != nil {
		j.Status = "FAILED"
		j.Error = "multi journal persistence failed: " + err.Error()
		_ = atomicJobUpdate(j, true)
	}
	if _, err := os.Stat(j.Output); err == nil {
		if err := os.WriteFile(filepath.Join(j.Output, "multi_job.json"), mustJSON(j), 0600); err != nil && j.Status == "PASS" {
			j.Status = "FAILED"
			j.Error = "cannot preserve result job provenance: " + err.Error()
			_ = atomicJobUpdate(j, true)
		}
	}
}
func (x *MultiApp) cancelJob(w http.ResponseWriter, id string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	j := x.jobs[id]
	if j == nil {
		fail(w, 404, errors.New("任务不存在"))
		return
	}
	if j.Status != "QUEUED" && j.Status != "RUNNING" && j.Status != "CANCELING" {
		fail(w, 409, errors.New("任务已结束，无法取消"))
		return
	}
	// Write the marker before releasing the lock. The Multi executable checks
	// it every 200 ms, and terminates its authoritative Core child processes.
	marker := filepath.Join(x.parent.data, "multi-jobs", ".cancel-"+id)
	if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
		fail(w, 500, err)
		return
	}
	if err := os.WriteFile(marker, []byte("cancel\n"), 0600); err != nil {
		fail(w, 500, err)
		return
	}
	if cancel, ok := x.cancels[id]; ok {
		cancel()
	}
	j.Status = "CANCELING"
	if err := atomicJobUpdate(j, true); err != nil {
		fail(w, 500, err)
		return
	}
	jsonOut(w, 202, map[string]any{"id": id, "status": "CANCELING"})
}
func (x *MultiApp) jobList(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	x.mu.Lock()
	list := historicalJobs(filepath.Join(x.parent.data, "multi-jobs"), x.jobs, true, 100)
	x.mu.Unlock()
	jsonOut(w, 200, map[string]any{"jobs": list})
}
func (x *MultiApp) jobDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/multi/jobs/")
	cancelRequest := strings.HasSuffix(id, "/cancel")
	if cancelRequest {
		id = strings.TrimSuffix(id, "/cancel")
	}
	if (cancelRequest && r.Method != "POST") || (!cancelRequest && r.Method != "GET") {
		w.WriteHeader(405)
		return
	}
	if len(id) != 24 {
		fail(w, 404, errors.New("invalid job id"))
		return
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			fail(w, 404, errors.New("invalid job id"))
			return
		}
	}
	if cancelRequest {
		x.cancelJob(w, id)
		return
	}
	x.mu.Lock()
	j := x.jobs[id]
	var cp Job
	if j != nil {
		cp = *j
	}
	x.mu.Unlock()
	if j == nil {
		saved, e := readJournal(filepath.Join(x.parent.data, "multi-jobs"), id, true)
		if e != nil {
			fail(w, 404, errors.New("job not found"))
			return
		}
		cp = saved
	}
	result := map[string]any{"job": cp}
	if cp.Status == "PASS" {
		b, e := os.ReadFile(filepath.Join(x.parent.data, "multi-jobs", id, "run_summary.json"))
		if e == nil {
			var v any
			if json.Unmarshal(b, &v) == nil {
				result["summary"] = v
			}
		}
	}
	if r.URL.Query().Get("download") == "results" && cp.Status == "PASS" {
		http.ServeFile(w, r, filepath.Join(x.parent.data, "multi-jobs", id, "results.csv"))
		return
	}
	jsonOut(w, 200, result)
}

// A single-account factor editor. Each override is addressed by account,
// stage, allocation; the UI cannot alter arbitrary paths or delete templates.
func (x *MultiApp) factor(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		x.mu.Lock()
		defer x.mu.Unlock()
		dir, e := x.projectDir(r.URL.Query().Get("name"))
		if e != nil {
			fail(w, 404, e)
			return
		}
		account := r.URL.Query().Get("account")
		assignments, e := readAssignments(filepath.Join(dir, "assignments.csv"))
		if e != nil {
			fail(w, 422, e)
			return
		}
		template := ""
		for _, a := range assignments {
			if a["account"] == account {
				template = a["template"]
				break
			}
		}
		if template == "" {
			fail(w, 422, errors.New("未知科目"))
			return
		}
		if !multiSafe.MatchString(strings.TrimSuffix(template, ".json")) || !strings.HasSuffix(template, ".json") {
			fail(w, 422, errors.New("非法模板"))
			return
		}
		b, e := os.ReadFile(filepath.Join(dir, "templates", template))
		if e != nil {
			fail(w, 500, e)
			return
		}
		var rule any
		if e = json.Unmarshal(b, &rule); e != nil {
			fail(w, 500, e)
			return
		}
		overrides, e := factorRead(filepath.Join(dir, "overrides.csv"))
		if e != nil {
			fail(w, 422, e)
			return
		}
		current := []factorRow{}
		for _, v := range overrides {
			if v.Account == account {
				current = append(current, v)
			}
		}
		// Keep the three edit tokens consistent with the account the user saw.
		assignmentBytes, e := os.ReadFile(filepath.Join(dir, "assignments.csv"))
		if e != nil {
			fail(w, 500, e)
			return
		}
		overrideBytes, e := os.ReadFile(filepath.Join(dir, "overrides.csv"))
		if e != nil {
			fail(w, 500, e)
			return
		}
		jsonOut(w, 200, map[string]any{"account": account, "template": template, "rule": rule, "overrides": current,
			"versions": map[string]string{"assignments": sha256Hex(assignmentBytes), "template": sha256Hex(b), "overrides": sha256Hex(overrideBytes)},
		})
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Name     string      `json:"name"`
		Account  string      `json:"account"`
		Rows     []factorRow `json:"rows"`
		Versions struct {
			Assignments string `json:"assignments"`
			Template    string `json:"template"`
			Overrides   string `json:"overrides"`
		} `json:"versions"`
	}
	if e := multiRequest(w, r, &req, 128<<10); e != nil {
		fail(w, 400, e)
		return
	}
	if len(req.Rows) > 200 {
		fail(w, 422, errors.New("单科目最多200条覆盖"))
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.busy[req.Name] {
		fail(w, 409, errors.New("正在计算，不能修改因子"))
		return
	}
	dir, e := x.projectDir(req.Name)
	if e != nil {
		fail(w, 404, e)
		return
	}
	assignments, e := readAssignments(filepath.Join(dir, "assignments.csv"))
	if e != nil {
		fail(w, 422, e)
		return
	}
	template := ""
	for _, a := range assignments {
		if a["account"] == req.Account {
			template = a["template"]
			break
		}
	}
	if template == "" {
		fail(w, 422, errors.New("科目未分配模板"))
		return
	}
	raw, e := os.ReadFile(filepath.Join(dir, "templates", template))
	if e != nil {
		fail(w, 500, e)
		return
	}
	// Reject stale edits, including changed template assignments.
	assignmentBytes, e := os.ReadFile(filepath.Join(dir, "assignments.csv"))
	if e != nil {
		fail(w, 500, e)
		return
	}
	overrideBytes, e := os.ReadFile(filepath.Join(dir, "overrides.csv"))
	if e != nil {
		fail(w, 500, e)
		return
	}
	if req.Versions.Assignments == "" || req.Versions.Template == "" || req.Versions.Overrides == "" ||
		req.Versions.Assignments != sha256Hex(assignmentBytes) ||
		req.Versions.Template != sha256Hex(raw) ||
		req.Versions.Overrides != sha256Hex(overrideBytes) {
		fail(w, http.StatusConflict, errors.New("科目规则或因子配置已被修改，请刷新后重新核对，未保存本次修改"))
		return
	}
	var rule struct {
		Datasets struct {
			Drivers map[string]string `json:"drivers"`
		} `json:"datasets"`
		Stages []struct {
			ID          string `json:"id"`
			Allocations []struct {
				ID string `json:"id"`
			} `json:"allocations"`
		} `json:"stages"`
	}
	if e = json.Unmarshal(raw, &rule); e != nil {
		fail(w, 422, e)
		return
	}
	seen := map[string]bool{}
	for _, row := range req.Rows {
		if row.Account != req.Account || row.Stage == "" || row.Allocation == "" {
			fail(w, 422, errors.New("科目/阶段/规则未填写"))
			return
		}
		id := row.Stage + "\x00" + row.Allocation
		if seen[id] {
			fail(w, 422, errors.New("重复因子覆盖"))
			return
		}
		seen[id] = true
		valid := false
		for _, st := range rule.Stages {
			if st.ID == row.Stage {
				for _, a := range st.Allocations {
					if a.ID == row.Allocation {
						valid = true
					}
				}
			}
		}
		if !valid {
			fail(w, 422, errors.New("找不到目标分摊因子"))
			return
		}
		if row.DriverDataset != "" {
			if _, exists := rule.Datasets.Drivers[row.DriverDataset]; !exists {
				fail(w, 422, errors.New("Driver 数据集未在模板中定义"))
				return
			}
		}
		if row.Share != "" {
			if !regexp.MustCompile(`^(0(?:\.\d{1,6})?|1(?:\.0{1,6})?)$`).MatchString(row.Share) {
				fail(w, 422, errors.New("share 只允许0到1，最多6位小数"))
				return
			}
		}
		if row.WeightField != "" && !multiSafe.MatchString(row.WeightField) {
			fail(w, 422, errors.New("weight_field 必须是英文列名"))
			return
		}
	}
	// Validate the entire effective rule, not merely individual fractions.
	if e := validateEffectiveFactorPlan(raw, req.Rows); e != nil {
		fail(w, 422, e)
		return
	}
	path := filepath.Join(dir, "overrides.csv")
	old, e := factorRead(path)
	if e != nil {
		fail(w, 422, e)
		return
	}
	fresh := []factorRow{}
	for _, v := range old {
		if v.Account != req.Account {
			fresh = append(fresh, v)
		}
	}
	fresh = append(fresh, req.Rows...)
	sort.Slice(fresh, func(i, j int) bool {
		a, b := fresh[i], fresh[j]
		return a.Account+"\x00"+a.Stage+"\x00"+a.Allocation < b.Account+"\x00"+b.Stage+"\x00"+b.Allocation
	})
	tmp, e := os.CreateTemp(dir, ".override-")
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer os.Remove(tmp.Name())
	cw := csv.NewWriter(tmp)
	_ = cw.Write([]string{"account", "stage", "allocation", "driver_dataset", "weight_field", "share"})
	for _, v := range fresh {
		_ = cw.Write([]string{v.Account, v.Stage, v.Allocation, v.DriverDataset, v.WeightField, v.Share})
	}
	cw.Flush()
	if e = cw.Error(); e == nil {
		e = tmp.Sync()
	}
	if er := tmp.Close(); e == nil {
		e = er
	}
	if e != nil {
		fail(w, 500, e)
		return
	}
	if e = os.Rename(tmp.Name(), path); e != nil {
		fail(w, 500, e)
		return
	}
	jsonOut(w, 200, map[string]any{"saved": len(req.Rows)})
}

type factorRow struct {
	Account       string `json:"account"`
	Stage         string `json:"stage"`
	Allocation    string `json:"allocation"`
	DriverDataset string `json:"driver_dataset"`
	WeightField   string `json:"weight_field"`
	Share         string `json:"share"`
}

func factorRead(path string) ([]factorRow, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	r := csv.NewReader(f)
	all, e := r.ReadAll()
	if e != nil {
		return nil, e
	}
	if len(all) < 1 {
		return nil, errors.New("empty override file")
	}
	expected := []string{"account", "stage", "allocation", "driver_dataset", "weight_field", "share"}
	for i, v := range expected {
		if i >= len(all[0]) || strings.TrimPrefix(all[0][i], "\ufeff") != v {
			return nil, errors.New("invalid overrides.csv header")
		}
	}
	out := []factorRow{}
	for _, v := range all[1:] {
		if len(v) != 6 {
			return nil, errors.New("bad override row")
		}
		out = append(out, factorRow{v[0], v[1], v[2], v[3], v[4], v[5]})
	}
	return out, nil
}
