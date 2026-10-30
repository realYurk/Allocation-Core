// Allocation Multi is a fail-closed, standard-library-only job orchestrator.
// It never computes allocations: it invokes the authoritative Allocation Core binary.
package multi

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Amounts          string `json:"amounts"`
	Assignments      string `json:"assignments"`
	Overrides        string `json:"overrides,omitempty"`
	Templates        string `json:"templates"`
	Drivers          string `json:"drivers"`
	Workers          int    `json:"workers"`
	Jobs             int    `json:"jobs"`
	DiskPartitions   int    `json:"disk_partitions"`
	DriverDiskIndex  bool   `json:"driver_disk_index"`
	DriverCacheScope string `json:"driver_cache_scope,omitempty"`
	EvidenceGzip     bool   `json:"evidence_gzip"`
	KeepGroupOutputs bool   `json:"keep_group_outputs"`
	TimeoutSeconds   int    `json:"timeout_seconds"`
}
type Datasets struct {
	Amounts map[string]string `json:"amounts"`
	Drivers map[string]string `json:"drivers"`
}
type Edge struct {
	From string `json:"from"`
}
type Stage struct {
	ID     string `json:"id"`
	Inputs []Edge `json:"inputs"`
	Source struct {
		Grouping map[string]string `json:"grouping"`
	} `json:"source"`
	Target map[string]struct {
		Mode string `json:"mode"`
	} `json:"target"`
	ConservationKeys []string `json:"conservation_keys"`
	Allocations      []struct {
		ID            string `json:"id"`
		DriverDataset string `json:"driver_dataset"`
		WeightField   string `json:"weight_field"`
		Share         string `json:"share"`
	} `json:"allocations"`
}
type Rule struct {
	Schema     string   `json:"schema"`
	Name       string   `json:"name"`
	Precision  int      `json:"precision"`
	Dimensions []string `json:"dimensions"`
	Immutable  []string `json:"immutable_dimensions"`
	Datasets   Datasets `json:"datasets"`
	Stages     []Stage  `json:"stages"`
}
type Assignment struct{ Account, Template string }
type Override struct{ Account, Stage, Allocation, DriverDataset, WeightField, Share string }
type Group struct {
	ID         string         `json:"group_id"`
	Accounts   []string       `json:"accounts"`
	Template   string         `json:"template"`
	Count      int64          `json:"input_rows"`
	Input      string         `json:"-"`
	Rule       string         `json:"-"`
	Output     string         `json:"-"`
	Summary    map[string]any `json:"-"`
	DurationMS int64          `json:"duration_ms"`
	Status     string         `json:"status"`
	Error      string         `json:"error,omitempty"`
}
type AccountResult struct {
	Account  string `json:"account"`
	Template string `json:"template"`
	Input    string `json:"input_amount"`
	Output   string `json:"output_amount"`
	Rows     int64  `json:"result_rows"`
	Status   string `json:"status"`
}
type Result struct {
	Status     string          `json:"status"`
	Started    string          `json:"started_utc"`
	DurationMS int64           `json:"duration_ms"`
	Accounts   int             `json:"accounts"`
	Groups     int             `json:"groups"`
	Input      string          `json:"input_amount"`
	Output     string          `json:"output_amount"`
	ResultRows int64           `json:"result_rows"`
	Details    []AccountResult `json:"details"`
	Engines    []Group         `json:"engines"`
}

type project struct {
	dir                       string
	cfg                       Config
	engine                    string
	stage                     string
	groups                    []*Group
	assign                    map[string]string
	effective                 map[string]string
	header                    []string
	accountIndex, amountIndex int
	sums                      map[string]*big.Int
	precision                 int
	canonicalHeader           []string
	ctx                       context.Context
	controls                  map[string]string
	amountOriginalSHA         string
	driverSourceSHA           map[string]string
}

