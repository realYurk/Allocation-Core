package service

// Local HTTP integration adapter. The trusted rules/data root is immutable;
// callers can only address files inside it. Financial runs are isolated in
// unique output directories and use the same authoritative engine as CLI.

import (
	"allocation-core/internal/engine"
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Root             string
	Store            string
	Token            string
	Concurrent       int
	Queue            int
	MaxActiveWorkers int // upper bound of workers per service job, 1..16
}
type Request struct {
	Rule            string `json:"rule"`
	Workers         int    `json:"workers"`
	DiskPartitions  int    `json:"disk_partitions"`
	DriverDiskIndex bool   `json:"driver_disk_index"`
	DriverCache     bool   `json:"driver_cache"`
	EvidenceGzip    bool   `json:"evidence_gzip"`
}
type Job struct {
	ID          string            `json:"id"`
	Rule        string            `json:"rule"`
	Status      string            `json:"status"`
	Submitted   string            `json:"submitted_utc"`
	Started     string            `json:"started_utc,omitempty"`
	Finished    string            `json:"finished_utc,omitempty"`
	Error       string            `json:"error,omitempty"`
	Summary     *engine.Summary   `json:"summary,omitempty"`
	InputSHA256 map[string]string `json:"input_sha256,omitempty"`
}
type task struct {
	id   string
	rule string
	opts engine.RunOptions
}
type Server struct {
	cfg   Config
	root  string
	store string
	mx    sync.RWMutex
	jobs  map[string]*Job
	queue chan task
	mux   *http.ServeMux
}

