package engine

import (
	"allocationcore/internal/capacity"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Summary struct {
	DataSHA256   map[string]string `json:"data_sha256"`
	Schema       string            `json:"schema"`
	Engine       string            `json:"engine"`
	Name         string            `json:"name"`
	RuleSHA256   string            `json:"rule_sha256"`
	StartedUTC   string            `json:"started_utc"`
	Stages       []StageSummary    `json:"stages"`
	InputAmount  string            `json:"input_amount"`
	OutputAmount string            `json:"output_amount"`
	PoolChecks   int               `json:"pool_checks"`
	OutputRows   int               `json:"output_rows"`
	TerminalRows int               `json:"terminal_rows"` // final result rows, excluding intermediate stages
	LedgerRows   int               `json:"ledger_rows"`
	Status       string            `json:"status"`
	ReusedStages int               `json:"reused_stages,omitempty"`
}
type StageSummary struct {
	Stage      string `json:"stage"`
	Input      string `json:"input"`
	Output     string `json:"output"`
	Pools      int    `json:"pools"`
	OutputRows int    `json:"output_rows"`
}

// RunOptions controls bounded parallelism without changing the allocation rule schema.
// Workers=1 preserves the original sequential semantics; 0 defaults to 1.
type RunOptions struct {
	Workers            int
	DiskPartitions     int
	DriverDiskIndex    bool   // optional disk-backed external sorted driver index (disk executor only)
	DriverCacheDir     string // optional persistent content-addressed driver index cache
	CheckpointDir      string // durable completed stages, disk execution only
	Resume             bool   // allow reusing validated completed checkpoints
	TestStopAfterStage int    // tests only: abort after N completed durable stages
	EvidenceGzip       bool   // Stream lossless, integrity-recorded gzip for evidence CSV
}

// PoolResult is computed entirely off-thread. Output files are written only by
// the coordinator in sorted pool order, giving byte-identical results for all
// worker counts. Memory is bounded by one batch (2*workers pools), not by all pools.
type poolResult struct {
	outputs       []Row
	ledger        [][]string
	input, output int64
	err           error
}

func calculatePool(st Stage, dims, immutable []string, precision int, poolKey string, pool map[string]string, total int64,
	refs []driverRef, active []Allocation, shares []int64, names []string) poolResult {
	result := poolResult{input: total}
	amounts, err := Apportion(total, shares, names, false)
	if err != nil {
		result.err = err
		return result
	}
	for ix, alloc := range active {
		part := amounts[ix]
		if part == 0 {
			continue
		}
		targets, weights, ids, err := candidates(refs[ix], st, dims, pool)
		if err != nil {
			result.err = err
			return result
		}
		if len(weights) == 0 {
			result.err = fail("stage %s rule %s pool %s has no eligible drivers", st.ID, alloc.ID, poolKey)
			return result
		}
		values, err := Apportion(part, weights, ids, alloc.NegativePolicy == "ALLOW_SIGNED")
		if err != nil {
			result.err = fmt.Errorf("stage %s rule %s pool %s: %w", st.ID, alloc.ID, poolKey, err)
			return result
		}
		denom := int64(0)
		for _, weight := range weights {
			denom, err = checkedAdd(denom, weight)
			if err != nil {
				result.err = err
				return result
			}
		}
		for j, amt := range values {
			if amt == 0 {
				continue
			}
			for _, d := range immutable {
				if targets[j][d] != pool[d] {
					result.err = fail("conservation dimension %s changed", d)
					return result
				}
			}
			for _, d := range st.ConservationKeys {
				if targets[j][d] != pool[d] {
					result.err = fail("conservation dimension %s changed", d)
					return result
				}
			}
			result.output, err = checkedAdd(result.output, amt)
			if err != nil {
				result.err = err
				return result
			}
			outputID := rowID(st.ID, poolKey, alloc.ID, ids[j])
			result.outputs = append(result.outputs, Row{Coord: targets[j], Amount: amt, IDs: []string{outputID}})
			result.ledger = append(result.ledger, []string{st.ID, alloc.ID, poolKey, ids[j], outputID,
				Money(total, precision), Money(part, precision), Money(weights[j], 6), Money(denom, 6), Money(amt, precision)})
		}
	}
	if result.output != total {
		result.err = fail("stage %s pool %s not conserved: %d vs %d", st.ID, poolKey, total, result.output)
	}
	return result
}

// parallelPools uses persistent workers for the whole stage instead of
// recreating goroutines after every 2*workers pools. It retains the original
// bounded batch size, stable coordinator output order and error semantics.
// Compute callbacks must not mutate shared state without synchronization.
func parallelPools(keys []string, workerCount int, compute func(string) poolResult, consume func(string, poolResult) error) error {
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > runtime.GOMAXPROCS(0)*4 {
		workerCount = runtime.GOMAXPROCS(0) * 4
	}
	if workerCount > 32 {
		workerCount = 32
	}
	if workerCount <= 1 {
		for _, k := range keys {
			r := compute(k)
			if r.err != nil {
				return r.err
			}
			if err := consume(k, r); err != nil {
				return err
			}
		}
		return nil
	}
	if len(keys) == 0 {
		return nil
	}
	if workerCount > len(keys) {
		workerCount = len(keys)
	}
	type task struct {
		key  string
		dst  *poolResult
		done *sync.WaitGroup
	}
	jobs := make(chan task, workerCount)
	var workers sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := range jobs {
				*j.dst = compute(j.key)
				j.done.Done()
			}
		}()
	}
	defer func() { close(jobs); workers.Wait() }()
	batchSize := workerCount * 2
	for start := 0; start < len(keys); start += batchSize {
		end := start + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[start:end]
		results := make([]poolResult, len(batch))
		var done sync.WaitGroup
		done.Add(len(batch))
		for i, k := range batch {
			jobs <- task{key: k, dst: &results[i], done: &done}
		}
		done.Wait()
		for i, k := range batch {
			if results[i].err != nil {
				return results[i].err
			}
			if err := consume(k, results[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// Source IDs include dataset, user ID and row position to avoid collisions
// when multiple datasets reuse invoice numbers or CSV line numbers.
func sourceRowID(dataset, id string, ordinal int) string {
	vals, _ := json.Marshal([]any{dataset, id, ordinal})
	return string(vals)
}

// lineageLink records an edge that would otherwise be lost when several
// stage results share the same full-dimensional coordinate and are coalesced.
func lineageLink(write func([]string) error, stage, event, parent, child string, amount int64, precision int) error {
	if parent == child {
		return nil
	}
	return write([]string{stage, event, parent, child, Money(amount, precision)})
}

// linkCoalesced emits evidence for every input ID discarded by mergeRows.
// It does not change money, rounding or existing result IDs.
func linkCoalesced(input, output []Row, dims []string, stage string, precision int, write func([]string) error) error {
	target := make(map[string]string, len(output))
	for _, row := range output {
		if len(row.IDs) != 0 {
			target[key(row.Coord, dims)] = row.IDs[0]
		}
	}
	for _, row := range input {
		child := target[key(row.Coord, dims)]
		for _, parent := range row.IDs {
			if err := lineageLink(write, stage, "COALESCE", parent, child, row.Amount, precision); err != nil {
				return err
			}
		}
	}
	return nil
}

func rowID(stage, pool, rule, target string) string {
	h := sha256.New()
	for _, s := range []string{stage, pool, rule, target} {
		b := []byte(s)
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}
func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type driverIndex struct {
	bound   []string
	buckets map[string][]DriverRow
	disk    *diskDriver
}
type driverRef struct {
	alloc        Allocation
	ix           driverIndex
	targetChecks map[string]func(string) bool
}

// csvSink may transparently write an on-the-fly gzip file, preserving the
// exact original CSV byte stream (including UTF-8 BOM). It never stages a full
// plaintext ledger. Both formats use the same coordinator order and rows.
type csvSink struct {
	buf     *bufio.Writer
	f       *os.File
	w       *csv.Writer
	gz      *gzip.Writer
	digest  hash.Hash
	rawSize int64
	final   string
	temp    string
}

type digestWriter struct {
	dst  io.Writer
	sink *csvSink
}

func (d digestWriter) Write(p []byte) (int, error) {
	n, err := d.dst.Write(p)
	if n > 0 {
		_, _ = d.sink.digest.Write(p[:n])
		d.sink.rawSize += int64(n)
	}
	return n, err
}

func newSink(path string, header []string) (*csvSink, error) {
	return newSinkWithCompression(path, header, false)
}
func newSinkWithCompression(path string, header []string, compress bool) (*csvSink, error) {
	if compress {
		path += ".gz"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	tmp := path + ".incomplete"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	s := &csvSink{f: f, final: path, temp: tmp, digest: sha256.New()}
	var target io.Writer = f
	if compress {
		z, e := gzip.NewWriterLevel(f, gzip.BestSpeed)
		if e != nil {
			f.Close()
			os.Remove(tmp)
			return nil, e
		}
		s.gz = z
		target = z
	}
	s.buf = bufio.NewWriterSize(digestWriter{dst: target, sink: s}, 256*1024)
	if _, err = s.buf.Write([]byte{0xEF, 0xBB, 0xBF}); err == nil {
		s.w = csv.NewWriter(s.buf)
		err = s.w.Write(header)
	}
	if err != nil {
		s.abort()
		return nil, err
	}
	return s, nil
}
func (s *csvSink) write(fields []string) error { return s.w.Write(fields) }
func (s *csvSink) finish() error {
	s.w.Flush()
	if err := s.w.Error(); err != nil {
		s.abort()
		return err
	}
	if err := s.buf.Flush(); err != nil {
		s.abort()
		return err
	}
	if s.gz != nil {
		if err := s.gz.Close(); err != nil {
			s.abort()
			return err
		}
	}
	if err := s.f.Sync(); err != nil {
		s.abort()
		return err
	}
	if err := s.f.Close(); err != nil {
		os.Remove(s.temp)
		return err
	}
	return os.Rename(s.temp, s.final)
}
func (s *csvSink) abort() {
	if s.gz != nil {
		_ = s.gz.Close()
	}
	if s.f != nil {
		_ = s.f.Close()
	}
	_ = os.Remove(s.temp)
}

func sourceFilter(row Row, f map[string]func(string) bool) bool {
	for d, fn := range f {
		if !fn(row.Coord[d]) {
			return false
		}
	}
	return true
}
func makeChecks(fs map[string]Filter, h map[string]map[string][]string) (map[string]func(string) bool, error) {
	out := map[string]func(string) bool{}
	for d, f := range fs {
		fn, err := compileFilter(d, f, h)
		if err != nil {
			return nil, err
		}
		out[d] = fn
	}
	return out, nil
}
func buildDriverIndex(rows []DriverRow, alloc Allocation, st Stage, dims []string, h map[string]map[string][]string) (driverRef, error) {
	ref := driverRef{alloc: alloc, ix: driverIndex{buckets: map[string][]DriverRow{}}, targetChecks: map[string]func(string) bool{}}
	filters, err := makeChecks(alloc.DriverFilters, h)
	if err != nil {
		return ref, err
	}
	for d, t := range st.Target {
		if t.Mode == "INCLUDE" || t.Mode == "INCLUDE_EXCLUDE" {
			fn, err := compileFilter(d, Filter{Include: t.Include, Exclude: t.Exclude}, h)
			if err != nil {
				return ref, err
			}
			ref.targetChecks[d] = fn
		}
	}
	if len(rows) == 0 {
		return ref, nil
	}
	for _, d := range dims {
		if st.Target[d].Mode == "SOURCE" {
			if _, ok := rows[0].Coord[d]; ok {
				ref.ix.bound = append(ref.ix.bound, d)
			}
		}
	}
	for _, r := range rows {
		good := true
		for d, fn := range filters {
			v, ok := r.Coord[d]
			if !ok {
				return ref, fail("driver filter requires missing column %s", d)
			}
			if !fn(v) {
				good = false
				break
			}
		}
		if !good {
			continue
		}
		for d, fn := range ref.targetChecks {
			v, ok := r.Coord[d]
			if !ok {
				return ref, fail("target mode INCLUDE requires missing driver dimension %s", d)
			}
			if !fn(v) {
				good = false
				break
			}
		}
		if !good {
			continue
		}
		k := key(r.Coord, ref.ix.bound)
		ref.ix.buckets[k] = append(ref.ix.buckets[k], r)
	}
	return ref, nil
}
func candidates(ref driverRef, st Stage, dims []string, pool map[string]string) ([]map[string]string, []int64, []string, error) {
	bucketKey := key(pool, ref.ix.bound)
	grouped := map[string]int64{}
	targets := map[string]map[string]string{}
	process := func(dr DriverRow) error {
		target := map[string]string{}
		good := true
		for _, d := range dims {
			t := st.Target[d]
			switch t.Mode {
			case "SOURCE":
				if val, ok := dr.Coord[d]; ok && val != pool[d] {
					good = false
				}
				target[d] = pool[d]
			case "ELEMENT":
				target[d] = t.Value
			case "INCLUDE", "INCLUDE_EXCLUDE":
				target[d] = dr.Coord[d]
			}
			if !good {
				break
			}
		}
		if !good {
			return nil
		}
		w := dr.Weights[ref.alloc.WeightField]
		if w < 0 && ref.alloc.NegativePolicy != "ALLOW_SIGNED" {
			return fail("negative driver requires ALLOW_SIGNED: %s", ref.alloc.ID)
		}
		k := key(target, dims)
		sum, err := checkedAdd(grouped[k], w)
		if err != nil {
			return err
		}
		grouped[k] = sum
		targets[k] = target
		return nil
	}
	var err error
	if ref.ix.disk != nil {
		err = ref.ix.disk.forEach(bucketKey, process)
	} else {
		for _, row := range ref.ix.buckets[bucketKey] {
			if err = process(row); err != nil {
				break
			}
		}
	}
	if err != nil {
		return nil, nil, nil, err
	}
	keys := make([]string, 0, len(grouped))
	for k := range grouped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	values := make([]int64, len(keys))
	out := make([]map[string]string, len(keys))
	for i, k := range keys {
		values[i] = grouped[k]
		out[i] = targets[k]
	}
	return out, values, keys, nil
}
func routeRows(rows []Row, edges []route, inbox map[string][]Row) error {
	shares := make([]int64, len(edges))
	names := make([]string, len(edges))
	for i, e := range edges {
		shares[i] = e.Share
		names[i] = e.To
	}
	for _, r := range rows {
		values, err := Apportion(r.Amount, shares, names, false)
		if err != nil {
			return err
		}
		for i, v := range values {
			if v != 0 {
				inbox[edges[i].To] = append(inbox[edges[i].To], Row{Coord: r.Coord, Amount: v, IDs: r.IDs})
			}
		}
	}
	return nil
}
func sumRows(rows []Row) (int64, error) {
	var n int64
	for _, r := range rows {
		v, err := checkedAdd(n, r.Amount)
		if err != nil {
			return 0, err
		}
		n = v
	}
	return n, nil
}
func mergeRows(rows []Row, dims []string) ([]Row, error) {
	if len(rows) == 0 {
		return rows, nil
	}
	m := map[string]Row{}
	for _, r := range rows {
		k := key(r.Coord, dims)
		if old, ok := m[k]; ok {
			amount, err := checkedAdd(old.Amount, r.Amount)
			if err != nil {
				return nil, err
			}
			old.Amount = amount
			m[k] = old
		} else {
			m[k] = r
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Row, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out, nil
}
func writeResult(s *csvSink, stageID string, row Row, dims []string, precision int) error {
	id := ""
	if len(row.IDs) > 0 {
		id = row.IDs[0]
	}
	fields := []string{stageID, id}
	for _, d := range dims {
		fields = append(fields, row.Coord[d])
	}
	fields = append(fields, Money(row.Amount, precision))
	return s.write(fields)
}

// Run executes a validated allocation DAG. Source files are read-only. Results and evidence are atomic file outputs.
func Run(configPath, outputDir string) (Summary, error) {
	return RunWithOptions(configPath, outputDir, RunOptions{Workers: 1})
}

func RunWithOptions(configPath, outputDir string, options RunOptions) (summary Summary, runErr error) {
	if options.Workers < 0 || options.Workers > 128 {
		return summary, fail("workers must be 0..128")
	}
	if options.Workers == 0 {
		options.Workers = 1
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return summary, err
	}
	spec, err := ParseSpec(data)
	if err != nil {
		return summary, err
	}
	plan, err := Compile(spec)
	if err != nil {
		return summary, err
	}
	h := sha256.Sum256(data)
	summary = Summary{DataSHA256: map[string]string{}, Schema: Schema, Engine: "allocation-core", Name: spec.Name, RuleSHA256: hex.EncodeToString(h[:]), StartedUTC: time.Now().UTC().Format(time.RFC3339), Status: "RUNNING"}
	base := filepath.Dir(configPath)
	if outputDir == "" {
		outputDir = filepath.Join(base, "output")
	}
	if e := capacity.CheckEnv(outputDir); e != nil {
		return summary, e
	}
	if _, e := os.Stat(outputDir); e == nil {
		return summary, fail("output directory already exists, choose a new folder: %s", outputDir)
	} else if !os.IsNotExist(e) {
		return summary, e
	}
	if e := os.MkdirAll(filepath.Dir(outputDir), 0755); e != nil {
		return summary, e
	}
	workingDir, e := os.MkdirTemp(filepath.Dir(outputDir), ".allocation-incomplete-")
	if e != nil {
		return summary, e
	}
	defer os.RemoveAll(workingDir)
	outputFinalDir := outputDir
	outputDir = workingDir
	resultHead := append([]string{"terminal_stage", "row_id"}, spec.Dimensions...)
	resultHead = append(resultHead, "amount")
	out, err := newSink(filepath.Join(outputDir, "results.csv"), resultHead)
	if err != nil {
		return summary, err
	}
	defer func() {
		if runErr != nil {
			out.abort()
		}
	}()
	ledger, err := newSinkWithCompression(filepath.Join(outputDir, "ledger.csv"), []string{"stage", "rule", "pool", "target", "target_row_id", "pool_amount", "share_amount", "driver_weight", "driver_denominator", "allocated_amount"}, options.EvidenceGzip)
	if err != nil {
		return summary, err
	}
	defer func() {
		if runErr != nil {
			ledger.abort()
		}
	}()
	membership, err := newSinkWithCompression(filepath.Join(outputDir, "source_to_pool.csv"), []string{"stage", "pool", "upstream_row_id", "input_amount"}, options.EvidenceGzip)
	if err != nil {
		return summary, err
	}
	defer func() {
		if runErr != nil {
			membership.abort()
		}
	}()
	reconcile, err := newSink(filepath.Join(outputDir, "reconciliation.csv"), []string{"stage", "pool", "input_amount", "output_amount", "delta", "status"})
	if err != nil {
		return summary, err
	}
	defer func() {
		if runErr != nil {
			reconcile.abort()
		}
	}()
	lineage, err := newSinkWithCompression(filepath.Join(outputDir, "lineage_links.csv"), []string{"stage", "event", "parent_row_id", "child_row_id", "parent_amount"}, options.EvidenceGzip)
	if err != nil {
		return summary, err
	}
	defer func() {
		if runErr != nil {
			lineage.abort()
		}
	}()
	inbox := map[string][]Row{}
	orderedSources := make([]string, 0, len(plan.Outgoing))
	for k := range plan.Outgoing {
		orderedSources = append(orderedSources, k)
	}
	sort.Strings(orderedSources)
	for _, source := range orderedSources {
		edges := plan.Outgoing[source]
		if _, isStage := func() (Stage, bool) {
			for _, s := range plan.Ordered {
				if s.ID == source {
					return s, true
				}
			}
			return Stage{}, false
		}(); isStage {
			continue
		}
		path, _ := Resolve(base, spec.Datasets.Amounts[source])
		rows, err := ReadAmounts(path, spec.Dimensions, spec.Precision)
		if err != nil {
			return summary, fmt.Errorf("amount source %s: %w", source, err)
		}
		amount, err := sumRows(rows)
		if err != nil {
			return summary, err
		}
		sum, err := ParseScaled(summary.InputAmount, spec.Precision)
		if summary.InputAmount == "" {
			sum = 0
			err = nil
		}
		if err != nil {
			return summary, err
		}
		sum, err = checkedAdd(sum, amount)
		if err != nil {
			return summary, err
		}
		summary.InputAmount = Money(sum, spec.Precision)
		if _, ok := summary.DataSHA256[path]; !ok {
			if hash, er := fileSHA(path); er == nil {
				summary.DataSHA256[path] = hash
			} else {
				return summary, er
			}
		}
		for idx := range rows {
			rows[idx].IDs = []string{sourceRowID(source, rows[idx].IDs[0], idx+1)}
		}
		if err := routeRows(rows, edges, inbox); err != nil {
			return summary, fmt.Errorf("route source %s: %w", source, err)
		}
	}
	if summary.InputAmount == "" {
		summary.InputAmount = Money(0, spec.Precision)
	}
	driverCache := map[string][]DriverRow{}
	grandTotal := int64(0)
	for _, st := range plan.Ordered {
		inputs := inbox[st.ID]
		delete(inbox, st.ID)
		before, err := sumRows(inputs)
		if err != nil {
			return summary, err
		}
		leaf := len(plan.Outgoing[st.ID]) == 0
		buffered := []Row{}
		after := int64(0)
		outRows := 0
		pools := 0
		emit := func(r Row) error {
			var er error
			after, er = checkedAdd(after, r.Amount)
			if er != nil {
				return er
			}
			outRows++
			if leaf {
				summary.TerminalRows++
				if er := writeResult(out, st.ID, r, spec.Dimensions, spec.Precision); er != nil {
					return er
				}
				grandTotal, er = checkedAdd(grandTotal, r.Amount)
				return er
			}
			buffered = append(buffered, r)
			return nil
		}
		if !Enabled(st.Enabled) || st.Operation == "MERGE" || st.Operation == "PASS" {
			if st.Operation == "MERGE" {
				for _, row := range inputs {
					mergedID := rowID(st.ID, "MERGED", "", key(row.Coord, spec.Dimensions))
					for _, parent := range row.IDs {
						if er := lineageLink(lineage.write, st.ID, "MERGE", parent, mergedID, row.Amount, spec.Precision); er != nil {
							return summary, er
						}
					}
				}
				// Preserve evidence for BOTH branches before aggregating coordinates.
				for _, r := range inputs {
					pool := key(r.Coord, spec.Dimensions)
					for _, id := range r.IDs {
						if er := membership.write([]string{st.ID, pool, id, Money(r.Amount, spec.Precision)}); er != nil {
							return summary, er
						}
					}
				}
				inputs, err = mergeRows(inputs, spec.Dimensions)
				if err != nil {
					return summary, err
				}
				for i := range inputs {
					inputs[i].IDs = []string{rowID(st.ID, "MERGED", "", key(inputs[i].Coord, spec.Dimensions))}
				}
			}
			for _, r := range inputs {
				if err := emit(r); err != nil {
					return summary, err
				}
			}
		} else {
			checks, err := makeChecks(st.Source.Filters, spec.Hierarchies)
			if err != nil {
				return summary, err
			}
			type bucket struct {
				coord map[string]string
				total int64
			}
			groups := map[string]*bucket{}
			for _, row := range inputs {
				if !sourceFilter(row, checks) {
					if err := emit(row); err != nil {
						return summary, err
					}
					continue
				}
				pool := map[string]string{}
				for _, d := range spec.Dimensions {
					if st.Source.Grouping[d] == "AS_IS" {
						pool[d] = row.Coord[d]
					} else {
						pool[d] = poolMember
					}
				}
				k := key(pool, spec.Dimensions)
				g := groups[k]
				if g == nil {
					g = &bucket{coord: pool}
					groups[k] = g
				}
				for _, id := range row.IDs {
					if er := membership.write([]string{st.ID, k, id, Money(row.Amount, spec.Precision)}); er != nil {
						return summary, er
					}
				}
				g.total, err = checkedAdd(g.total, row.Amount)
				if err != nil {
					return summary, err
				}
			}
			pools = len(groups)
			refs := []driverRef{}
			active := []Allocation{}
			weightsShares := []int64{}
			names := []string{}
			for _, a := range st.Allocations {
				if !Enabled(a.Enabled) {
					continue
				}
				path, _ := Resolve(base, spec.Datasets.Drivers[a.DriverDataset])
				cacheKey := path + "\x00" + a.WeightField
				rows, ok := driverCache[cacheKey]
				if !ok {
					rows, err = ReadDrivers(path, spec.Dimensions, a.WeightField)
					if err != nil {
						return summary, err
					}
					driverCache[cacheKey] = rows
					if _, ok := summary.DataSHA256[path]; !ok {
						if hash, er := fileSHA(path); er == nil {
							summary.DataSHA256[path] = hash
						} else {
							return summary, er
						}
					}
				}
				idx, err := buildDriverIndex(rows, a, st, spec.Dimensions, spec.Hierarchies)
				if err != nil {
					return summary, err
				}
				refs = append(refs, idx)
				active = append(active, a)
				sh := a.Share
				if sh == "" {
					sh = "1"
				}
				n, _ := ParseScaled(sh, 6)
				weightsShares = append(weightsShares, n)
				names = append(names, a.ID)
			}
			keys := make([]string, 0, len(groups))
			for k := range groups {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			err = parallelPools(keys, options.Workers,
				func(k string) poolResult {
					g := groups[k]
					return calculatePool(st, spec.Dimensions, spec.Immutable, spec.Precision, k, g.coord, g.total, refs, active, weightsShares, names)
				},
				func(k string, r poolResult) error {
					for _, row := range r.outputs {
						if er := emit(row); er != nil {
							return er
						}
					}
					for _, record := range r.ledger {
						if er := ledger.write(record); er != nil {
							return er
						}
						summary.LedgerRows++
					}
					if er := reconcile.write([]string{st.ID, k, Money(r.input, spec.Precision), Money(r.output, spec.Precision), Money(0, spec.Precision), "PASS"}); er != nil {
						return er
					}
					summary.PoolChecks++
					return nil
				})
			if err != nil {
				return summary, err
			}
		}
		if before != after {
			return summary, fail("stage %s total mismatch %d vs %d", st.ID, before, after)
		}
		summary.Stages = append(summary.Stages, StageSummary{Stage: st.ID, Input: Money(before, spec.Precision), Output: Money(after, spec.Precision), Pools: pools, OutputRows: outRows})
		summary.OutputRows += outRows
		if !leaf {
			merged, err := mergeRows(buffered, spec.Dimensions)
			if err != nil {
				return summary, err
			}
			if err := linkCoalesced(buffered, merged, spec.Dimensions, st.ID, spec.Precision, lineage.write); err != nil {
				return summary, err
			}
			if err := routeRows(merged, plan.Outgoing[st.ID], inbox); err != nil {
				return summary, err
			}
		}
	}
	inputAmount, err := ParseScaled(summary.InputAmount, spec.Precision)
	if err != nil {
		return summary, err
	}
	if grandTotal != inputAmount {
		return summary, fail("DAG final total %d != input total %d", grandTotal, inputAmount)
	}
	summary.OutputAmount = Money(grandTotal, spec.Precision)
	summary.Status = "PASS"
	if err := membership.finish(); err != nil {
		return summary, err
	}
	if err := ledger.finish(); err != nil {
		return summary, err
	}
	if err := reconcile.finish(); err != nil {
		return summary, err
	}
	if err := out.finish(); err != nil {
		return summary, err
	}
	if err := lineage.finish(); err != nil {
		return summary, err
	}
	js, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return summary, err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "run_summary.json"), js, 0644); err != nil {
		return summary, err
	}
	if options.EvidenceGzip {
		if err := WriteEvidenceStreamManifest(outputDir, out, ledger, membership, reconcile, lineage); err != nil {
			return summary, err
		}
	}
	if err := os.Rename(outputDir, outputFinalDir); err != nil {
		return summary, err
	}
	return summary, nil
}

func ParseStageList(items []string) string { return strings.Join(items, ",") }
func UnitString(n int64) string            { return strconv.FormatInt(n, 10) }