func CLI() {
	if len(os.Args) > 1 && os.Args[1] == "batch" {
		runBatchCLI()
	}
	projectDir := flag.String("project", "", "folder containing project.json, amounts, assignments, templates and drivers")
	output := flag.String("out", "", "new published result directory")
	engine := flag.String("engine", "", "authoritative alloc binary; optional if next to multi executable")
	cancelFile := flag.String("cancel-file", "", "path to a local cancellation marker (Studio uses this for graceful child cancellation)")
	studioLease := flag.String("studio-lease", "", "Studio heartbeat file; stop Core children if host Studio disappears")
	flag.Parse()
	if *projectDir == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "usage: allocation_multi -project PROJECT_FOLDER -out NEW_OUTPUT_DIR [-engine core/alloc_...] [-cancel-file FILE] ")
		os.Exit(2)
	}
	exe := *engine
	if exe == "" {
		self, _ := os.Executable()
		base := filepath.Dir(self)
		ext := ""
		if runtime.GOOS == "windows" {
			ext = ".exe"
		}
		exe = filepath.Join(base, "core", fmt.Sprintf("alloc_%s_%s%s", runtime.GOOS, runtime.GOARCH, ext))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if *cancelFile != "" {
		// Polling avoids Unix-only process groups and lets Windows Studio cancel
		// every authoritative Core child through its own CommandContext.
		go func() {
			tick := time.NewTicker(200 * time.Millisecond)
			defer tick.Stop()
			for {
				if _, err := os.Stat(*cancelFile); err == nil {
					cancel()
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
			}
		}()
	}
	if *studioLease != "" {
		// The launcher owns the lease. A missing or expired lease cancels the
		// entire Multi context, which stops all Core CommandContext children.
		if st, e := os.Stat(*studioLease); e != nil || !st.Mode().IsRegular() {
			fmt.Fprintln(os.Stderr, "BLOCK: Studio heartbeat missing")
			os.Exit(1)
		}
		go monitorStudioLease(ctx, cancel, *studioLease, 25*time.Second, 2*time.Second)
	}
	result, err := RunContext(ctx, *projectDir, *output, exe)
	if err != nil {
		fmt.Fprintln(os.Stderr, "BLOCK:", err)
		os.Exit(1)
	}
	fmt.Printf("PASS: %d accounts, %d rule groups, %d results, %s -> %s (%dms)\n", result.Accounts, result.Groups, result.ResultRows, result.Input, result.Output, result.DurationMS)
}

// Only Studio-launched Multi tasks opt into this lease. Without it, the standalone
// command line remains independent and never stops because a UI was closed.
func monitorStudioLease(ctx context.Context, cancel context.CancelFunc, path string, ttl, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			st, err := os.Stat(path)
			if err != nil || !st.Mode().IsRegular() || time.Since(st.ModTime()) > ttl {
				cancel()
				return
			}
		}
	}
}

func safeFile(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\:\x00") {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}
func regularWithin(dir, name string) (string, error) {
	if !safeFile(name) {
		return "", fmt.Errorf("unsafe file: %q", name)
	}
	p := filepath.Join(dir, name)
	fi, e := os.Lstat(p)
	if e != nil {
		return "", e
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %s", p)
	}
	return p, nil
}
func readJSON(path string, v any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return decodeStrictJSON(path, b, v)
}
func decodeStrictJSON(path string, b []byte, v any) error {
	var e error
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if e = dec.Decode(v); e != nil {
		return fmt.Errorf("%s: %w", path, e)
	}
	var extra any
	if e = dec.Decode(&extra); e != io.EOF {
		return fmt.Errorf("%s: trailing JSON", path)
	}
	return nil
}
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, b, 0600)
}
func readCSV(path string) ([][]string, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	rd := csv.NewReader(bufio.NewReaderSize(f, 1<<17))
	rd.FieldsPerRecord = -1
	rows, e := rd.ReadAll()
	return rows, e
}
func requiredColumns(header []string, cols ...string) (map[string]int, error) {
	p := map[string]int{}
	for i, s := range header {
		s = strings.TrimPrefix(s, "\ufeff")
		if s == "" {
			return nil, errors.New("empty header")
		}
		if _, ok := p[s]; ok {
			return nil, fmt.Errorf("duplicate column: %s", s)
		}
		p[s] = i
	}
	for _, c := range cols {
		if _, ok := p[c]; !ok {
			return nil, fmt.Errorf("missing CSV column: %s", c)
		}
	}
	return p, nil
}
func get(row []string, ix int) string {
	if ix < 0 || ix >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[ix])
}
func digest(s []byte) string { h := sha256.Sum256(s); return hex.EncodeToString(h[:]) }
func parseScaled(s string, precision int) (*big.Int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty amount")
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	parts := strings.Split(s, ".")
	if len(parts) > 2 || len(parts[0]) == 0 {
		return nil, fmt.Errorf("invalid amount %s", s)
	}
	for _, r := range s {
		if r != '.' && (r < '0' || r > '9') {
			return nil, fmt.Errorf("invalid amount %s", s)
		}
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > precision {
		return nil, fmt.Errorf("precision exceeds %d: %s", precision, s)
	}
	i, ok := new(big.Int).SetString(parts[0]+frac+strings.Repeat("0", precision-len(frac)), 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount %s", s)
	}
	if neg {
		i.Neg(i)
	}
	return i, nil
}
func money(v *big.Int, p int) string {
	neg := ""
	n := new(big.Int).Set(v)
	if n.Sign() < 0 {
		neg = "-"
		n.Abs(n)
	}
	if p == 0 {
		return neg + n.String()
	}
	unit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(p)), nil)
	q, r := new(big.Int).QuoRem(n, unit, new(big.Int))
	return neg + q.String() + "." + fmt.Sprintf("%0*s", p, r.String())
}
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
func validateRuleForMulti(raw []byte) (Rule, error) {
	var r Rule
	if e := json.Unmarshal(raw, &r); e != nil {
		return r, e
	}
	if r.Schema != "allocation-ir/2.0" || r.Precision < 0 || r.Precision > 6 || !contains(r.Dimensions, "account") {
		return r, errors.New("rule requires allocation-ir/2.0, precision 0..6 and account dimension")
	}
	if len(r.Datasets.Amounts) != 1 {
		return r, errors.New("multi currently requires exactly one amounts dataset per template")
	}
	if !contains(r.Immutable, "account") {
		return r, errors.New("account must be in immutable_dimensions")
	}
	if len(r.Stages) == 0 {
		return r, errors.New("no stages")
	}
	for _, s := range r.Stages {
		if s.Source.Grouping["account"] != "AS_IS" || s.Target["account"].Mode != "SOURCE" || !contains(s.ConservationKeys, "account") {
			return r, fmt.Errorf("stage %s must preserve account as AS_IS/SOURCE/conservation_key", s.ID)
		}
	}
	return r, nil
}