func utc() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func New(cfg Config) (*Server, error) {
	if cfg.Concurrent == 0 {
		cfg.Concurrent = 2
	}
	if cfg.Queue == 0 {
		cfg.Queue = 16
	}
	if cfg.MaxActiveWorkers == 0 {
		cfg.MaxActiveWorkers = 8
	}
	if cfg.MaxActiveWorkers < 1 || cfg.MaxActiveWorkers > 16 {
		return nil, fmt.Errorf("max_active_workers must be 1..16")
	}
	if cfg.Concurrent < 1 || cfg.Concurrent > 16 || cfg.Queue < 1 || cfg.Queue > 256 {
		return nil, fmt.Errorf("concurrent must be 1..16; queue 1..256")
	}
	root, e := filepath.Abs(cfg.Root)
	if e != nil {
		return nil, e
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return nil, e
	}
	info, e := os.Stat(root)
	if e != nil {
		return nil, e
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root must be directory")
	}
	store, e := filepath.Abs(cfg.Store)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Join(store, "jobs"), 0750); e != nil {
		return nil, e
	}
	store, e = filepath.EvalSymlinks(store)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Join(store, "state"), 0750); e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Join(store, "snapshots"), 0750); e != nil {
		return nil, e
	}
	s := &Server{cfg: cfg, root: root, store: store, jobs: map[string]*Job{}, queue: make(chan task, cfg.Queue), mux: http.NewServeMux()}
	if e = s.loadJobs(); e != nil {
		return nil, e
	}
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		jsonOut(w, 200, map[string]string{"status": "ok", "engine": "allocation-core/0.14"})
	})
	s.mux.HandleFunc("/v1/jobs", s.jobsHandler)
	s.mux.HandleFunc("/v1/jobs/", s.jobHandler)
	s.mux.HandleFunc("/v1/validate", s.validateHandler)
	for i := 0; i < cfg.Concurrent; i++ {
		go s.worker()
	}
	return s, nil
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path != "/healthz" && s.cfg.Token != "" {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(auth) != len(s.cfg.Token) || subtle.ConstantTimeCompare([]byte(auth), []byte(s.cfg.Token)) != 1 {
			jsonOut(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func parseRequest(r *http.Request) (Request, error) {
	var req Request
	r.Body = http.MaxBytesReader(nil, r.Body, 32*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(&req); e != nil {
		return req, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return req, fmt.Errorf("request must contain exactly one JSON object")
	}
	if req.Workers == 0 {
		req.Workers = 2
	}
	if req.DiskPartitions == 0 {
		req.DiskPartitions = 8
	}
	if req.Workers < 1 || req.Workers > 32 || req.DiskPartitions < 2 || req.DiskPartitions > 64 {
		return req, fmt.Errorf("workers 1..32; disk_partitions 2..64")
	}
	if req.DriverCache && !req.DriverDiskIndex {
		return req, fmt.Errorf("driver_cache requires driver_disk_index=true")
	}
	return req, nil
}
func (s *Server) allowed(relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", errors.New("rule must be a nonempty relative path")
	}
	target := filepath.Join(s.root, filepath.FromSlash(relative))
	actual, e := filepath.EvalSymlinks(target)
	if e != nil {
		return "", e
	}
	rel, e := filepath.Rel(s.root, actual)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path outside configured root")
	}
	info, e := os.Stat(actual)
	if e != nil {
		return "", e
	}
	if info.IsDir() {
		return "", errors.New("expected file, got directory")
	}
	return actual, nil
}
func (s *Server) loadRule(relative string) (string, engine.Spec, error) {
	var empty engine.Spec
	path, e := s.allowed(relative)
	if e != nil {
		return "", empty, e
	}
	data, e := os.ReadFile(path)
	if e != nil {
		return "", empty, e
	}
	spec, e := engine.ParseSpec(data)
	if e != nil {
		return "", empty, e
	}
	if _, e = engine.Compile(spec); e != nil {
		return "", empty, e
	}
	// Prevent dataset paths (including symlink targets) from escaping the root.
	datasets := []string{}
	for _, p := range spec.Datasets.Amounts {
		datasets = append(datasets, p)
	}
	for _, p := range spec.Datasets.Drivers {
		datasets = append(datasets, p)
	}
	for _, p := range datasets {
		if filepath.IsAbs(p) {
			return "", empty, errors.New("absolute dataset path prohibited in service")
		}
		abs, e := engine.Resolve(filepath.Dir(path), p)
		if e != nil {
			return "", empty, e
		}
		rel, e := filepath.Rel(s.root, abs)
		if e != nil {
			return "", empty, e
		}
		if _, e = s.allowed(rel); e != nil {
			return "", empty, fmt.Errorf("dataset %s: %w", p, e)
		}
	}
	return path, spec, nil
}
func (s *Server) validateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	req, e := parseRequest(r)
	if e != nil {
		jsonOut(w, 400, map[string]string{"error": e.Error()})
		return
	}
	_, spec, e := s.loadRule(req.Rule)
	if e != nil {
		jsonOut(w, 400, map[string]string{"error": e.Error()})
		return
	}
	graph := make([]string, 0, len(spec.Stages))
	for _, st := range spec.Stages {
		graph = append(graph, st.ID)
	}
	jsonOut(w, 200, map[string]any{"valid": true, "name": spec.Name, "dimensions": spec.Dimensions, "stages": graph})
}
func (s *Server) jobsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		s.mx.RLock()
		jobs := make([]Job, 0, len(s.jobs))
		for _, j := range s.jobs {
			jobs = append(jobs, *j)
		}
		s.mx.RUnlock()
		sort.Slice(jobs, func(i, j int) bool { return jobs[i].Submitted > jobs[j].Submitted })
		jsonOut(w, 200, map[string]any{"jobs": jobs})
	case "POST":
		req, e := parseRequest(r)
		if e != nil {
			jsonOut(w, 400, map[string]string{"error": e.Error()})
			return
		}
		_, _, e = s.loadRule(req.Rule)
		if e != nil {
			jsonOut(w, 400, map[string]string{"error": e.Error()})
			return
		}
		if req.Workers > s.cfg.MaxActiveWorkers || req.Workers*s.cfg.Concurrent > 32 {
			jsonOut(w, 400, map[string]string{"error": "workers exceed configured service resource budget"})
			return
		}
		var b [16]byte
		if _, e = rand.Read(b[:]); e != nil {
			jsonOut(w, 500, map[string]string{"error": "random generator failed"})
			return
		}
		id := hex.EncodeToString(b[:])
		// Freeze the rule and every referenced input before queueing work.
		// The running task NEVER reads the mutable external source paths.
		frozen, hashes, e := s.snapshot(id, req.Rule)
		if e != nil {
			jsonOut(w, 400, map[string]string{"error": e.Error()})
			return
		}
		opts := engine.RunOptions{Workers: req.Workers, DiskPartitions: req.DiskPartitions, DriverDiskIndex: req.DriverDiskIndex, EvidenceGzip: req.EvidenceGzip}
		if req.DriverCache {
			opts.DriverCacheDir = filepath.Join(s.store, "cache")
		}
		j := &Job{ID: id, Rule: req.Rule, Status: "QUEUED", Submitted: utc(), InputSHA256: hashes}
		s.mx.Lock()
		// Persist QUEUED before giving the task to a worker.
		if e = s.saveJob(*j); e != nil {
			s.mx.Unlock()
			os.RemoveAll(filepath.Join(s.store, "snapshots", id))
			jsonOut(w, 500, map[string]string{"error": "cannot persist job state"})
			return
		}
		select {
		case s.queue <- task{id, frozen, opts}:
			s.jobs[id] = j
			response := *j
			s.mx.Unlock()
			jsonOut(w, 202, response)
		default:
			s.mx.Unlock()
			os.RemoveAll(filepath.Join(s.store, "snapshots", id))
			os.Remove(s.statePath(id))
			jsonOut(w, 429, map[string]string{"error": "queue full; retry later"})
		}
	default:
		w.WriteHeader(405)
	}
}
func (s *Server) jobHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	parts := strings.Split(suffix, "/")
	if len(parts) > 2 || parts[0] == "" {
		w.WriteHeader(404)
		return
	}
	id := parts[0]
	s.mx.RLock()
	p, exists := s.jobs[id]
	var j Job
	if exists {
		j = *p
	}
	s.mx.RUnlock()
	if !exists {
		jsonOut(w, 404, map[string]string{"error": "unknown job"})
		return
	}
	if len(parts) == 1 {
		jsonOut(w, 200, j)
		return
	}
	allowed := map[string]bool{"results.csv": true, "ledger.csv": true, "ledger.csv.gz": true, "reconciliation.csv": true, "source_to_pool.csv": true, "source_to_pool.csv.gz": true, "lineage_links.csv": true, "lineage_links.csv.gz": true, "evidence_stream_manifest.json": true, "run_summary.json": true}
	if !allowed[parts[1]] {
		w.WriteHeader(404)
		return
	}
	if j.Status != "PASS" {
		jsonOut(w, 409, map[string]string{"error": "job not complete"})
		return
	}
	path := filepath.Join(s.store, "jobs", id, parts[1])
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", parts[1]))
	http.ServeFile(w, r, path)
}
func (s *Server) worker() {
	for t := range s.queue {
		s.mx.Lock()
		j := s.jobs[t.id]
		j.Status = "RUNNING"
		j.Started = utc()
		if e := s.saveJob(*j); e != nil {
			j.Status = "FAILED"
			j.Error = "job state persist failed: " + e.Error()
			j.Finished = utc()
			_ = s.saveJob(*j)
			s.mx.Unlock()
			continue
		}
		s.mx.Unlock()
		result, e := engine.RunDiskWithOptions(t.rule, filepath.Join(s.store, "jobs", t.id), t.opts)
		s.mx.Lock()
		j = s.jobs[t.id]
		j.Finished = utc()
		if e != nil {
			j.Status = "FAILED"
			j.Error = e.Error()
		} else {
			j.Status = "PASS"
			j.Summary = &result
		}
		if e := s.saveJob(*j); e != nil {
			j.Status = "FAILED"
			j.Error = "final state persist failed: " + e.Error()
			_ = s.saveJob(*j)
		}
		s.mx.Unlock()
	}
}

