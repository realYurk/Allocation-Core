package studio

// Allocation Studio is an independent local inspection and launch UI.
// It never reimplements or changes financial allocation calculations.

import (
	"allocationcore/internal/demo"
	"allocationcore/internal/engine"
	"allocationcore/internal/multi"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed public/*
var publicFS embed.FS

type Application struct {
	home, data, custom, output, engine, addr string
	mu                                       sync.RWMutex
	jobs                                     map[string]*Job
	sem                                      chan struct{}
	slots                                    chan struct{}
	cancels                                  map[string]context.CancelFunc
	editMu                                   sync.Mutex
}
type Job struct {
	ID        string                  `json:"id"`
	Case      string                  `json:"case"`
	Status    string                  `json:"status"`
	Start     string                  `json:"start"`
	End       string                  `json:"end,omitempty"`
	Error     string                  `json:"error,omitempty"`
	Output    string                  `json:"-"`
	Log       string                  `json:"log,omitempty"`
	Diagnosis *multi.FailureDiagnosis `json:"diagnosis,omitempty"`
}
type GenericRule struct {
	Name       string   `json:"name"`
	Dimensions []string `json:"dimensions"`
	Datasets   struct {
		Amounts map[string]string `json:"amounts"`
		Drivers map[string]string `json:"drivers"`
	} `json:"datasets"`
	Stages    []map[string]any `json:"stages"`
	Precision int              `json:"precision"`
}
type CaseSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	Kind            string `json:"kind"`
	Stages          int    `json:"stages"`
	Drivers         int    `json:"drivers"`
	AccountCount    int    `json:"accountCount"`
	Period          string `json:"period"`
	Amount          string `json:"amount"`
	SourceRows      int64  `json:"source_rows"`
	SummaryComplete bool   `json:"summary_complete"`
	SummaryError    string `json:"summary_error,omitempty"`
}

// Only these local-only business import endpoints accept multipart file data.
// The UI host/origin guard remains mandatory for every upload request.
func studioMultipartUpload(r *http.Request) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
		return false
	}
	switch r.URL.Path {
	case "/api/multi/onboard", "/api/multi/driver-import", "/api/multi/reference-upload":
		return true
	default:
		return false
	}
}

func Run() {
	rootFlag := flag.String("root", "", "installation root containing core/ and examples/")
	dataFlag := flag.String("data-dir", "", "persistent user data directory")
	listen := flag.String("listen", "127.0.0.1:8866", "local-only HTTP listener")
	noBrowser := flag.Bool("no-browser", false, "do not launch default browser")
	flag.Parse()
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		log.Fatal("only loopback addresses are supported")
	}
	exe, _ := os.Executable()
	home := filepath.Dir(exe)
	if *rootFlag != "" {
		home = *rootFlag
	}
	home, err = filepath.Abs(home)
	if err != nil {
		log.Fatal(err)
	}
	data := *dataFlag
	if data == "" {
		if runtime.GOOS == "windows" && os.Getenv("LOCALAPPDATA") != "" {
			data = filepath.Join(os.Getenv("LOCALAPPDATA"), "AllocationCoreCleanData")
		} else if h, e := os.UserHomeDir(); e == nil {
			data = filepath.Join(h, "AllocationCoreCleanData")
		} else {
			log.Fatal(e)
		}
	}
	data, err = filepath.Abs(data)
	if err != nil {
		log.Fatal(err)
	}
	app := &Application{home: home, data: data, custom: filepath.Join(data, "cases"), output: filepath.Join(data, "jobs"), addr: *listen, jobs: map[string]*Job{}, sem: make(chan struct{}, 2), slots: make(chan struct{}, 8), cancels: map[string]context.CancelFunc{}}
	for _, d := range []string{app.custom, app.output} {
		if err = os.MkdirAll(d, 0700); err != nil {
			log.Fatal(err)
		}
	}
	if err = prepareDemo(home, data); err != nil {
		log.Printf("Optional synthetic demo unavailable: %v", err)
	}
	app.jobs, err = recoverJournals(app.output, false)
	if err != nil {
		log.Fatal("legacy job recovery: ", err)
	}
	// All long-running cancellable workers use this same executable.
	app.engine, err = os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	assets, _ := fs.Sub(publicFS, "public")
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("/", app.index)
	mux.HandleFunc("/api/health", app.health)
	mux.HandleFunc("/api/cases", app.cases)
	mux.HandleFunc("/api/cases/", app.caseDetail)
	mux.HandleFunc("/api/jobs", app.jobsEndpoint)
	mux.HandleFunc("/api/jobs/", app.jobDetail)
	mux.HandleFunc("/api/trace", app.trace)
	mux.HandleFunc("/api/validate", app.validate)
	app.registerDesigner(mux)
	app.registerMulti(mux)
	mux.HandleFunc("/workspace", app.workspace)
	mux.HandleFunc("/rules", app.ruleKitPage)
	explorerProxy, stopExplorer, explorerErr := startExplorerCompanion(home, data)
	if explorerErr != nil {
		log.Printf("Explorer companion unavailable / 数据对比组件不可用: %v", explorerErr)
		mux.HandleFunc("/explorer/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Explorer companion not installed / 未安装对比程序", 503)
		})
	} else {
		defer stopExplorer()
		mux.Handle("/explorer/", explorerProxy)
	}
	secure := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject DNS rebinding and cross-site POSTs. No CORS is granted.
		hh, _, e := net.SplitHostPort(r.Host)
		if e != nil {
			hh = r.Host
		}
		if hh != "127.0.0.1" && hh != "localhost" && hh != "[::1]" && hh != "::1" {
			http.Error(w, "invalid Host", 403)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			if origin != "" && origin != "http://"+r.Host {
				http.Error(w, "cross-origin request rejected", 403)
				return
			}
			if r.Header.Get("Content-Type") != "application/json" && !explorerUpload(r) && !studioMultipartUpload(r) {
				http.Error(w, "application/json required", 415)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
	log.Printf("Allocation Studio → http://%s (data: %s)", *listen, data)
	srv := &http.Server{Addr: *listen, Handler: secure, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8192}
	listener, listenErr := net.Listen("tcp", *listen)
	if listenErr != nil && *listen == "127.0.0.1:8866" {
		for port := 8867; port <= 8885; port++ {
			listener, listenErr = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if listenErr == nil {
				break
			}
		}
	}
	if listenErr != nil {
		log.Fatalf("LISTEN_FAILED %s: %v", *listen, listenErr)
	}
	actual := "http://" + listener.Addr().String()
	log.Printf("READY %s/workspace", actual)
	if !*noBrowser {
		go func() {
			client := &http.Client{Timeout: 500 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
			for attempt := 0; attempt < 100; attempt++ {
				resp, err := client.Get(actual + "/api/health")
				if err == nil {
					io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
					resp.Body.Close()
					if resp.StatusCode == 200 {
						openBrowser(actual + "/workspace")
						return
					}
				}
				time.Sleep(70 * time.Millisecond)
			}
			log.Printf("STARTUP_CHECK_FAILED browser not opened; inspect logs")
		}()
	}
	if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Studio failed to start: %v", err)
	}
}
func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		_ = exec.Command("open", url).Start()
	default:
		_ = exec.Command("xdg-open", url).Start()
	}
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, e error) {
	jsonOut(w, status, map[string]string{"error": e.Error()})
}
func (a *Application) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := publicFS.ReadFile("public/index.html")
	if err != nil {
		http.Error(w, "UI unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (a *Application) health(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, map[string]any{"status": "ok", "engine": "bundled-core", "data_dir": a.data, "user_cases": a.custom, "platform": runtime.GOOS + "/" + runtime.GOARCH})
}
func (a *Application) locate(id string) (string, string, error) {
	if id == "" || len(id) > 90 || strings.ContainsAny(id, "/\\ .") || strings.Contains(id, "..") {
		return "", "", errors.New("invalid case ID")
	}
	var base string
	var kind string
	if strings.HasPrefix(id, "user_") {
		base = filepath.Join(a.custom, strings.TrimPrefix(id, "user_"))
		kind = "custom"
	} else {
		base = filepath.Join(a.data, "examples", id)
		kind = "example"
	}
	baseAbs, e := filepath.Abs(base)
	if e != nil {
		return "", "", e
	}
	root := filepath.Join(a.data, "examples")
	if kind == "custom" {
		root = a.custom
	}
	rootAbs, e := filepath.Abs(root)
	if e != nil {
		return "", "", e
	}
	rel, e := filepath.Rel(rootAbs, baseAbs)
	if e != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", "", errors.New("unsafe case path")
	}
	fi, e := os.Lstat(baseAbs)
	if e != nil {
		return "", "", e
	}
	if !fi.IsDir() || (fi.Mode()&os.ModeSymlink) != 0 {
		return "", "", errors.New("case must be a normal directory")
	}
	return baseAbs, kind, nil
}
func (a *Application) readCase(id string) (GenericRule, string, string, error) {
	var rule GenericRule
	dir, kind, e := a.locate(id)
	if e != nil {
		return rule, "", "", e
	}
	// Normalize both ends of containment checks (Windows 8.3 and junctions).
	if resolvedDir, e := filepath.EvalSymlinks(dir); e == nil {
		dir = resolvedDir
	}
	rulePath := filepath.Join(dir, "rule.json")
	ruleInfo, pathErr := os.Lstat(rulePath)
	if pathErr != nil || !ruleInfo.Mode().IsRegular() {
		return rule, "", "", errors.New("rule.json must be a regular file")
	}
	data, e := os.ReadFile(rulePath)
	if e != nil {
		return rule, "", "", e
	}
	if e = json.Unmarshal(data, &rule); e != nil {
		return rule, "", "", e
	}
	if len(rule.Stages) == 0 {
		return rule, "", "", errors.New("rule has no stages")
	}
	for _, m := range []map[string]string{rule.Datasets.Amounts, rule.Datasets.Drivers} {
		for _, relative := range m {
			if relative == "" || filepath.IsAbs(relative) || strings.Contains(relative, "\\") {
				return rule, "", "", errors.New("absolute/backslash dataset rejected")
			}
			f := filepath.Join(dir, relative)
			resolved, e := filepath.EvalSymlinks(f)
			if e != nil {
				return rule, "", "", e
			}
			rel, e := filepath.Rel(dir, resolved)
			if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return rule, "", "", errors.New("dataset escapes case folder")
			}
			stat, e := os.Stat(resolved)
			if e != nil || !stat.Mode().IsRegular() {
				return rule, "", "", errors.New("dataset must be file")
			}
		}
	}
	return rule, dir, kind, nil
}
func caseIds(dir string, prefix string) []string {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return nil
	}
	ids := []string{}
	for _, ent := range entries {
		if ent.IsDir() && !strings.HasPrefix(ent.Name(), ".") {
			ids = append(ids, prefix+ent.Name())
		}
	}
	sort.Strings(ids)
	return ids
}
func (a *Application) allCaseIDs() []string {
	ids := caseIds(filepath.Join(a.data, "examples"), "")
	return append(ids, caseIds(a.custom, "user_")...)
}

// sourceRows is intentionally a PREVIEW for the legacy Studio case explorer.
// Never use these rows to calculate complete financial summaries.
func (a *Application) sourceRows(rule GenericRule, dir string) ([]map[string]string, bool, error) {
	rows := []map[string]string{}
	truncated := false
	names := make([]string, 0, len(rule.Datasets.Amounts))
	for n := range rule.Datasets.Amounts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		r, e := readCSV(filepath.Join(dir, rule.Datasets.Amounts[name]), 501)
		if e != nil {
			return nil, false, e
		}
		if len(r) > 500 {
			truncated = true
			r = r[:500]
		}
		for _, item := range r {
			item["_dataset"] = name
			rows = append(rows, item)
		}
	}
	return rows, truncated, nil
}

// summarizeCaseAmounts streams ALL rows using the very same decimal parser
// and dimension validation as the allocation engine. It never substitutes
// the preview's first 500 rows for a financial total.
func summarizeCaseAmounts(rule GenericRule, dir string) (int, string, string, int64, error) {
	accounts := make(map[string]struct{})
	var total int64
	var sourceRows int64
	period := ""
	names := make([]string, 0, len(rule.Datasets.Amounts))
	for n := range rule.Datasets.Amounts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, rule.Datasets.Amounts[name])
		err := engine.StreamAmounts(path, rule.Dimensions, rule.Precision, func(row engine.Row) error {
			if row.Amount > 0 && total > math.MaxInt64-row.Amount || row.Amount < 0 && total < math.MinInt64-row.Amount {
				return fmt.Errorf("amount sum overflows signed 64-bit precision")
			}
			total += row.Amount
			sourceRows++
			if ac := row.Coord["account"]; ac != "" {
				accounts[ac] = struct{}{}
			}
			if period == "" {
				period = row.Coord["period"]
			}
			return nil
		})
		if err != nil {
			return 0, "", "", 0, fmt.Errorf("amount dataset %s: %w", name, err)
		}
	}
	if rule.Precision < 0 || rule.Precision > 6 {
		return 0, "", "", 0, fmt.Errorf("unsupported financial precision %d", rule.Precision)
	}
	return len(accounts), period, engine.Money(total, rule.Precision), sourceRows, nil
}

func readCSV(path string, limit int) ([]map[string]string, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	h, e := reader.Read()
	if e != nil {
		return nil, e
	}
	for i := range h {
		h[i] = strings.TrimPrefix(h[i], "\ufeff")
	}
	result := make([]map[string]string, 0)
	for len(result) < limit {
		r, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		record := map[string]string{}
		for i, k := range h {
			if i < len(r) {
				record[k] = r[i]
			}
		}
		result = append(result, record)
	}
	return result, nil
}
func (a *Application) cases(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method", 405)
		return
	}
	out := []CaseSummary{}
	for _, id := range a.allCaseIDs() {
		rule, dir, kind, e := a.readCase(id)
		if e != nil {
			continue
		}
		accountCount, period, amount, rows, err := summarizeCaseAmounts(rule, dir)
		info := CaseSummary{ID: id, Name: rule.Name, Type: kind, Kind: kind, Stages: len(rule.Stages),
			Drivers: len(rule.Datasets.Drivers), AccountCount: accountCount, Period: period, Amount: amount,
			SourceRows: rows, SummaryComplete: err == nil}
		if err != nil {
			// A partial total MUST NOT look like a complete financial total.
			info.SummaryError = err.Error()
		}
		out = append(out, info)
	}
	jsonOut(w, 200, map[string]any{"cases": out})
}
func (a *Application) caseDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method", 405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/cases/")
	rule, dir, kind, e := a.readCase(id)
	if e != nil {
		fail(w, 404, e)
		return
	}
	src, sourceTruncated, e := a.sourceRows(rule, dir)
	if e != nil {
		fail(w, 422, e)
		return
	}
	drivers := map[string][]map[string]string{}
	for k, v := range rule.Datasets.Drivers {
		data, e := readCSV(filepath.Join(dir, v), 500)
		if e == nil {
			drivers[k] = data
		}
	}
	jsonOut(w, 200, map[string]any{"id": id, "type": kind, "rule": rule, "sources": src, "sources_truncated": sourceTruncated, "sources_preview_limit_per_dataset": 500, "drivers": drivers})
}
func randomID() string {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		return fmt.Sprintf("fallback%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
func (a *Application) jobsEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		a.mu.RLock()
		out := historicalJobs(a.output, a.jobs, false, 50)
		a.mu.RUnlock()
		jsonOut(w, 200, map[string]any{"jobs": out})
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		Case    string `json:"case"`
		Workers int    `json:"workers"`
		Gzip    bool   `json:"gzip"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if e := dec.Decode(&req); e != nil {
		fail(w, 400, e)
		return
	}
	if req.Workers == 0 {
		req.Workers = 2
	}
	if req.Workers < 1 || req.Workers > 8 {
		fail(w, 400, errors.New("workers must be between 1 and 8"))
		return
	}
	a.editMu.Lock()
	defer a.editMu.Unlock()
	_, dir, _, e := a.readCase(req.Case)
	_ = dir
	if e != nil {
		fail(w, 400, e)
		return
	}
	select {
	case a.slots <- struct{}{}:
	default:
		fail(w, 429, errors.New("Studio任务队列已满（最多8个待执行/执行中任务）"))
		return
	}
	id := randomID()
	ctx, cancel := context.WithCancel(context.Background())
	job := &Job{ID: id, Case: req.Case, Status: "QUEUED", Start: time.Now().Format(time.RFC3339), Output: filepath.Join(a.output, id)}
	if e := atomicJobUpdate(job, false); e != nil {
		cancel()
		<-a.slots
		fail(w, 500, fmt.Errorf("cannot persist queued job: %w", e))
		return
	}
	a.mu.Lock()
	a.jobs[id] = job
	a.cancels[id] = cancel
	a.mu.Unlock()
	posted := *job
	go a.execute(ctx, job, req.Workers, req.Gzip)
	jsonOut(w, 202, &posted)
}
func (a *Application) execute(ctx context.Context, j *Job, workers int, gzip bool) {
	defer func() { <-a.slots }()
	select {
	case a.sem <- struct{}{}:
		defer func() { <-a.sem }()
	case <-ctx.Done():
		a.finish(j, "CANCELED", "queued job canceled", "")
		return
	}
	if ctx.Err() != nil {
		a.finish(j, "CANCELED", "queued job canceled", "")
		return
	}
	a.mu.Lock()
	j.Status = "RUNNING"
	saveErr := atomicJobUpdate(j, false)
	a.mu.Unlock()
	if saveErr != nil {
		a.finish(j, "FAILED", "cannot persist RUNNING: "+saveErr.Error(), "")
		return
	}
	_, dir, _, err := a.readCase(j.Case)
	if err != nil {
		a.finish(j, "FAILED", err.Error(), "")
		return
	}
	// Copy all referenced inputs and the exact rule to one immutable private
	// directory before passing them to Core. No mutable project files are read
	// by the financial engine. This incurs temporary disk usage intentionally.
	snapshot, ruleSnapshot, inputManifest, err := snapshotLegacyCase(ctx, dir, a.output)
	if err != nil {
		if ctx.Err() != nil {
			a.finish(j, "CANCELED", ctx.Err().Error(), "")
		} else {
			a.finish(j, "FAILED", "input snapshot failed", err.Error())
		}
		return
	}
	defer os.RemoveAll(snapshot)
	args := []string{"run", filepath.Join(snapshot, "rule.json"), j.Output, "--workers", strconv.Itoa(workers)}
	// Legacy Studio now exposes Core's safe disk mode automatically for large
	// projects, while smaller demos retain their original in-memory behavior.
	var totalSize int64
	for _, item := range inputManifest {
		totalSize += item.Bytes
	}
	if totalSize >= 128*1024*1024 {
		args = append(args, "--disk-partitions", "8", "--driver-disk-index", "--driver-cache-dir", filepath.Join(snapshot, "driver-cache"))
	}
	if gzip {
		args = append(args, "--evidence-gzip")
	}
	cmd := exec.CommandContext(ctx, a.engine, append([]string{"core"}, args...)...)
	cmd.Dir = a.home
	output, e := cmd.CombinedOutput()
	if len(output) > 8192 {
		output = output[:8192]
	}
	if ctx.Err() != nil {
		a.finish(j, "CANCELED", ctx.Err().Error(), string(output))
		return
	}
	if e != nil {
		a.finish(j, "FAILED", e.Error(), string(output))
		return
	}
	raw, e := os.ReadFile(filepath.Join(j.Output, "run_summary.json"))
	if e != nil {
		a.finish(j, "FAILED", "no run summary", string(output))
		return
	}
	var summary struct {
		Status  string `json:"status"`
		RuleSHA string `json:"rule_sha256"`
	}
	if e = json.Unmarshal(raw, &summary); e != nil || summary.Status != "PASS" {
		a.finish(j, "FAILED", "run summary is not PASS", string(output))
		return
	}
	currentHash := sha256.Sum256(ruleSnapshot)
	if summary.RuleSHA != "" && summary.RuleSHA != hex.EncodeToString(currentHash[:]) {
		a.finish(j, "FAILED", "rule provenance mismatch", string(output))
		return
	}
	if e = os.WriteFile(filepath.Join(j.Output, "studio_rule_snapshot.json"), ruleSnapshot, 0600); e != nil {
		a.finish(j, "FAILED", "cannot preserve rule snapshot", e.Error())
		return
	}
	if e = os.WriteFile(filepath.Join(j.Output, "studio_input_manifest.json"), mustJSON(inputManifest), 0600); e != nil {
		a.finish(j, "FAILED", "cannot preserve input manifest", e.Error())
		return
	}
	a.finish(j, "PASS", "", string(output))
}
func (a *Application) finish(j *Job, status, msg, logText string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	j.Status = status
	if a.cancels != nil {
		delete(a.cancels, j.ID)
	}
	if status != "PASS" {
		_ = os.MkdirAll(j.Output, 0700)
	}
	j.Error = msg
	j.Log = logText
	j.End = time.Now().Format(time.RFC3339)
	if err := atomicJobUpdate(j, false); err != nil {
		log.Printf("ERROR: legacy journal update %s: %v", j.ID, err)
		j.Status = "FAILED"
		j.Error = "job journal persistence failed: " + err.Error()
		_ = atomicJobUpdate(j, false)
	}
	// Compatibility with VNext3 batch artifacts; never create the result folder
	// until the external engine has finished and published its own output.
	if _, err := os.Stat(j.Output); err == nil {
		if err := os.WriteFile(filepath.Join(j.Output, "studio_job.json"), mustJSON(j), 0600); err != nil && j.Status == "PASS" {
			j.Status = "FAILED"
			j.Error = "cannot preserve result job provenance: " + err.Error()
			_ = atomicJobUpdate(j, false)
		}
	}
}
func mustJSON(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return b }
func (a *Application) getJob(id string) (*Job, error) {
	if len(id) != 24 {
		return nil, errors.New("invalid job ID")
	}
	_, err := hex.DecodeString(id)
	if err != nil {
		return nil, err
	}
	a.mu.RLock()
	j := a.jobs[id]
	if j != nil {
		copyOfJob := *j
		a.mu.RUnlock()
		return &copyOfJob, nil
	}
	a.mu.RUnlock()
	out := filepath.Join(a.output, id)
	b, e := os.ReadFile(filepath.Join(out, "studio_job.json"))
	if e != nil {
		return nil, errors.New("job not found")
	}
	var old Job
	if e = json.Unmarshal(b, &old); e != nil {
		return nil, e
	}
	old.Output = out
	return &old, nil
}
func (a *Application) jobDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/jobs/"), "/")
	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		a.mu.Lock()
		j := a.jobs[parts[0]]
		if j == nil || (j.Status != "QUEUED" && j.Status != "RUNNING" && j.Status != "CANCELING") {
			a.mu.Unlock()
			fail(w, 409, errors.New("任务不存在或已经结束"))
			return
		}
		if cancel, ok := a.cancels[parts[0]]; ok {
			cancel()
		}
		j.Status = "CANCELING"
		persistErr := atomicJobUpdate(j, false)
		a.mu.Unlock()
		if persistErr != nil {
			fail(w, 500, persistErr)
			return
		}
		jsonOut(w, 202, map[string]string{"id": parts[0], "status": "CANCELING"})
		return
	}
	if r.Method != "GET" {
		http.Error(w, "method", 405)
		return
	}
	j, e := a.getJob(parts[0])
	if e != nil {
		fail(w, 404, e)
		return
	}
	if len(parts) == 1 {
		out := map[string]any{"job": j}
		if j.Status == "PASS" {
			out["summary"] = readJSONFile(filepath.Join(j.Output, "run_summary.json"))
			results, _ := readCSV(filepath.Join(j.Output, "results.csv"), 300)
			out["results"] = results
			recon, _ := readCSV(filepath.Join(j.Output, "reconciliation.csv"), 300)
			out["reconciliation"] = recon
			out["files"] = listOutputFiles(j.Output)
		}
		jsonOut(w, 200, out)
		return
	}
	if len(parts) != 3 || parts[1] != "download" || j.Status != "PASS" {
		http.NotFound(w, r)
		return
	}
	allowed := map[string]bool{"results.csv": true, "reconciliation.csv": true, "run_summary.json": true, "ledger.csv": true, "ledger.csv.gz": true, "lineage_links.csv": true, "lineage_links.csv.gz": true, "source_to_pool.csv": true, "source_to_pool.csv.gz": true, "evidence_stream_manifest.json": true}
	name := parts[2]
	if !allowed[name] {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeFile(w, r, filepath.Join(j.Output, name))
}
func listOutputFiles(dir string) []string {
	out := []string{}
	ent, _ := os.ReadDir(dir)
	for _, e := range ent {
		if !e.IsDir() && e.Name() != "studio_job.json" && e.Name() != "studio_rule_snapshot.json" {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
func readJSONFile(path string) any {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil
	}
	var obj any
	if json.Unmarshal(b, &obj) != nil {
		return nil
	}
	return obj
}

var traceIndexMu sync.Mutex

func (a *Application) trace(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	var req struct {
		Job   string `json:"job"`
		RowID string `json:"row_id"`
	}
	if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
		fail(w, 400, e)
		return
	}
	if len(req.RowID) == 0 || len(req.RowID) > 256 {
		fail(w, 400, errors.New("invalid row ID"))
		return
	}
	j, e := a.getJob(req.Job)
	if e != nil || j.Status != "PASS" {
		fail(w, 404, errors.New("completed job not found"))
		return
	}
	_, dir, _, e := a.readCase(j.Case)
	if e != nil {
		fail(w, 404, e)
		return
	}
	// Compressed evidence needs a packed index before a direct trace.
	// This creates a disposable derived index, never changes ledger or amounts.
	if _, exists := os.Stat(filepath.Join(j.Output, "ledger.csv.gz")); exists == nil {
		traceIndexMu.Lock()
		if _, indexed := os.Stat(filepath.Join(j.Output, "trace_packed_v19")); indexed != nil {
			build := exec.Command(a.engine, "core", "trace-pack-index", j.Output)
			if buf, buildErr := build.CombinedOutput(); buildErr != nil {
				traceIndexMu.Unlock()
				fail(w, 422, fmt.Errorf("cannot build trace index: %s", string(buf)))
				return
			}
		}
		traceIndexMu.Unlock()
	}
	ruleForTrace := filepath.Join(j.Output, "studio_rule_snapshot.json")
	if _, err := os.Stat(ruleForTrace); err != nil {
		ruleForTrace = filepath.Join(dir, "rule.json")
	}
	cmd := exec.Command(a.engine, "core", "trace", ruleForTrace, j.Output, req.RowID)
	buf, e := cmd.CombinedOutput()
	if e != nil {
		if len(buf) > 1500 {
			buf = buf[:1500]
		}
		fail(w, 422, fmt.Errorf("trace failed: %s", strings.TrimSpace(string(buf))))
		return
	}
	var parsed any
	if json.Unmarshal(buf, &parsed) == nil {
		jsonOut(w, 200, map[string]any{"trace": parsed})
	} else {
		jsonOut(w, 200, map[string]any{"trace_text": string(buf)})
	}
}
func (a *Application) validate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method", 405)
		return
	}
	var req struct {
		Case string `json:"case"`
	}
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); e != nil {
		fail(w, 400, e)
		return
	}
	_, dir, _, e := a.readCase(req.Case)
	if e != nil {
		fail(w, 400, e)
		return
	}
	cmd := exec.Command(a.engine, "core", "doctor", filepath.Join(dir, "rule.json"))
	buf, e := cmd.CombinedOutput()
	if len(buf) > 12000 {
		buf = buf[:12000]
	}
	if e != nil {
		jsonOut(w, 422, map[string]any{"status": "FAILED", "detail": string(buf)})
		return
	}
	jsonOut(w, 200, map[string]any{"status": "PASS", "detail": string(buf)})
}

// Constant-time access token helpers are intentionally absent: this binds to loopback only,
// with Host and Origin checks. It is not a multi-user service.

// prepareDemo copies only public, synthetic rule descriptions into the private
// user workspace and generates synthetic data there. Installation is read-only.
func prepareDemo(home, data string) error {
	for _, sub := range []string{"examples", filepath.Join("testdata", "multi_sample")} {
		source := filepath.Join(home, sub)
		if e := filepath.Walk(source, func(path string, info os.FileInfo, e error) error {
			if e != nil {
				return e
			}
			rel, err := filepath.Rel(home, path)
			if err != nil {
				return err
			}
			target := filepath.Join(data, rel)
			if info.IsDir() {
				return os.MkdirAll(target, 0700)
			}
			if !info.Mode().IsRegular() || !strings.HasSuffix(info.Name(), ".json") {
				return nil
			}
			if _, e := os.Stat(target); e == nil {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, b, 0600)
		}); e != nil {
			return e
		}
	}
	_, err := demo.Generate(data)
	return err
}