// Apply overrides on typed JSON preserving all other Rule fields (including hierarchies and filters).
func applyOverride(root map[string]any, o Override) error {
	stages, ok := root["stages"].([]any)
	if !ok {
		return errors.New("invalid stages")
	}
	for _, sv := range stages {
		s, ok := sv.(map[string]any)
		if !ok || s["id"] != o.Stage {
			continue
		}
		aa, ok := s["allocations"].([]any)
		if !ok {
			return fmt.Errorf("stage %s has no allocations", o.Stage)
		}
		for _, av := range aa {
			a, ok := av.(map[string]any)
			if ok && a["id"] == o.Allocation {
				if o.DriverDataset != "" {
					a["driver_dataset"] = o.DriverDataset
				}
				if o.WeightField != "" {
					a["weight_field"] = o.WeightField
				}
				if o.Share != "" {
					a["share"] = o.Share
				}
				return nil
			}
		}
		return fmt.Errorf("allocation %s not found on stage %s", o.Allocation, o.Stage)
	}
	return fmt.Errorf("stage %s not found", o.Stage)
}
func (p *project) recordControl(path string, raw []byte) {
	if p.controls == nil {
		p.controls = make(map[string]string)
	}
	p.controls[path] = digest(raw)
}
func (p *project) verifyControls() error {
	for path, expected := range p.controls {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		got, err := fileDigest(path)
		if err != nil || got != expected {
			return fmt.Errorf("project configuration changed during run: %s", path)
		}
	}
	return nil
}
func (p *project) initialize() error {
	var cfg Config
	configPath := filepath.Join(p.dir, "project.json")
	configBytes, e := os.ReadFile(configPath)
	if e != nil {
		return e
	}
	if e = decodeStrictJSON(configPath, configBytes, &cfg); e != nil {
		return e
	}
	p.cfg = cfg
	p.recordControl(configPath, configBytes)
	if cfg.Workers < 1 || cfg.Workers > 16 || cfg.Jobs < 1 || cfg.Jobs > 16 || cfg.Workers*cfg.Jobs > 32 || cfg.DiskPartitions < 0 || cfg.DiskPartitions == 1 || cfg.DiskPartitions > 64 || cfg.TimeoutSeconds < 0 {
		return errors.New("workers 1..16, jobs 1..16, workers*jobs <=32, disk_partitions 0 or 2..64, timeout>=0 required")
	}
	if !cfg.KeepGroupOutputs {
		return errors.New("keep_group_outputs must be true: financial evidence cannot be discarded")
	}
	if cfg.DriverCacheScope != "" && cfg.DriverCacheScope != "run" && cfg.DriverCacheScope != "project" {
		return errors.New("driver_cache_scope must be run or project")
	}
	if cfg.DriverCacheScope == "project" && !cfg.DriverDiskIndex {
		return errors.New("project cache requires driver_disk_index")
	}
	if cfg.DriverDiskIndex && cfg.DiskPartitions < 2 {
		return errors.New("driver_disk_index needs disk_partitions >=2")
	}
	if !safeFile(cfg.Amounts) || !safeFile(cfg.Assignments) || cfg.Templates != "templates" || cfg.Drivers != "drivers" {
		return errors.New("expected safe CSV files and templates/drivers folder names")
	}
	for _, d := range []string{cfg.Templates, cfg.Drivers} {
		fi, er := os.Lstat(filepath.Join(p.dir, d))
		if er != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("invalid folder %s", d)
		}
	}
	assPath, e := regularWithin(p.dir, cfg.Assignments)
	if e != nil {
		return e
	}
	assBytes, e := os.ReadFile(assPath)
	if e != nil {
		return e
	}
	p.recordControl(assPath, assBytes)
	rows, e := csv.NewReader(strings.NewReader(string(assBytes))).ReadAll()
	if e != nil || len(rows) == 0 {
		return fmt.Errorf("assignments: %v", e)
	}
	header, e := requiredColumns(rows[0], "account", "template")
	if e != nil {
		return e
	}
	p.assign = map[string]string{}
	p.effective = map[string]string{}
	p.sums = map[string]*big.Int{}
	p.precision = -1
	for n, row := range rows[1:] {
		account, template := get(row, header["account"]), get(row, header["template"])
		if account == "" || !safeFile(template) || !strings.HasSuffix(template, ".json") {
			return fmt.Errorf("invalid account/template on assignments row %d", n+2)
		}
		if _, ok := p.assign[account]; ok {
			return fmt.Errorf("duplicate assignment: %s", account)
		}
		p.assign[account] = template
	}
	if len(p.assign) == 0 {
		return errors.New("no accounts assigned")
	}
	ovs := map[string][]Override{}
	if cfg.Overrides != "" {
		path, er := regularWithin(p.dir, cfg.Overrides)
		if er != nil {
			return er
		}
		ovsBytes, er := os.ReadFile(path)
		if er != nil {
			return er
		}
		p.recordControl(path, ovsBytes)
		rr, er := csv.NewReader(strings.NewReader(string(ovsBytes))).ReadAll()
		if er != nil || len(rr) == 0 {
			return fmt.Errorf("overrides: %v", er)
		}
		ix, er := requiredColumns(rr[0], "account", "stage", "allocation", "driver_dataset", "weight_field", "share")
		if er != nil {
			return er
		}
		seen := map[string]bool{}
		for n, row := range rr[1:] {
			o := Override{get(row, ix["account"]), get(row, ix["stage"]), get(row, ix["allocation"]), get(row, ix["driver_dataset"]), get(row, ix["weight_field"]), get(row, ix["share"])}
			if _, ok := p.assign[o.Account]; !ok {
				return fmt.Errorf("override for unassigned account %s", o.Account)
			}
			sig := o.Account + "\x00" + o.Stage + "\x00" + o.Allocation
			if seen[sig] {
				return fmt.Errorf("duplicate override %s", sig)
			}
			seen[sig] = true
			if o.Stage == "" || o.Allocation == "" {
				return fmt.Errorf("override row %d: missing stage or allocation", n+2)
			}
			ovs[o.Account] = append(ovs[o.Account], o)
		}
	}
	names := make([]string, 0, len(p.assign))
	for a := range p.assign {
		names = append(names, a)
	}
	sort.Strings(names)
	sigGroups := map[string]*Group{}
	// Cache source templates: every account sees the same template bytes, even
	// if somebody replaces a JSON file during initialization.
	templateCache := map[string][]byte{}
	for _, account := range names {
		path, er := regularWithin(filepath.Join(p.dir, "templates"), p.assign[account])
		if er != nil {
			return er
		}
		b, ok := templateCache[path]
		if !ok {
			b, er = os.ReadFile(path)
			if er != nil {
				return er
			}
			templateCache[path] = b
			p.recordControl(path, b)
		}
		var root map[string]any
		if er = json.Unmarshal(b, &root); er != nil {
			return er
		}
		for _, o := range ovs[account] {
			if o.DriverDataset != "" && !safeFile(o.DriverDataset) {
				return errors.New("unsafe driver dataset name")
			}
			if er = applyOverride(root, o); er != nil {
				return fmt.Errorf("account %s: %w", account, er)
			}
		}
		b, er = json.Marshal(root)
		if er != nil {
			return er
		}
		rule, er := validateRuleForMulti(b)
		if er != nil {
			return fmt.Errorf("account %s: %w", account, er)
		}
		if p.precision == -1 {
			p.precision = rule.Precision
			p.canonicalHeader = append([]string{}, rule.Dimensions...)
		} else if p.precision != rule.Precision || strings.Join(p.canonicalHeader, "\x00") != strings.Join(rule.Dimensions, "\x00") {
			return fmt.Errorf("account %s has incompatible dimension order or precision", account)
		}
		// Only single amounts source; make the generated input relative to each group rule folder.
		for k := range rule.Datasets.Amounts {
			root["datasets"].(map[string]any)["amounts"].(map[string]any)[k] = "group_amounts.csv"
		}
		for k, v := range rule.Datasets.Drivers {
			driver, er := regularWithin(filepath.Join(p.dir, "drivers"), v)
			if er != nil {
				return fmt.Errorf("account %s driver %s: %w", account, v, er)
			}
			root["datasets"].(map[string]any)["drivers"].(map[string]any)[k] = driver
		}
		// Group identical effective rules. The runtime input path is shared by this group only.
		normalized, er := json.Marshal(root)
		if er != nil {
			return er
		}
		id := digest(normalized)[:20]
		g := sigGroups[id]
		if g == nil {
			g = &Group{ID: id, Template: p.assign[account], Status: "QUEUED"}
			sigGroups[id] = g
			g.Rule = string(normalized)
		}
		g.Accounts = append(g.Accounts, account)
		p.effective[account] = id
		p.sums[account] = new(big.Int)
	}
	ids := make([]string, 0, len(sigGroups))
	for k := range sigGroups {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p.groups = append(p.groups, sigGroups[id])
	}
	return nil
}