// Persistent task metadata: atomic rename is the commit point. Restart never
// silently replays a partially completed job: it is reported INTERRUPTED.
func (s *Server) statePath(id string) string { return filepath.Join(s.store, "state", id+".json") }
func (s *Server) saveJob(j Job) error {
	path := s.statePath(j.ID)
	blob, e := json.MarshalIndent(j, "", "  ")
	if e != nil {
		return e
	}
	temp := path + ".pending"
	f, e := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(blob); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	// Windows os.Rename cannot overwrite an existing destination. Remove before
	// rename on that platform; the .pending file remains for investigation on failure.
	if e = os.Rename(temp, path); e != nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return e
		}
		return os.Rename(temp, path)
	}
	return nil
}
func (s *Server) loadJobs() error {
	entries, e := os.ReadDir(filepath.Join(s.store, "state"))
	if e != nil {
		return e
	}
	for _, item := range entries {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".json") {
			continue
		}
		blob, e := os.ReadFile(filepath.Join(s.store, "state", item.Name()))
		if e != nil {
			return e
		}
		var j Job
		if e = json.Unmarshal(blob, &j); e != nil {
			return fmt.Errorf("corrupt job metadata %s: %w", item.Name(), e)
		}
		if j.ID+".json" != item.Name() {
			return fmt.Errorf("mismatched job id: %s", item.Name())
		}
		if j.Status == "QUEUED" || j.Status == "RUNNING" {
			j.Status = "INTERRUPTED"
			j.Finished = utc()
			j.Error = "service restarted before job completed; outputs not published; submit new job to retry"
			if e = s.saveJob(j); e != nil {
				return e
			}
		}
		s.jobs[j.ID] = &j
	}
	return nil
}

