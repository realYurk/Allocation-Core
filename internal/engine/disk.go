package engine

// Disk-backed, stage-at-a-time DAG executor. Intermediate rows are spooled to
// files, then hash-partitioned by grouping keys so every amount pool is wholly
// contained in one partition. This is a bounded-PARTITION implementation, not
// constant-memory: skewed pools and cached driver indexes still use RAM.

import (
	"allocationcore/internal/capacity"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type spoolWriter struct {
	f *os.File
	b *bufio.Writer
	e *json.Encoder
}

const defaultSpoolBufferBytes = 256 << 10

func openSpool(path string) (*spoolWriter, error) {
	return openSpoolBuffered(path, defaultSpoolBufferBytes)
}

// openSpoolBuffered is used for the short-lived partition files only. Durable
// checkpoint and audit records keep the original buffering and byte format.
func openSpoolBuffered(path string, size int) (*spoolWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	b := bufio.NewWriterSize(f, size)
	return &spoolWriter{f: f, b: b, e: json.NewEncoder(b)}, nil
}

// shardBufferBytes keeps the *total* user-space buffers bounded as the number
// of disk partitions rises. Previously 64 shards held 64*256KiB = 16MiB even
// when the source had only a handful of rows. At 64 shards this is now 2MiB.
// The file format, record order and checkpoint contract are unchanged.
func shardBufferBytes(shards int) int {
	budget := 2 << 20 // per partition operation, not per shard
	if shards <= 0 {
		return defaultSpoolBufferBytes
	}
	bytes := budget / shards
	if bytes < 16<<10 {
		bytes = 16 << 10
	}
	if bytes > defaultSpoolBufferBytes {
		bytes = defaultSpoolBufferBytes
	}
	return bytes
}
func (s *spoolWriter) put(row Row) error { return s.e.Encode(row) }
func (s *spoolWriter) close() error {
	if err := s.b.Flush(); err != nil {
		s.f.Close()
		return err
	}
	return s.f.Close()
}
func scanSpool(path string, f func(Row) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	dec := json.NewDecoder(bufio.NewReaderSize(file, 256*1024))
	for {
		var row Row
		err = dec.Decode(&row)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("spool %s: %w", path, err)
		}
		if err = f(row); err != nil {
			return err
		}
	}
}
func hashShard(k string, n int) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(k))
	return int(h.Sum64() % uint64(n))
}
func stageShardKey(row Row, st Stage, dims []string) string {
	if st.Operation == "MERGE" {
		return key(row.Coord, dims)
	}
	if !Enabled(st.Enabled) || st.Operation == "PASS" {
		return key(row.Coord, dims)
	}
	// Encode directly in dimension order. Creating a temporary coordinate map
	// for EVERY spooled row used to multiply allocations and GC pressure.
	// This retains exactly the original JSON key bytes, sharding and order.
	values := make([]string, len(dims))
	for i, d := range dims {
		if st.Source.Grouping[d] == "AS_IS" {
			values[i] = row.Coord[d]
		} else {
			values[i] = poolMember
		}
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

// scanSpoolRecord is deliberately limited to temporary, internally generated
// JSON-lines spools. It decodes a row once to find its partition, and passes
// through the ORIGINAL validated bytes without re-encoding them. Durable
// checkpoint files and public financial CSV formats remain unchanged.
// A partially written final line is rejected rather than silently published.
func scanSpoolRecord(path string, emit func(Row, []byte) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 256*1024)
	for {
		// ReadSlice borrows the reader buffer: the shard writer copies bytes
		// before the next read. Unusually large JSON records fall back to a
		// separately allocated buffer; no arbitrary row-length cutoff.
		raw, e := reader.ReadSlice('\n')
		if e == bufio.ErrBufferFull {
			oversized := append([]byte(nil), raw...)
			for e == bufio.ErrBufferFull {
				var chunk []byte
				chunk, e = reader.ReadSlice('\n')
				oversized = append(oversized, chunk...)
			}
			raw = oversized
		}
		if e == io.EOF && len(raw) == 0 {
			return nil
		}
		if e != nil && e != io.EOF {
			return fmt.Errorf("spool %s: %w", path, e)
		}
		if e == io.EOF {
			return fmt.Errorf("spool %s: incomplete final JSONL record", path)
		}
		var row Row
		if er := json.Unmarshal(raw, &row); er != nil {
			return fmt.Errorf("spool %s: invalid JSONL record: %w", path, er)
		}
		if er := emit(row, raw); er != nil {
			return er
		}
	}
}

// partitionSpool reopens the one-file inbox and creates N buckets in a single
// sequential scan, then closes all handles before consuming any bucket.
func partitionSpool(infile, prefix string, n int, shardKey func(Row) string) ([]string, error) {
	if n < 1 {
		return nil, fmt.Errorf("partition count must be positive")
	}
	paths := make([]string, n)
	writers := make([]*spoolWriter, n)
	for i := range paths {
		paths[i] = fmt.Sprintf("%s_%03d.jsonl", prefix, i)
	}
	// Initialize a writer only when a record actually belongs to its shard.
	// This avoids eagerly opening 64 files/buffers for a one-pool workload.
	// Empty shard files are still published below so existing readers and
	// checkpoint recovery see the exact same paths as previous versions.
	err := scanSpoolRecord(infile, func(row Row, raw []byte) error {
		index := hashShard(shardKey(row), n)
		w := writers[index]
		if w == nil {
			var e error
			w, e = openSpoolBuffered(paths[index], shardBufferBytes(n))
			if e != nil {
				return e
			}
			writers[index] = w
		}
		_, e := w.b.Write(raw)
		return e
	})
	// Always close every opened file. On a failure, the caller's private
	// working directory is discarded and none of these shards are published.
	for _, w := range writers {
		if w != nil {
			if e := w.close(); err == nil {
				err = e
			}
		}
	}
	if err != nil {
		return nil, err
	}
	for i, w := range writers {
		if w == nil {
			f, e := os.Create(paths[i])
			if e != nil {
				return nil, e
			}
			if e = f.Close(); e != nil {
				return nil, e
			}
		}
	}
	return paths, nil
}

// diskCoordinateAccumulator keeps only one row per full business coordinate.
// In a skewed MERGE or stage-output partition a million input records may
// collapse to a handful of coordinates. Retaining the entire []Row before
// mergeRows causes avoidable proportional heap growth. The first row's IDs
// and original coordinate map are retained to preserve row identity exactly.
type diskCoordinateAccumulator struct {
	dimensions []string
	groups     map[string]Row
}

func newDiskCoordinateAccumulator(dimensions []string) *diskCoordinateAccumulator {
	return &diskCoordinateAccumulator{dimensions: dimensions, groups: make(map[string]Row)}
}

func (a *diskCoordinateAccumulator) addWithChild(row Row) (string, error) {
	k := key(row.Coord, a.dimensions)
	existing, ok := a.groups[k]
	if ok {
		amount, e := checkedAdd(existing.Amount, row.Amount)
		if e != nil {
			return "", e
		}
		existing.Amount = amount
		a.groups[k] = existing
	} else {
		existing = row
		a.groups[k] = row
	}
	if len(existing.IDs) == 0 {
		return "", nil
	}
	return existing.IDs[0], nil
}

func (a *diskCoordinateAccumulator) add(row Row) error {
	_, e := a.addWithChild(row)
	return e
}

func (a *diskCoordinateAccumulator) sorted() []Row {
	keys := make([]string, 0, len(a.groups))
	for k := range a.groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([]Row, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, a.groups[k])
	}
	return rows
}