// A bounded, buffered LRU writer handles thousands of groups with fixed FD count.
type csvHandle struct {
	file   *os.File
	buffer *bufio.Writer
	writer *csv.Writer
}
type csvHandles struct {
	open   map[string]*csvHandle
	order  []string
	header []string
	limit  int
}

func newHandles(header []string, limit int) *csvHandles {
	return &csvHandles{open: map[string]*csvHandle{}, header: header, limit: limit}
}
func (h *csvHandles) evict(name string) error {
	v := h.open[name]
	if v == nil {
		return nil
	}
	v.writer.Flush()
	if e := v.writer.Error(); e != nil {
		return e
	}
	if e := v.buffer.Flush(); e != nil {
		return e
	}
	if e := v.file.Sync(); e != nil {
		return e
	}
	if e := v.file.Close(); e != nil {
		return e
	}
	delete(h.open, name)
	return nil
}
func (h *csvHandles) write(path string, row []string) error {
	v := h.open[path]
	if v == nil {
		if len(h.open) >= h.limit {
			old := h.order[0]
			h.order = h.order[1:]
			if e := h.evict(old); e != nil {
				return e
			}
		}
		_, er := os.Stat(path)
		fresh := os.IsNotExist(er)
		if er != nil && !fresh {
			return er
		}
		f, er := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if er != nil {
			return er
		}
		b := bufio.NewWriterSize(f, 128*1024)
		v = &csvHandle{file: f, buffer: b, writer: csv.NewWriter(b)}
		h.open[path] = v
		if fresh {
			if e := v.writer.Write(h.header); e != nil {
				return e
			}
		}
	}
	for i, n := range h.order {
		if n == path {
			h.order = append(h.order[:i], h.order[i+1:]...)
			break
		}
	}
	h.order = append(h.order, path)
	return v.writer.Write(row)
}
func (h *csvHandles) close() error {
	var err error
	for name := range h.open {
		if e := h.evict(name); e != nil && err == nil {
			err = e
		}
	}
	h.order = nil
	return err
}