// snapshot copies the rule and data under a job-owned immutable directory.
// Relative layout is preserved to avoid modifying the financial rule schema.
func (s *Server) snapshot(id, relative string) (string, map[string]string, error) {
	path, spec, e := s.loadRule(relative)
	if e != nil {
		return "", nil, e
	}
	root := filepath.Join(s.store, "snapshots", id)
	tmp := root + ".pending"
	if e = os.MkdirAll(tmp, 0700); e != nil {
		return "", nil, e
	}
	defer os.RemoveAll(tmp)
	sources := []string{path}
	base := filepath.Dir(path)
	for _, p := range spec.Datasets.Amounts {
		q, er := engine.Resolve(base, p)
		if er != nil {
			return "", nil, er
		}
		sources = append(sources, q)
	}
	for _, p := range spec.Datasets.Drivers {
		q, er := engine.Resolve(base, p)
		if er != nil {
			return "", nil, er
		}
		sources = append(sources, q)
	}
	hashes := map[string]string{}
	for _, source := range sources {
		resolved, er := filepath.EvalSymlinks(source)
		if er != nil {
			return "", nil, er
		}
		rel, er := filepath.Rel(s.root, filepath.Clean(source))
		if er != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", nil, fmt.Errorf("snapshot source escapes allowed root")
		}
		_, er = s.allowed(rel)
		if er != nil {
			return "", nil, er
		}
		if _, exists := hashes[rel]; exists {
			continue
		}
		if er = os.MkdirAll(filepath.Dir(filepath.Join(tmp, rel)), 0700); er != nil {
			return "", nil, er
		}
		before, er := digestFile(resolved)
		if er != nil {
			return "", nil, er
		}
		in, er := os.Open(resolved)
		if er != nil {
			return "", nil, er
		}
		out, er := os.OpenFile(filepath.Join(tmp, rel), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if er != nil {
			in.Close()
			return "", nil, er
		}
		_, er = io.CopyBuffer(out, in, make([]byte, 256*1024))
		in.Close()
		if er2 := out.Sync(); er == nil {
			er = er2
		}
		if er2 := out.Close(); er == nil {
			er = er2
		}
		if er != nil {
			return "", nil, er
		}
		copied, er := digestFile(filepath.Join(tmp, rel))
		if er != nil {
			return "", nil, er
		}
		after, er := digestFile(resolved)
		if er != nil {
			return "", nil, er
		}
		if before != after || after != copied {
			return "", nil, fmt.Errorf("source changed during snapshot: %s", rel)
		}
		hashes[rel] = copied
	}
	if e = os.Rename(tmp, root); e != nil {
		return "", nil, e
	}
	// Do not leak absolute host paths in public state JSON.
	ruleRel, er := filepath.Rel(s.root, path)
	if er != nil {
		return "", nil, er
	}
	return filepath.Join(root, ruleRel), hashes, nil
}
func digestFile(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.CopyBuffer(h, bufio.NewReader(f), make([]byte, 256*1024))
	if e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