// streamCoalescedSpool performs the merge and produces COALESCE links in a
// SINGLE forward scan. The final merged row ID is always the first row's ID
// per coordinate, so lineage can be emitted immediately in original order.
// A million repeated records therefore need only one row per distinct
// coordinate in RAM. Published record order and checkpoints remain unchanged.
func streamCoalescedSpool(path string, dims []string, stage string, precision int, write func([]string) error) ([]Row, error) {
	agg := newDiskCoordinateAccumulator(dims)
	e := scanSpool(path, func(row Row) error {
		child, err := agg.addWithChild(row)
		if err != nil {
			return err
		}
		for _, parent := range row.IDs {
			if err := lineageLink(write, stage, "COALESCE", parent, child, row.Amount, precision); err != nil {
				return err
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return agg.sorted(), nil
}

// Streaming coalescing is deliberately limited to large candidate partitions.
// Most sparse partitions are already small and benefit from the old one-pass
// vector fast path. Sampling 256 rows keeps high-cardinality partitions on the
// old path while detecting the repeated-coordinate workloads that inflate RAM.
func shouldStreamCoalesceSpool(path string, dims []string) (bool, error) {
	info, e := os.Stat(path)
	if e != nil {
		return false, e
	}
	if info.Size() < 2<<20 {
		return false, nil
	}
	// A repeated-coordinate tail may be invisible in the first 256 rows.
	// For large partitions avoid buffering the entire []Row based on the
	// leading sample. Memory is still O(unique coordinates), not fixed.
	if info.Size() >= 16<<20 {
		return true, nil
	}
	seen := make(map[string]bool, 256)
	duplicates := 0
	const sample = 256
	stop := fmt.Errorf("finished coordinate repetition sampling")
	count := 0
	e = scanSpool(path, func(row Row) error {
		k := key(row.Coord, dims)
		if seen[k] {
			duplicates++
		} else {
			seen[k] = true
		}
		count++
		if count >= sample {
			return stop
		}
		return nil
	})
	if e != nil && e != stop {
		return false, e
	}
	return count == sample && duplicates >= 32, nil
}

// RunDiskWithOptions streams source input and stage-to-stage inboxes to disk.
// Each intermediate output is aggregated by full coordinates before routing,
// matching the single-core engine's DAG semantics even for fractional edges.
func RunDiskWithOptions(configPath, final string, options RunOptions) (summary Summary, err error) {
	if options.DiskPartitions < 2 || options.DiskPartitions > 64 {
		return summary, fail("disk partitions must be 2..64")
	}
	if options.Workers < 0 || options.Workers > 128 {
		return summary, fail("workers must be 0..128")
	}
	if options.Workers == 0 {
		options.Workers = 1
	}
	data, e := os.ReadFile(configPath)
	if e != nil {
		return summary, e
	}
	spec, e := ParseSpec(data)
	if e != nil {
		return summary, e
	}
	plan, e := Compile(spec)
	if e != nil {
		return summary, e
	}
	ruleHash := sha256.Sum256(data)
	summary = Summary{DataSHA256: map[string]string{}, Schema: Schema, Engine: "allocation-core", Name: spec.Name, RuleSHA256: hex.EncodeToString(ruleHash[:]), StartedUTC: time.Now().UTC().Format(time.RFC3339), Status: "RUNNING"}
	base := filepath.Dir(configPath)
	if final == "" {
		final = filepath.Join(base, "output")
	}
	if e := capacity.CheckEnv(final); e != nil {
		return summary, e
	}
	if _, e = os.Stat(final); e == nil {
		return summary, fail("output directory already exists: %s", final)
	} else if !os.IsNotExist(e) {
		return summary, e
	}
	if e = os.MkdirAll(filepath.Dir(final), 0755); e != nil {
		return summary, e
	}
	cp, e := initCheckpoint(data, spec, configPath, options.CheckpointDir, final, options.Resume)
	if e != nil {
		return summary, e
	}
	if cp != nil {
		// Report the COMPLETE verified input snapshot even when prior stages are reused.
		for path, hash := range cp.identity.InputSHA256 {
			summary.DataSHA256[path] = hash
		}
	}
	working, e := os.MkdirTemp(filepath.Dir(final), ".allocation-incomplete-")
	if e != nil {
		return summary, e
	}
	defer os.RemoveAll(working)
	// Shared sinks remain unpublished if any stage fails.
	head := append([]string{"terminal_stage", "row_id"}, spec.Dimensions...)
	head = append(head, "amount")
	results, e := newSink(filepath.Join(working, "results.csv"), head)
	if e != nil {
		return summary, e
	}
	defer results.abort()
	ledger, e := newSinkWithCompression(filepath.Join(working, "ledger.csv"), []string{"stage", "rule", "pool", "target", "target_row_id", "pool_amount", "share_amount", "driver_weight", "driver_denominator", "allocated_amount"}, options.EvidenceGzip)
	if e != nil {
		return summary, e
	}
	defer ledger.abort()
	membership, e := newSinkWithCompression(filepath.Join(working, "source_to_pool.csv"), []string{"stage", "pool", "upstream_row_id", "input_amount"}, options.EvidenceGzip)
	if e != nil {
		return summary, e
	}
	defer membership.abort()
	reconcile, e := newSink(filepath.Join(working, "reconciliation.csv"), []string{"stage", "pool", "input_amount", "output_amount", "delta", "status"})
	if e != nil {
		return summary, e
	}
	defer reconcile.abort()
	lineage, e := newSinkWithCompression(filepath.Join(working, "lineage_links.csv"), []string{"stage", "event", "parent_row_id", "child_row_id", "parent_amount"}, options.EvidenceGzip)
	if e != nil {
		return summary, e
	}
	defer lineage.abort()
	globalSinks := map[string]*csvSink{"results.csv": results, "ledger.csv": ledger, "source_to_pool.csv": membership, "reconciliation.csv": reconcile, "lineage_links.csv": lineage}
	headers := map[string][]string{"results.csv": head, "ledger.csv": {"stage", "rule", "pool", "target", "target_row_id", "pool_amount", "share_amount", "driver_weight", "driver_denominator", "allocated_amount"}, "source_to_pool.csv": {"stage", "pool", "upstream_row_id", "input_amount"}, "reconciliation.csv": {"stage", "pool", "input_amount", "output_amount", "delta", "status"}, "lineage_links.csv": {"stage", "event", "parent_row_id", "child_row_id", "parent_amount"}}
	type dest struct {
		stage Stage
		path  string
		w     *spoolWriter
	}
	in := map[string]*dest{}
	for i, st := range plan.Ordered {
		path := filepath.Join(working, fmt.Sprintf("in_%03d.jsonl", i))
		w, er := openSpool(path)
		if er != nil {
			return summary, er
		}
		in[st.ID] = &dest{stage: st, path: path, w: w}
	}
	defer func() {
		for _, d := range in {
			if d.w != nil {
				_ = d.w.close()
				d.w = nil
			}
		}
	}()
	routeRow := func(row Row, edges []route) error {
		shares := make([]int64, len(edges))
		names := make([]string, len(edges))
		for i, edge := range edges {
			shares[i] = edge.Share
			names[i] = edge.To
		}
		vals, er := Apportion(row.Amount, shares, names, false)
		if er != nil {
			return er
		}
		for i, val := range vals {
			if val == 0 {
				continue
			}
			d := in[edges[i].To]
			if d == nil || d.w == nil {
				return fail("stage receiver closed: %s", edges[i].To)
			}
			if er = d.w.put(Row{Coord: row.Coord, Amount: val, IDs: row.IDs}); er != nil {
				return er
			}
		}
		return nil
	}
	sources := make([]string, 0, len(spec.Datasets.Amounts))
	// Only reachable datasets count as input, matching the in-memory runner.
	for id := range spec.Datasets.Amounts {
		if len(plan.Outgoing[id]) > 0 {
			sources = append(sources, id)
		}
	}
	sort.Strings(sources)
	var originalTotal int64
	for _, source := range sources {
		path, _ := Resolve(base, spec.Datasets.Amounts[source])
		ordinal := 0
		e = StreamAmounts(path, spec.Dimensions, spec.Precision, func(row Row) error {
			ordinal++
			row.IDs = []string{sourceRowID(source, row.IDs[0], ordinal)}
			var er error
			originalTotal, er = checkedAdd(originalTotal, row.Amount)
			if er != nil {
				return er
			}
			return routeRow(row, plan.Outgoing[source])
		})
		if e != nil {
			return summary, fmt.Errorf("amount source %s: %w", source, e)
		}
		hash, er := fileSHA(path)
		if er != nil {
			return summary, er
		}
		summary.DataSHA256[path] = hash
	}
	summary.InputAmount = Money(originalTotal, spec.Precision)
	driverCache := map[string][]DriverRow{}
	var terminal int64
	for stageIdx, st := range plan.Ordered {
		d := in[st.ID]
		if er := d.w.close(); er != nil {
			return summary, er
		}
		d.w = nil
		leaf := len(plan.Outgoing[st.ID]) == 0
		cached, er := cp.load(stageIdx, st)
		if er != nil {
			return summary, er
		}
		if cached != nil {
			if leaf == (cached.SHA256["next.jsonl"] != "") {
				return summary, fail("checkpoint leaf status mismatch: %s", st.ID)
			}
			er = cp.replay(stageIdx, cached, globalSinks, func(row Row) error { return routeRow(row, plan.Outgoing[st.ID]) })
			if er != nil {
				return summary, er
			}
			summary.Stages = append(summary.Stages, cached.Stage)
			summary.LedgerRows += cached.LedgerRows
			summary.TerminalRows += cached.ResultRows
			summary.PoolChecks += cached.PoolChecks
			summary.OutputRows += cached.Stage.OutputRows
			summary.ReusedStages++
			if leaf {
				v, err2 := ParseScaled(cached.Stage.Output, spec.Precision)
				if err2 != nil {
					return summary, err2
				}
				terminal, err2 = checkedAdd(terminal, v)
				if err2 != nil {
					return summary, err2
				}
			}
			_ = os.Remove(d.path)
			continue
		}
		stageRecord, er := cp.start(stageIdx, st, headers, leaf)
		if er != nil {
			return summary, er
		}
		if stageRecord != nil {
			defer stageRecord.abort()
		}
		startLedger, startTerminal, startPools := summary.LedgerRows, summary.TerminalRows, summary.PoolChecks
		writeBoth := func(name string, sink *csvSink, fields []string) error {
			if e := sink.write(fields); e != nil {
				return e
			}
			return stageRecord.write(name, fields)
		}
		paths, er := partitionSpool(d.path, filepath.Join(working, fmt.Sprintf("stage_%03d_shard", stageIdx)), options.DiskPartitions, func(row Row) string { return stageShardKey(row, st, spec.Dimensions) })
		if er != nil {
			return summary, er
		}
		_ = os.Remove(d.path)
		// Nonterminal stage outputs are spooled, then full-coordinate-aggregated
		// per shard before applying edge shares (rounding changes otherwise).
		var stageOut *spoolWriter
		stageOutPath := filepath.Join(working, fmt.Sprintf("stage_%03d_outputs.jsonl", stageIdx))
		if !leaf {
			stageOut, er = openSpool(stageOutPath)
			if er != nil {
				return summary, er
			}
		}
		var before, after int64
		stageRows, stagePools := 0, 0
		emit := func(row Row) error {
			var e error
			after, e = checkedAdd(after, row.Amount)
			if e != nil {
				return e
			}
			stageRows++
			if !leaf {
				return stageOut.put(row)
			}
			summary.TerminalRows++
			terminal, e = checkedAdd(terminal, row.Amount)
			if e != nil {
				return e
			}
			id := ""
			if len(row.IDs) > 0 {
				id = row.IDs[0]
			}
			fields := []string{st.ID, id}
			for _, dim := range spec.Dimensions {
				fields = append(fields, row.Coord[dim])
			}
			fields = append(fields, Money(row.Amount, spec.Precision))
			return writeBoth("results.csv", results, fields)
		}
		checks := map[string]func(string) bool{}
		if Enabled(st.Enabled) && (st.Operation == "" || st.Operation == "ALLOCATE") {
			checks, er = makeChecks(st.Source.Filters, spec.Hierarchies)
			if er != nil {
				return summary, er
			}
		}
		refs := []driverRef{}
		active := []Allocation{}
		shares := []int64{}
		names := []string{}
		if Enabled(st.Enabled) && (st.Operation == "" || st.Operation == "ALLOCATE") {
			for _, a := range st.Allocations {
				if !Enabled(a.Enabled) {
					continue
				}
				path, _ := Resolve(base, spec.Datasets.Drivers[a.DriverDataset])
				ck := path + "\x00" + a.WeightField
				var idx driverRef
				var e error
				if options.DriverDiskIndex {
					prefix := filepath.Join(working, fmt.Sprintf("driver_%03d_%03d", stageIdx, len(refs)))
					idx, e = buildCachedDiskDriverIndex(path, a.WeightField, prefix, options.DriverCacheDir, a, st, spec.Dimensions, spec.Hierarchies)
				} else {
					rows, ok := driverCache[ck]
					if !ok {
						rows, er = ReadDrivers(path, spec.Dimensions, a.WeightField)
						if er != nil {
							return summary, er
						}
						driverCache[ck] = rows
					}
					idx, e = buildDriverIndex(rows, a, st, spec.Dimensions, spec.Hierarchies)
				}
				if e != nil {
					return summary, e
				}
				if options.DriverDiskIndex && options.DriverCacheDir != "" {
					if idx.cacheHit {
						summary.DriverCacheHits++
					} else {
						summary.DriverCacheBuilds++
					}
				}
				// Also close on any later failure before the working dir is removed.
				if idx.ix.disk != nil {
					defer idx.ix.disk.close()
				}
				if _, ok := summary.DataSHA256[path]; !ok {
					hash, ee := fileSHA(path)
					if ee != nil {
						return summary, ee
					}
					summary.DataSHA256[path] = hash
				}
				refs = append(refs, idx)
				active = append(active, a)
				share := a.Share
				if share == "" {
					share = "1"
				}
				v, _ := ParseScaled(share, 6)
				shares = append(shares, v)
				names = append(names, a.ID)
			}
		}
		for _, shardPath := range paths {
			addInput := func(row Row) error { var e error; before, e = checkedAdd(before, row.Amount); return e }
			if !Enabled(st.Enabled) || st.Operation == "MERGE" || st.Operation == "PASS" {
				// PASS and disabled stages stream directly, while MERGE only
				// needs to hold one full-coordinate shard in memory.
				if st.Operation == "MERGE" {
					merged := newDiskCoordinateAccumulator(spec.Dimensions)
					er = scanSpool(shardPath, func(row Row) error {
						if e := addInput(row); e != nil {
							return e
						}
						pool := key(row.Coord, spec.Dimensions)
						mergedID := rowID(st.ID, "MERGED", "", pool)
						for _, parent := range row.IDs {
							if e := lineageLink(func(fields []string) error { return writeBoth("lineage_links.csv", lineage, fields) }, st.ID, "MERGE", parent, mergedID, row.Amount, spec.Precision); e != nil {
								return e
							}
						}
						for _, id := range row.IDs {
							if e := writeBoth("source_to_pool.csv", membership, []string{st.ID, pool, id, Money(row.Amount, spec.Precision)}); e != nil {
								return e
							}
						}
						return merged.add(row)
					})
					if er != nil {
						return summary, er
					}
					shardRows := merged.sorted()
					for i := range shardRows {
						shardRows[i].IDs = []string{rowID(st.ID, "MERGED", "", key(shardRows[i].Coord, spec.Dimensions))}
						if e := emit(shardRows[i]); e != nil {
							return summary, e
						}
					}
				} else {
					er = scanSpool(shardPath, func(row Row) error {
						if e := addInput(row); e != nil {
							return e
						}
						return emit(row)
					})
					if er != nil {
						return summary, er
					}
				}
			} else {
				type bucket struct {
					coord map[string]string
					total int64
				}
				groups := map[string]*bucket{}
				er = scanSpool(shardPath, func(row Row) error {
					if e := addInput(row); e != nil {
						return e
					}
					if !sourceFilter(row, checks) {
						return emit(row)
					}
					pool := make(map[string]string, len(spec.Dimensions))
					for _, dim := range spec.Dimensions {
						if st.Source.Grouping[dim] == "AS_IS" {
							pool[dim] = row.Coord[dim]
						} else {
							pool[dim] = poolMember
						}
					}
					k := key(pool, spec.Dimensions)
					g := groups[k]
					if g == nil {
						g = &bucket{coord: pool}
						groups[k] = g
					}
					for _, id := range row.IDs {
						if e := writeBoth("source_to_pool.csv", membership, []string{st.ID, k, id, Money(row.Amount, spec.Precision)}); e != nil {
							return e
						}
					}
					var e error
					g.total, e = checkedAdd(g.total, row.Amount)
					return e
				})
				if er != nil {
					return summary, er
				}
				keys := make([]string, 0, len(groups))
				for k := range groups {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				stagePools += len(keys)
				er = parallelPools(keys, options.Workers,
					func(k string) poolResult {
						g := groups[k]
						return calculatePool(st, spec.Dimensions, spec.Immutable, spec.Precision, k, g.coord, g.total, refs, active, shares, names)
					},
					func(k string, r poolResult) error {
						for _, row := range r.outputs {
							if e := emit(row); e != nil {
								return e
							}
						}
						for _, rec := range r.ledger {
							if e := writeBoth("ledger.csv", ledger, rec); e != nil {
								return e
							}
							summary.LedgerRows++
						}
						if e := writeBoth("reconciliation.csv", reconcile, []string{st.ID, k, Money(r.input, spec.Precision), Money(r.output, spec.Precision), Money(0, spec.Precision), "PASS"}); e != nil {
							return e
						}
						summary.PoolChecks++
						return nil
					})
				if er != nil {
					return summary, er
				}
			}
			_ = os.Remove(shardPath)
		}
		for _, ref := range refs {
			if ref.ix.disk != nil {
				ref.ix.disk.close()
			}
		}
		if !leaf {
			if e := stageOut.close(); e != nil {
				return summary, e
			}
			groupPaths, e := partitionSpool(stageOutPath, filepath.Join(working, fmt.Sprintf("stage_%03d_outgroup", stageIdx)), options.DiskPartitions, func(row Row) string { return key(row.Coord, spec.Dimensions) })
			if e != nil {
				return summary, e
			}
			_ = os.Remove(stageOutPath)
			for _, path := range groupPaths {
				useStreaming, err := shouldStreamCoalesceSpool(path, spec.Dimensions)
				if err != nil {
					return summary, err
				}
				var groupRows []Row
				if useStreaming {
					groupRows, err = streamCoalescedSpool(path, spec.Dimensions, st.ID, spec.Precision, func(fields []string) error {
						return writeBoth("lineage_links.csv", lineage, fields)
					})
					if err != nil {
						return summary, err
					}
				} else {
					if err = scanSpool(path, func(row Row) error { groupRows = append(groupRows, row); return nil }); err != nil {
						return summary, err
					}
					mergedRows, mergeErr := mergeRows(groupRows, spec.Dimensions)
					if mergeErr != nil {
						return summary, mergeErr
					}
					if mergeErr := linkCoalesced(groupRows, mergedRows, spec.Dimensions, st.ID, spec.Precision, func(fields []string) error {
						return writeBoth("lineage_links.csv", lineage, fields)
					}); mergeErr != nil {
						return summary, mergeErr
					}
					groupRows = mergedRows
				}
				for _, row := range groupRows {
					if e = stageRecord.nextRow(row); e != nil {
						return summary, e
					}
					if e = routeRow(row, plan.Outgoing[st.ID]); e != nil {
						return summary, e
					}
				}
				_ = os.Remove(path)
			}
		}
		if before != after {
			return summary, fail("stage %s total mismatch %d vs %d", st.ID, before, after)
		}
		stageSummary := StageSummary{Stage: st.ID, Input: Money(before, spec.Precision), Output: Money(after, spec.Precision), Pools: stagePools, OutputRows: stageRows}
		summary.Stages = append(summary.Stages, stageSummary)
		summary.OutputRows += stageRows
		if stageRecord != nil {
			if e = stageRecord.commit(cp, stageSummary, summary.LedgerRows-startLedger, summary.TerminalRows-startTerminal, summary.PoolChecks-startPools); e != nil {
				return summary, e
			}
		}
		if options.TestStopAfterStage > 0 && stageIdx+1 >= options.TestStopAfterStage {
			return summary, fail("TEST ONLY: stopped after durable stage %d", stageIdx+1)
		}
	}
	if terminal != originalTotal {
		return summary, fail("final amount %s != original %s", Money(terminal, spec.Precision), Money(originalTotal, spec.Precision))
	}
	if e = cp.validateInputs(); e != nil {
		return summary, e
	}
	summary.OutputAmount = Money(terminal, spec.Precision)
	summary.Status = "PASS"
	for _, s := range []*csvSink{membership, ledger, reconcile, results, lineage} {
		if e = s.finish(); e != nil {
			return summary, e
		}
	}
	js, e := json.MarshalIndent(summary, "", "  ")
	if e != nil {
		return summary, e
	}
	if e = os.WriteFile(filepath.Join(working, "run_summary.json"), js, 0644); e != nil {
		return summary, e
	}
	if options.EvidenceGzip {
		if e = WriteEvidenceStreamManifest(working, results, ledger, membership, reconcile, lineage); e != nil {
			return summary, e
		}
	}
	if e = os.Rename(working, final); e != nil {
		return summary, e
	}
	return summary, nil
}