// Bucket by rule group before writing individual group files: keeps FD and
// disk writes bounded even if millions of rows interleave 1,000 rule groups.
func (p *project) splitInputs() error {
	path, e := regularWithin(p.dir, p.cfg.Amounts)
	if e != nil {
		return e
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	// Hash the bytes actually parsed while streaming; one full-file read
	// replaces an additional pre-scan before CSV processing.
	readHash := sha256.New()
	rd := csv.NewReader(bufio.NewReaderSize(io.TeeReader(f, readHash), 256*1024))
	rd.FieldsPerRecord = -1
	p.header, e = rd.Read()
	if e != nil {
		return e
	}
	ix, e := requiredColumns(p.header, "account", "amount")
	if e != nil {
		return e
	}
	for _, dim := range p.canonicalHeader {
		if _, ok := ix[dim]; !ok {
			return fmt.Errorf("amounts missing dimension %s", dim)
		}
	}
	p.accountIndex = ix["account"]
	p.amountIndex = ix["amount"]
	byID := map[string]*Group{}
	for _, g := range p.groups {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		folder := filepath.Join(p.stage, "groups", g.ID)
		if e = os.MkdirAll(folder, 0700); e != nil {
			return e
		}
		g.Input = filepath.Join(folder, "group_amounts.csv")
		g.Output = filepath.Join(folder, "output")
		byID[g.ID] = g
	}
	shardDir := filepath.Join(p.stage, "shards")
	if e = os.MkdirAll(shardDir, 0700); e != nil {
		return e
	}
	shards := newHandles(p.header, 64)
	defer shards.close()
	counts := map[string]int64{}
	n := int64(0)
	for {
		row, er := rd.Read()
		if er == io.EOF {
			break
		}
		if er != nil {
			return fmt.Errorf("amount input row %d: %w", n+2, er)
		}
		n++
		if n%1024 == 0 {
			if err := p.ctx.Err(); err != nil {
				return err
			}
		}
		a := get(row, p.accountIndex)
		id, ok := p.effective[a]
		if !ok {
			return fmt.Errorf("unassigned amount account %q at row %d", a, n+1)
		}
		if get(row, p.amountIndex) == "" {
			return fmt.Errorf("empty money at row %d", n+1)
		}
		v, er := parseScaled(get(row, p.amountIndex), p.precision)
		if er != nil {
			return fmt.Errorf("amounts row %d: %w", n+1, er)
		}
		p.sums[a].Add(p.sums[a], v)
		counts[a]++
		byID[id].Count++
		// Group IDs are hex hashes. Each group always belongs to exactly one shard.
		z, er := strconv.ParseUint(id[:2], 16, 8)
		if er != nil {
			return er
		}
		sh := fmt.Sprintf("bucket_%02d.csv", int(z)%64)
		if er = shards.write(filepath.Join(shardDir, sh), row); er != nil {
			return er
		}
	}
	if e = shards.close(); e != nil {
		return e
	}
	finalHash, e := fileDigest(path)
	if e != nil {
		return e
	}
	if finalHash != hex.EncodeToString(readHash.Sum(nil)) {
		return errors.New("amounts changed during snapshot")
	}
	p.amountOriginalSHA = finalHash
	for a := range p.assign {
		if counts[a] == 0 {
			return fmt.Errorf("assigned account has no input data: %s", a)
		}
	}
	for _, g := range p.groups {
		if g.Count == 0 {
			return fmt.Errorf("rule group has no input: %s", g.ID)
		}
	}
	for i := 0; i < 64; i++ {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		file := filepath.Join(shardDir, fmt.Sprintf("bucket_%02d.csv", i))
		sf, er := os.Open(file)
		if os.IsNotExist(er) {
			continue
		}
		if er != nil {
			return er
		}
		r := csv.NewReader(bufio.NewReaderSize(sf, 128*1024))
		r.FieldsPerRecord = -1
		_, er = r.Read()
		if er != nil {
			sf.Close()
			return er
		}
		handles := newHandles(p.header, 32)
		for {
			row, er := r.Read()
			if er == io.EOF {
				break
			}
			if er != nil {
				handles.close()
				sf.Close()
				return er
			}
			id := p.effective[get(row, p.accountIndex)]
			if er = handles.write(byID[id].Input, row); er != nil {
				handles.close()
				sf.Close()
				return er
			}
		}
		if er = handles.close(); er != nil {
			sf.Close()
			return er
		}
		if er = sf.Close(); er != nil {
			return er
		}
		if er = os.Remove(file); er != nil {
			return er
		}
	}
	return os.Remove(shardDir)
}
func (p *project) buildRuleFiles() error {
	for _, g := range p.groups {
		path := filepath.Join(p.stage, "groups", g.ID, "rule.json")
		if e := os.WriteFile(path, []byte(g.Rule), 0600); e != nil {
			return e
		}
		g.Rule = path
	}
	return nil
}

// driverCacheDirectory persists only when explicitly requested by project configuration.
// Each Core validates a cache entry's semantic config and source content digest.
func (p *project) driverCacheDirectory() (string, error) {
	dir := filepath.Join(p.stage, "driver-cache")
	if p.cfg.DriverCacheScope == "project" {
		dir = filepath.Join(p.dir, ".allocation-driver-cache")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("unsafe driver-cache directory: %s", dir)
	}
	return dir, nil
}
func (p *project) execute(parent context.Context) error {
	// Exactly Jobs workers, regardless of 1,000+ distinct rule groups. First
	// failure cancels in-flight Core subprocesses and prevents further dispatch.
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	queue := make(chan *Group)
	var wg sync.WaitGroup
	var first error
	var once sync.Once
	failGroup := func(g *Group, err error) {
		g.Status = "FAILED"
		g.Error = err.Error()
		once.Do(func() { first = fmt.Errorf("rule group %s: %w", g.ID, err); cancel() })
	}
	for i := 0; i < p.cfg.Jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range queue {
				if ctx.Err() != nil {
					return
				}
				start := time.Now()
				args := []string{"run", g.Rule, g.Output, "--workers", strconv.Itoa(p.cfg.Workers)}
				if p.cfg.DiskPartitions > 1 {
					args = append(args, "--disk-partitions", strconv.Itoa(p.cfg.DiskPartitions))
				}
				if p.cfg.DriverDiskIndex {
					cacheDir, cacheErr := p.driverCacheDirectory()
					if cacheErr != nil {
						failGroup(g, cacheErr)
						return
					}
					args = append(args, "--driver-disk-index", "--driver-cache-dir", cacheDir)
				}
				if p.cfg.EvidenceGzip {
					args = append(args, "--evidence-gzip")
				}
				c := exec.CommandContext(ctx, p.engine, append([]string{"core"}, args...)...)
				c.Dir = p.dir
				log, err := c.CombinedOutput()
				g.DurationMS = time.Since(start).Milliseconds()
				if len(log) > 10000 {
					log = log[:10000]
				}
				if err != nil {
					failGroup(g, fmt.Errorf("%v: %s", err, log))
					return
				}
				var summary map[string]any
				if err = readJSON(filepath.Join(g.Output, "run_summary.json"), &summary); err != nil || summary["status"] != "PASS" {
					failGroup(g, fmt.Errorf("missing/invalid run summary: %v", err))
					return
				}
				g.Summary = summary
				g.Status = "PASS"
			}
		}()
	}
