package engine

// Durable, content-verified stage checkpoints for the disk executor.
// Checkpoints are an optimization, never a trusted source of truth: the rule
// and ALL referenced input files are re-hashed on resume, each cached file
// is re-hashed before replay, and final output is only published on success.

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const checkpointFormat = "allocation-core/stage-checkpoint-v16/2"

type checkpointIdentity struct {
	Format           string            `json:"format"`
	InputFingerprint string            `json:"input_fingerprint"`
	RuleSHA256       string            `json:"rule_sha256"`
	InputSHA256      map[string]string `json:"input_sha256"`
}

type stageReceipt struct {
	Format     string            `json:"format"`
	ID         string            `json:"stage_id"`
	Position   int               `json:"position"`
	Stage      StageSummary      `json:"stage_summary"`
	LedgerRows int               `json:"ledger_rows"`
	ResultRows int               `json:"terminal_rows"`
	PoolChecks int               `json:"pool_checks"`
	SHA256     map[string]string `json:"file_sha256"`
}

type checkpointManager struct {
	root     string
	identity checkpointIdentity
}

func initCheckpoint(ruleData []byte, spec Spec, configPath, dir, final string, resume bool) (*checkpointManager, error) {
	if dir == "" {
		if resume {
			return nil, fail("--resume requires --checkpoint-dir")
		}
		return nil, nil
	}
	absRoot, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	absFinal, err := filepath.Abs(final)
	if err != nil {
		return nil, err
	}
	rootPrefix := absRoot + string(os.PathSeparator)
	finalPrefix := absFinal + string(os.PathSeparator)
	if absRoot == absFinal || strings.HasPrefix(absRoot, finalPrefix) || strings.HasPrefix(absFinal, rootPrefix) {
		return nil, fail("checkpoint directory must be separate from output directory")
	}
	h := sha256.Sum256(ruleData)
	identity := checkpointIdentity{Format: checkpointFormat, RuleSHA256: hex.EncodeToString(h[:]), InputSHA256: map[string]string{}}
	paths := map[string]bool{}
	base := filepath.Dir(configPath)
	for _, p := range spec.Datasets.Amounts {
		q, e := Resolve(base, p)
		if e != nil {
			return nil, e
		}
		paths[q] = true
	}
	for _, p := range spec.Datasets.Drivers {
		q, e := Resolve(base, p)
		if e != nil {
			return nil, e
		}
		paths[q] = true
	}
	keys := make([]string, 0, len(paths))
	for p := range paths {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, p := range keys {
		hash, e := fileSHA(p)
		if e != nil {
			return nil, e
		}
		identity.InputSHA256[p] = hash
	}
	data, _ := json.Marshal(identity)
	sum := sha256.Sum256(data)
	identity.InputFingerprint = hex.EncodeToString(sum[:])
	manager := &checkpointManager{root: absRoot, identity: identity}
	manifest := filepath.Join(absRoot, "identity.json")
	old, e := os.ReadFile(manifest)
	if e == nil {
		if !resume {
			return nil, fail("checkpoint already initialized: use --resume or a fresh --checkpoint-dir")
		}
		var previous checkpointIdentity
		if e = json.Unmarshal(old, &previous); e != nil {
			return nil, fail("invalid checkpoint identity: %v", e)
		}
		if previous.Format != identity.Format || previous.RuleSHA256 != identity.RuleSHA256 || previous.InputFingerprint != identity.InputFingerprint {
			return nil, fail("checkpoint fingerprint mismatch: rule or source/driver data changed; use a NEW checkpoint directory")
		}
	} else {
		if !os.IsNotExist(e) {
			return nil, e
		}
		if resume {
			return nil, fail("checkpoint does not exist: %s", manifest)
		}
		if e = os.MkdirAll(absRoot, 0755); e != nil {
			return nil, e
		}
		data, _ = json.MarshalIndent(identity, "", "  ")
		tmp := manifest + ".incomplete"
		if e = os.WriteFile(tmp, data, 0644); e != nil {
			return nil, e
		}
		if e = os.Rename(tmp, manifest); e != nil {
			return nil, e
		}
	}
	return manager, nil
}

func (c *checkpointManager) stagePath(i int) string {
	return filepath.Join(c.root, fmt.Sprintf("stage_%03d", i))
}
func (c *checkpointManager) load(i int, st Stage) (*stageReceipt, error) {
	if c == nil {
		return nil, nil
	}
	dir := c.stagePath(i)
	content, err := os.ReadFile(filepath.Join(dir, "receipt.json"))
	if os.IsNotExist(err) {
		if _, e := os.Stat(dir); e == nil {
			return nil, fail("incomplete stage checkpoint: %s", dir)
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r stageReceipt
	if err = json.Unmarshal(content, &r); err != nil {
		return nil, err
	}
	if r.Format != checkpointFormat || r.ID != st.ID || r.Position != i {
		return nil, fail("checkpoint stage mismatch at %d", i)
	}
	for name, expected := range r.SHA256 {
		if strings.ContainsAny(name, "/\\") || name == ".." {
			return nil, fail("invalid checkpoint filename")
		}
		actual, e := fileSHA(filepath.Join(dir, name))
		if e != nil {
			return nil, e
		}
		if actual != expected {
			return nil, fail("checkpoint file SHA-256 mismatch: %s", name)
		}
	}
	required := []string{"results.csv", "ledger.csv", "source_to_pool.csv", "reconciliation.csv", "lineage_links.csv"}
	if _, ok := r.SHA256["next.jsonl"]; ok {
		required = append(required, "next.jsonl")
	}
	for _, name := range required {
		if r.SHA256[name] == "" {
			return nil, fail("missing checkpoint artifact %s", name)
		}
	}
	return &r, nil
}

type stageRecorder struct {
	dir      string
	position int
	id       string
	sinks    map[string]*csvSink
	next     *spoolWriter
	closed   bool
}

func (c *checkpointManager) start(i int, st Stage, headers map[string][]string, leaf bool) (*stageRecorder, error) {
	if c == nil {
		return nil, nil
	}
	dir, err := os.MkdirTemp(c.root, ".pending-stage-")
	if err != nil {
		return nil, err
	}
	r := &stageRecorder{dir: dir, position: i, id: st.ID, sinks: map[string]*csvSink{}}
	for _, name := range []string{"results.csv", "ledger.csv", "source_to_pool.csv", "reconciliation.csv", "lineage_links.csv"} {
		sink, e := newSink(filepath.Join(dir, name), headers[name])
		if e != nil {
			r.abort()
			return nil, e
		}
		r.sinks[name] = sink
	}
	if !leaf {
		r.next, err = openSpool(filepath.Join(dir, "next.jsonl"))
		if err != nil {
			r.abort()
			return nil, err
		}
	}
	return r, nil
}
func (r *stageRecorder) write(name string, fields []string) error {
	if r == nil {
		return nil
	}
	return r.sinks[name].write(fields)
}
func (r *stageRecorder) nextRow(row Row) error {
	if r == nil || r.next == nil {
		return nil
	}
	return r.next.put(row)
}
func (r *stageRecorder) abort() {
	if r == nil || r.closed {
		return
	}
	for _, s := range r.sinks {
		s.abort()
	}
	if r.next != nil {
		_ = r.next.close()
	}
	_ = os.RemoveAll(r.dir)
	r.closed = true
}
func (r *stageRecorder) commit(c *checkpointManager, summary StageSummary, ledgerRows, resultRows, pools int) error {
	if r == nil {
		return nil
	}
	if r.next != nil {
		if e := r.next.close(); e != nil {
			return e
		}
		r.next = nil
	}
	files := []string{"results.csv", "ledger.csv", "source_to_pool.csv", "reconciliation.csv", "lineage_links.csv"}
	for _, name := range files {
		if e := r.sinks[name].finish(); e != nil {
			return e
		}
	}
	if _, e := os.Stat(filepath.Join(r.dir, "next.jsonl")); e == nil {
		files = append(files, "next.jsonl")
	}
	hashes := map[string]string{}
	for _, name := range files {
		h, e := fileSHA(filepath.Join(r.dir, name))
		if e != nil {
			return e
		}
		hashes[name] = h
	}
	receipt := stageReceipt{Format: checkpointFormat, ID: r.id, Position: r.position, Stage: summary, LedgerRows: ledgerRows, ResultRows: resultRows, PoolChecks: pools, SHA256: hashes}
	data, e := json.MarshalIndent(receipt, "", "  ")
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(r.dir, "receipt.json"), data, 0644); e != nil {
		return e
	}
	if e = os.Rename(r.dir, c.stagePath(r.position)); e != nil {
		return e
	}
	r.closed = true
	return nil
}
func replayCSV(path string, sink *csvSink) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	reader := csv.NewReader(f)
	if _, e = reader.Read(); e != nil {
		return e
	}
	for {
		record, e := reader.Read()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		if e = sink.write(record); e != nil {
			return e
		}
	}
}
func (c *checkpointManager) replay(i int, rec *stageReceipt, sinks map[string]*csvSink, next func(Row) error) error {
	dir := c.stagePath(i)
	for _, name := range []string{"results.csv", "ledger.csv", "source_to_pool.csv", "reconciliation.csv", "lineage_links.csv"} {
		if e := replayCSV(filepath.Join(dir, name), sinks[name]); e != nil {
			return e
		}
	}
	if rec.SHA256["next.jsonl"] != "" {
		if e := scanSpool(filepath.Join(dir, "next.jsonl"), next); e != nil {
			return e
		}
	}
	return nil
}

// validateInputs prevents publishing a resumed result based on files that were
// modified after fingerprint creation. Input data must remain read-only while
// a checkpointed run is active.
func (c *checkpointManager) validateInputs() error {
	if c == nil {
		return nil
	}
	for path, want := range c.identity.InputSHA256 {
		got, e := fileSHA(path)
		if e != nil {
			return e
		}
		if got != want {
			return fail("input was modified during execution: %s", path)
		}
	}
	return nil
}
