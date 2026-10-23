package engine

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// TraceReport is a bounded, human-readable reverse path from one result row
// to its upstream pools and source rows. Streaming scans keep RAM independent
// of the number of evidence rows, at the cost of sequential IO per depth.
type TraceReport struct {
	TargetRowID   string      `json:"target_row_id"`
	Roots         []string    `json:"source_row_ids"`
	Links         []TraceEdge `json:"links"`
	NodesExamined int         `json:"nodes_examined"`
	Truncated     bool        `json:"truncated"`
	Note          string      `json:"note"`
}
type TraceEdge struct {
	Kind              string `json:"kind"`
	Stage             string `json:"stage"`
	From              string `json:"from"`
	To                string `json:"to"`
	Amount            string `json:"amount,omitempty"`
	Rule              string `json:"rule,omitempty"`
	DriverDenominator string `json:"driver_denominator,omitempty"`
}

func scanEvidence(path string, expected []string, fn func([]string) error) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	cr := csv.NewReader(f)
	head, e := cr.Read()
	if e != nil {
		return e
	}
	pos, e := HeaderPositions(head)
	if e != nil {
		return e
	}
	indexes := make([]int, len(expected))
	for i, k := range expected {
		ix, ok := pos[k]
		if !ok {
			return fmt.Errorf("%s missing evidence field %s", path, k)
		}
		indexes[i] = ix
	}
	for {
		r, er := cr.Read()
		if er == io.EOF {
			return nil
		}
		if er != nil {
			return fmt.Errorf("%s: %w", path, er)
		}
		data := make([]string, len(indexes))
		for i, ix := range indexes {
			if ix >= len(r) {
				return fmt.Errorf("%s short evidence row", path)
			}
			data[i] = r[ix]
		}
		if er = fn(data); er != nil {
			return er
		}
	}
}

// TraceRow diagnoses one result at a time. It never changes calculation files.
// maxNodes protects against unbounded fan-in in a complex allocation chain.
func TraceRow(dir, id string, maxNodes int) (TraceReport, error) {
	packed, err := openPackedTraceIndex(dir)
	if err != nil {
		return TraceReport{}, err
	}
	if packed != nil {
		return traceRowWithScanner(dir, id, maxNodes, packed.scan)
	}
	idx, err := openTraceIndex(dir)
	if err != nil {
		return TraceReport{}, err
	}
	scanner := func(file string, keys map[string]bool, fields []string, keyOf func([]string) string, fn func([]string) error) error {
		return scanIndexedEvidence(idx, dir, file, keys, fields, keyOf, fn)
	}
	return traceRowWithScanner(dir, id, maxNodes, scanner)
}

func traceRowWithScanner(dir, id string, maxNodes int, scan func(string, map[string]bool, []string, func([]string) string, func([]string) error) error) (TraceReport, error) {
	report := TraceReport{TargetRowID: id, Note: "Money attribution is represented at pools; lineage merge edges show causal inputs, not individual proportional attribution."}
	if id == "" {
		return report, fail("row ID must not be empty")
	}
	if maxNodes < 1 {
		maxNodes = 500
	}
	if maxNodes > 5000 {
		return report, fail("max nodes must be <= 5000")
	}
	frontier := map[string]bool{id: true}
	seen := map[string]bool{id: true}
	roots := map[string]bool{}
	seenEdges := map[string]bool{}
	addEdge := func(x TraceEdge) {
		sig := x.Kind + "\x00" + x.Stage + "\x00" + x.From + "\x00" + x.To + "\x00" + x.Amount
		if !seenEdges[sig] {
			seenEdges[sig] = true
			report.Links = append(report.Links, x)
		}
	}
	for depth := 0; depth < 64 && len(frontier) > 0; depth++ {
		next := map[string]bool{}
		found := map[string]bool{}
		addParent := func(parent string) {
			if parent == "" {
				return
			}
			if !seen[parent] {
				if len(seen) >= maxNodes {
					report.Truncated = true
					return
				}
				seen[parent] = true
				next[parent] = true
			}
		}
		err := scan("lineage_links.csv", frontier, []string{"stage", "event", "parent_row_id", "child_row_id", "parent_amount"}, func(r []string) string { return r[3] }, func(r []string) error {
			found[r[3]] = true
			addEdge(TraceEdge{Kind: r[1], Stage: r[0], From: r[2], To: r[3], Amount: r[4]})
			addParent(r[2])
			return nil
		})
		if err != nil {
			return report, err
		}
		pools := map[string]bool{}
		err = scan("ledger.csv", frontier, []string{"stage", "rule", "pool", "target_row_id", "allocated_amount", "driver_denominator"}, func(r []string) string { return r[3] }, func(r []string) error {
			found[r[3]] = true
			poolID := "POOL:" + r[0] + ":" + r[2]
			pools[poolID] = true
			addEdge(TraceEdge{Kind: "ALLOCATE", Stage: r[0], From: poolID, To: r[3], Amount: r[4], Rule: r[1], DriverDenominator: r[5]})
			return nil
		})
		if err != nil {
			return report, err
		}
		if len(pools) > 0 {
			err = scan("source_to_pool.csv", poolKeysForIndex(pools), []string{"stage", "pool", "upstream_row_id", "input_amount"}, func(r []string) string { return r[0] + "\x1f" + r[1] }, func(r []string) error {
				poolID := "POOL:" + r[0] + ":" + r[1]
				addEdge(TraceEdge{Kind: "SOURCE_TO_POOL", Stage: r[0], From: r[2], To: poolID, Amount: r[3]})
				addParent(r[2])
				return nil
			})
			if err != nil {
				return report, err
			}
		}
		for id := range frontier {
			if !found[id] {
				roots[id] = true
			}
		}
		frontier = next
		if report.Truncated {
			break
		}
	}
	for id := range roots {
		report.Roots = append(report.Roots, id)
	}
	sort.Strings(report.Roots)
	report.NodesExamined = len(seen)
	sort.Slice(report.Links, func(i, j int) bool {
		a, b := report.Links[i], report.Links[j]
		if a.Stage != b.Stage {
			return a.Stage < b.Stage
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.From != b.From {
			return a.From < b.From
		}
		return a.To < b.To
	})
	return report, nil
}

// FirstResultID is a convenience entry for nonprogrammers: a trace can be
// shown immediately after a demo run without manually copying a long row ID.
func FirstResultID(dir string) (string, error) {
	f, err := os.Open(filepath.Join(dir, "results.csv"))
	if err != nil {
		return "", err
	}
	defer f.Close()
	r := csv.NewReader(f)
	h, err := r.Read()
	if err != nil {
		return "", err
	}
	p, err := HeaderPositions(h)
	if err != nil {
		return "", err
	}
	idx, ok := p["row_id"]
	if !ok {
		return "", fail("results.csv lacks row_id")
	}
	data, err := r.Read()
	if err != nil {
		return "", err
	}
	if len(data) <= idx || data[idx] == "" {
		return "", fail("results.csv missing first row ID")
	}
	return data[idx], nil
}

func poolKeysForIndex(pools map[string]bool) map[string]bool {
	out := map[string]bool{}
	for pool := range pools {
		for i := 5; i < len(pool); i++ {
			if pool[i] == ':' {
				out[pool[5:i]+"\x1f"+pool[i+1:]] = true
				break
			}
		}
	}
	return out
}