loop:
	for _, g := range p.groups {
		select {
		case <-ctx.Done():
			break loop
		case queue <- g:
		}
	}
	close(queue)
	wg.Wait()
	if parent.Err() != nil {
		return parent.Err()
	}
	if first != nil {
		return first
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
func (p *project) finish(start time.Time) (Result, error) {
	r := Result{Status: "PASS", Started: start.UTC().Format(time.RFC3339), Accounts: len(p.assign), Groups: len(p.groups)}
	input, output := new(big.Int), new(big.Int)
	actual := map[string]*big.Int{}
	rows := map[string]int64{}
	for a := range p.sums {
		actual[a] = new(big.Int)
		input.Add(input, p.sums[a])
	}
	merged, e := os.Create(filepath.Join(p.stage, "results.csv"))
	if e != nil {
		return r, e
	}
	defer merged.Close()
	writerBuffer := bufio.NewWriterSize(merged, 1<<20)
	w := csv.NewWriter(writerBuffer) // Writer Flush is also checked below.
	expectedHeader := append([]string{"terminal_stage", "row_id"}, p.canonicalHeader...)
	expectedHeader = append(expectedHeader, "amount")
	if e = w.Write(expectedHeader); e != nil {
		return r, e
	}
	accIx := 2
	for i, d := range p.canonicalHeader {
		if d == "account" {
			accIx = 2 + i
			break
		}
	}
	for _, g := range p.groups {
		fp := filepath.Join(g.Output, "results.csv")
		f, er := os.Open(fp)
		if er != nil {
			return r, er
		}
		rd := csv.NewReader(bufio.NewReaderSize(f, 1<<17))
		hdr, er := rd.Read()
		if len(hdr) > 0 {
			hdr[0] = strings.TrimPrefix(hdr[0], "\ufeff")
		}
		if er != nil {
			f.Close()
			return r, er
		}
		if strings.Join(hdr, "\x00") != strings.Join(expectedHeader, "\x00") {
			f.Close()
			return r, fmt.Errorf("result schema mismatch in group %s: actual=%v expected=%v", g.ID, hdr, expectedHeader)
		}
		for {
			row, err := rd.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				f.Close()
				return r, err
			}
			account := get(row, accIx)
			if p.effective[account] != g.ID {
				f.Close()
				return r, fmt.Errorf("cross-account leakage: group %s emitted %q", g.ID, account)
			}
			amt, err := parseScaled(get(row, len(row)-1), p.precision)
			if err != nil {
				f.Close()
				return r, err
			}
			actual[account].Add(actual[account], amt)
			rows[account]++
			r.ResultRows++
			if er = w.Write(row); er != nil {
				f.Close()
				return r, er
			}
		}
		f.Close()
	}
	w.Flush()
	if e = w.Error(); e != nil {
		return r, e
	}
	if e = writerBuffer.Flush(); e != nil {
		return r, e
	}
	if e = merged.Sync(); e != nil {
		return r, e
	}
	accs := make([]string, 0, len(p.assign))
	for a := range p.assign {
		accs = append(accs, a)
	}
	sort.Strings(accs)
	for _, a := range accs {
		out := actual[a]
		output.Add(output, out)
		if out.Cmp(p.sums[a]) != 0 {
			return r, fmt.Errorf("ACCOUNT CONSERVATION FAILED: %s input=%s output=%s", a, money(p.sums[a], p.precision), money(out, p.precision))
		}
		if rows[a] == 0 {
			return r, fmt.Errorf("account %s has no result rows", a)
		}
		r.Details = append(r.Details, AccountResult{a, p.assign[a], money(p.sums[a], p.precision), money(out, p.precision), rows[a], "PASS"})
	}
	r.Input = money(input, p.precision)
	r.Output = money(output, p.precision)
	r.DurationMS = time.Since(start).Milliseconds()
	if input.Cmp(output) != 0 {
		return r, errors.New("global conservation violation")
	}
	for _, g := range p.groups {
		r.Engines = append(r.Engines, *g)
	}
	accountFile, e := os.Create(filepath.Join(p.stage, "account_reconciliation.csv"))
	if e != nil {
		return r, e
	}
	accWriter := csv.NewWriter(accountFile)
	_ = accWriter.Write([]string{"account", "template", "input_amount", "output_amount", "delta", "result_rows", "status"})
	for _, v := range r.Details {
		if e = accWriter.Write([]string{v.Account, v.Template, v.Input, v.Output, money(new(big.Int), p.precision), strconv.FormatInt(v.Rows, 10), v.Status}); e != nil {
			accountFile.Close()
			return r, e
		}
	}
	accWriter.Flush()
	e = accWriter.Error()
	if er := accountFile.Sync(); e == nil {
		e = er
	}
	if er := accountFile.Close(); e == nil {
		e = er
	}
	if e != nil {
		return r, e
	}
	if e = writeJSON(filepath.Join(p.stage, "run_summary.json"), r); e != nil {
		return r, e
	}

	return r, nil
}

