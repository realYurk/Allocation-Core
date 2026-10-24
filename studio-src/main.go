package main

// Allocation Studio is an independent local inspection and launch UI.
// It never reimplements or changes financial allocation calculations.

import (
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
}
type Job struct {
	ID     string `json:"id"`
	Case   string `json:"case"`
	Status string `json:"status"`
	Start  string `json:"start"`
	End    string `json:"end,omitempty"`
	Error  string `json:"error,omitempty"`
	Output string `json:"-"`
	Log    string `json:"log,omitempty"`
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
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Kind         string `json:"kind"`
	Stages       int    `json:"stages"`
	Drivers      int    `json:"drivers"`
	AccountCount int    `json:"accountCount"`
	Period       string `json:"period"`
	Amount       string `json:"amount"`
}

func main() {
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
			data = filepath.Join(os.Getenv("LOCALAPPDATA"), "AllocationStudioData")
		} else if h, e := os.UserHomeDir(); e == nil {
			data = filepath.Join(h, "AllocationStudioData")
		} else {
			log.Fatal(e)
		}
	}
	data, err = filepath.Abs(data)
	if err != nil {
		log.Fatal(err)
	}
	app := &Application{home: home, data: data, custom: filepath.Join(data, "cases"), output: filepath.Join(data, "jobs"), addr: *listen, jobs: map[string]*Job{}, sem: make(chan struct{}, 2)}
	for _, d := range []string{app.custom, app.output} {
		if err = os.MkdirAll(d, 0700); err != nil {
			log.Fatal(err)
		}
	}
	program := "alloc_" + map[string]string{"windows": "windows", "linux": "linux", "darwin": "darwin"}[runtime.GOOS] + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		program += ".exe"
	}
	app.engine = filepath.Join(home, "core", program)
	if _, err = os.Stat(app.engine); err != nil {
		log.Fatal("missing authoritative engine: ", app.engine)
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
			if r.Header.Get("Content-Type") != "application/json" {
				http.Error(w, "application/json required", 415)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
	log.Printf("Allocation Studio → http://%s (data: %s)", *listen, data)
	if !*noBrowser {
		go func() { time.Sleep(650 * time.Millisecond); openBrowser("http://" + *listen + "/designer") }()
	}
	srv := &http.Server{Addr: *listen, Handler: secure, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8192}
	log.Fatal(srv.ListenAndServe())
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
	jsonOut(w, 200, map[string]any{"status": "ok", "engine": "separate-authoritative-binary", "data_dir": a.data, "user_cases": a.custom, "platform": runtime.GOOS + "/" + runtime.GOARCH})
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
		base = filepath.Join(a.home, "examples", id)
		kind = "example"
	}
	baseAbs, e := filepath.Abs(base)
	if e != nil {
		return "", "", e
	}
	root := filepath.Join(a.home, "examples")
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
	ids := caseIds(filepath.Join(a.home, "examples"), "")
	return append(ids, caseIds(a.custom, "user_")...)
}
func (a *Application) sourceRows(rule GenericRule, dir string) ([]map[string]string, error) {
	rows := []map[string]string{}
	names := make([]string, 0, len(rule.Datasets.Amounts))
	for n := range rule.Datasets.Amounts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		r, e := readCSV(filepath.Join(dir, rule.Datasets.Amounts[name]), 500)
		if e != nil {
			return nil, e
		}
		for _, item := range r {
			item["_dataset"] = name
			rows = append(rows, item)
		}
	}
	return rows, nil
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
		rows, _ := a.sourceRows(rule, dir)
		accounts := map[string]bool{}
		amount := 0.
		period := ""
		drivers := map[string]bool{}
		for _, row := range rows {
			accounts[row["account"]] = true
			v, _ := strconv.ParseFloat(row["amount"], 64)
			amount += v
			if period == "" {
				period = row["period"]
			}
		}
		for k := range rule.Datasets.Drivers {
			drivers[k] = true
		}
		out = append(out, CaseSummary{ID: id, Name: rule.Name, Type: kind, Kind: kind, Stages: len(rule.Stages), Drivers: len(drivers), AccountCount: len(accounts), Period: period, Amount: fmt.Sprintf("%.2f", amount)})
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
	src, e := a.sourceRows(rule, dir)
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
	jsonOut(w, 200, map[string]any{"id": id, "type": kind, "rule": rule, "sources": src, "drivers": drivers})
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
		out := []*Job{}
		seen := map[string]bool{}
		for _, v := range a.jobs {
			cp := *v
			cp.Log = ""
			seen[cp.ID] = true
			out = append(out, &cp)
		}
		a.mu.RUnlock()
		// Retain historical access after restarting the local Studio process.
		// The generated job IDs are strictly hex; filenames remain inside the jobs folder.
		entries, _ := os.ReadDir(a.output)
		for count, entry := range entries {
			if count >= 3000 {
				break
			}
			id := entry.Name()
			if !entry.IsDir() || len(id) != 24 || seen[id] {
				continue
			}
			if _, err := hex.DecodeString(id); err != nil {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(a.output, id, "studio_job.json"))
			if err != nil || len(raw) > 8192 {
				continue
			}
			var saved Job
			if json.Unmarshal(raw, &saved) != nil || saved.ID != id {
				continue
			}
			saved.Log = ""
			out = append(out, &saved)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Start > out[j].Start })
		if len(out) > 50 {
			out = out[:50]
		}
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
	_, dir, _, e := a.readCase(req.Case)
	_ = dir
	if e != nil {
		fail(w, 400, e)
		return
	}
	id := randomID()
	job := &Job{ID: id, Case: req.Case, Status: "QUEUED", Start: time.Now().Format(time.RFC3339), Output: filepath.Join(a.output, id)}
	a.mu.Lock()
	a.jobs[id] = job
	a.mu.Unlock()
	posted := *job
	go a.execute(job, req.Workers, req.Gzip)
	jsonOut(w, 202, &posted)
}
func (a *Application) execute(j *Job, workers int, gzip bool) {
	a.sem <- struct{}{}
	defer func() { <-a.sem }()
	a.mu.Lock()
	j.Status = "RUNNING"
	a.mu.Unlock()
	_, dir, _, e := a.readCase(j.Case)
	if e != nil {
		a.finish(j, "FAILED", e.Error(), "")
		return
	}
	// Preserve the exact rule used by this run for historical traceability.
	// The core never receives a mutable browser-side draft.
	ruleSnapshot, err := os.ReadFile(filepath.Join(dir, "rule.json"))
	if err != nil {
		a.finish(j, "FAILED", "cannot read rule snapshot", err.Error())
		return
	}
	// The authoritative engine creates a NEW output directory atomically.
	// Do not create j.Output here; doing so makes every run fail closed.
	args := []string{"run", filepath.Join(dir, "rule.json"), j.Output, "--workers", strconv.Itoa(workers)}
	if gzip {
		args = append(args, "--evidence-gzip")
	}
	cmd := exec.Command(a.engine, args...)
	cmd.Dir = a.home
	output, e := cmd.CombinedOutput()
	if len(output) > 8192 {
		output = output[:8192]
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
	var s struct {
		Status  string `json:"status"`
		RuleSHA string `json:"rule_sha256"`
	}
	if e = json.Unmarshal(raw, &s); e != nil || s.Status != "PASS" {
		a.finish(j, "FAILED", "run summary is not PASS", string(output))
		return
	}
	currentHash := sha256.Sum256(ruleSnapshot)
	if s.RuleSHA != "" && s.RuleSHA != hex.EncodeToString(currentHash[:]) {
		a.finish(j, "FAILED", "rule changed while executing; provenance mismatch", string(output))
		return
	}
	if e = os.WriteFile(filepath.Join(j.Output, "studio_rule_snapshot.json"), ruleSnapshot, 0600); e != nil {
		a.finish(j, "FAILED", "could not preserve rule for history", e.Error())
		return
	}
	a.finish(j, "PASS", "", string(output))
}
func (a *Application) finish(j *Job, status, msg, logText string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	j.Status = status
	if status == "FAILED" {
		_ = os.MkdirAll(j.Output, 0700)
	}
	j.Error = msg
	j.Log = logText
	j.End = time.Now().Format(time.RFC3339)
	_ = os.WriteFile(filepath.Join(j.Output, "studio_job.json"), mustJSON(j), 0600)
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
	if r.Method != "GET" {
		http.Error(w, "method", 405)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/jobs/"), "/")
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
			build := exec.Command(a.engine, "trace-pack-index", j.Output)
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
	cmd := exec.Command(a.engine, "trace", ruleForTrace, j.Output, req.RowID)
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
	cmd := exec.Command(a.engine, "doctor", filepath.Join(dir, "rule.json"))
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