// Every driver is copied once into the immutable job snapshot, then checked
// against its original SHA-256; per-group rules use relative paths so they
// remain valid after the pending directory is atomically published.
func fileDigest(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	if e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (p *project) snapshotDrivers() error {
	folder := filepath.Join(p.stage, "snapshots")
	if e := os.MkdirAll(folder, 0700); e != nil {
		return e
	}
	copied := map[string]string{}
	for _, g := range p.groups {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		var rule map[string]any
		if e := json.Unmarshal([]byte(g.Rule), &rule); e != nil {
			return e
		}
		ds := rule["datasets"].(map[string]any)["drivers"].(map[string]any)
		for key, raw := range ds {
			original, ok := raw.(string)
			if !ok {
				return errors.New("invalid driver source")
			}
			if snap, ok := copied[original]; ok {
				ds[key] = snap
				continue
			}
			info, e := os.Lstat(original)
			if e != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("driver must be regular: %s", original)
			}
			hashName := digest([]byte(original))[:20] + ".csv"
			snapAbs := filepath.Join(folder, hashName)
			src, e := os.Open(original)
			if e != nil {
				return e
			}
			dst, e := os.OpenFile(snapAbs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				src.Close()
				return e
			}
			digestCopy := sha256.New()
			_, e = io.Copy(dst, io.TeeReader(src, digestCopy))
			if er := dst.Sync(); e == nil {
				e = er
			}
			if er := dst.Close(); e == nil {
				e = er
			}
			src.Close()
			if e != nil {
				return e
			}
			after, e := fileDigest(original)
			if e != nil {
				return e
			}
			if after != hex.EncodeToString(digestCopy.Sum(nil)) {
				return fmt.Errorf("driver changed during snapshot: %s", original)
			}
			if p.driverSourceSHA == nil {
				p.driverSourceSHA = map[string]string{}
			}
			p.driverSourceSHA[original] = after
			rel := "../../snapshots/" + hashName
			copied[original] = rel
			ds[key] = rel
		}
		normalized, e := json.Marshal(rule)
		if e != nil {
			return e
		}
		g.Rule = string(normalized)
	}
	return nil
}

// Run never publishes partial outputs, including when an individual account fails.
func Run(dir, out, engine string) (Result, error) {
	return RunContext(context.Background(), dir, out, engine)
}
func RunContext(ctx context.Context, dir, out, engine string) (Result, error) {
	start := time.Now()
	var result Result
	var e error
	dir, e = filepath.Abs(dir)
	if e != nil {
		return result, e
	}
	engine, e = filepath.Abs(engine)
	if e != nil {
		return result, e
	}
	fi, e := os.Stat(engine)
	if e != nil || fi.IsDir() {
		return result, fmt.Errorf("missing engine: %s", engine)
	}
	out, e = filepath.Abs(out)
	if e != nil {
		return result, e
	}
	if _, e = os.Stat(out); e == nil {
		return result, fmt.Errorf("output exists: %s", out)
	} else if !os.IsNotExist(e) {
		return result, e
	}
	if e = os.MkdirAll(filepath.Dir(out), 0750); e != nil {
		return result, e
	}
	stage, e := os.MkdirTemp(filepath.Dir(out), ".multi-pending-")
	if e != nil {
		return result, e
	}
	defer os.RemoveAll(stage)
	p := &project{dir: dir, stage: stage, engine: engine, ctx: ctx}
	if e = ctx.Err(); e != nil {
		return result, e
	}
	if e = p.initialize(); e != nil {
		return result, e
	}
	// Apply the requested timeout to snapshotting, CSV splitting, child runs,
	// and final publication checks, not just the external Core processes.
	if p.cfg.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(p.cfg.TimeoutSeconds)*time.Second)
		p.ctx = ctx
		defer cancel()
	}
	if e = p.verifyControls(); e != nil {
		return result, e
	}
	if e = p.snapshotDrivers(); e != nil {
		return result, e
	}
	if e = p.splitInputs(); e != nil {
		return result, e
	}
	if e = p.buildRuleFiles(); e != nil {
		return result, e
	}
	if e = p.execute(ctx); e != nil {
		return result, e
	}
	result, e = p.finish(start)
	if e != nil {
		return result, e
	}
	if e = ctx.Err(); e != nil {
		return result, e
	}
	if e = p.verifyControls(); e != nil {
		return result, e
	}
	if e = p.snapshotControls(); e != nil {
		return result, e
	}
	if e = os.Rename(stage, out); e != nil {
		return result, e
	}
	return result, nil
}

// snapshotControls records the exact source configuration digests needed to
// prove which business rule assignments produced this published batch.
func (p *project) snapshotControls() error {
	dir := filepath.Join(p.stage, "configuration")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	mapping := map[string]string{}
	if p.amountOriginalSHA == "" {
		return fmt.Errorf("missing captured amounts SHA-256")
	}
	mapping[filepath.ToSlash(p.cfg.Amounts)] = p.amountOriginalSHA
	for original, sha := range p.driverSourceSHA {
		rel, err := filepath.Rel(p.dir, original)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("driver original outside project")
		}
		mapping[filepath.ToSlash(rel)] = sha
	}
	names := make([]string, 0, len(p.controls))
	for name := range p.controls {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, path := range names {
		rel, err := filepath.Rel(p.dir, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("control outside project")
		}
		rel = filepath.ToSlash(rel)
		mapping[rel] = p.controls[path]
		if filepath.Dir(path) == p.dir {
			filename := filepath.Base(path)
			if !safeFile(filename) {
				return fmt.Errorf("invalid control path %s", filename)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if digest(b) != p.controls[path] {
				return fmt.Errorf("configuration changed before snapshot: %s", rel)
			}
			if err = os.WriteFile(filepath.Join(dir, filename), b, 0600); err != nil {
				return err
			}
		}
	}
	if err := writeJSON(filepath.Join(dir, "input_sha256.json"), mapping); err != nil {
		return err
	}
	return p.verifyControls()
}
