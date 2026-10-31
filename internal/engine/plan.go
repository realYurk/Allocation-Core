package engine

import (
	"fmt"
	"sort"
	"strings"
)

func Compile(s Spec) (Planned, error) {
	p := Planned{Spec: s, Outgoing: map[string][]route{}}
	if s.Schema != Schema {
		return p, fail("schema must be %s", Schema)
	}
	if s.RoundingPolicy != "" && s.RoundingPolicy != RoundingLargestRemainderIdentity {
		return p, fail("unsupported rounding_policy %q; legacy uses %s", s.RoundingPolicy, RoundingLargestRemainderIdentity)
	}
	if s.Precision < 0 || s.Precision > 6 {
		return p, fail("precision must be 0..6")
	}
	if len(s.Dimensions) == 0 || len(s.Dimensions) > 30 {
		return p, fail("expected 1..30 dimensions")
	}
	dims := map[string]bool{}
	for _, d := range s.Dimensions {
		if d == "" || dims[d] || strings.ContainsRune(d, '\x00') {
			return p, fail("invalid/duplicate dimension %q", d)
		}
		dims[d] = true
	}
	for _, d := range s.Immutable {
		if !dims[d] {
			return p, fail("unknown immutable dimension %s", d)
		}
	}
	if len(s.Stages) == 0 || len(s.Stages) > 100 {
		return p, fail("expected 1..100 stages")
	}
	stages := map[string]Stage{}
	deps := map[string]int{}
	edges := map[string][]string{}
	for _, st := range s.Stages {
		if st.ID == "" || st.ID == "$previous" || stages[st.ID].ID != "" {
			return p, fail("duplicate/invalid stage ID %s", st.ID)
		}
		if st.Operation != "" && st.Operation != "ALLOCATE" && st.Operation != "MERGE" && st.Operation != "PASS" {
			return p, fail("stage %s unknown operation %s", st.ID, st.Operation)
		}
		if s.Datasets.Amounts[st.ID] != "" {
			return p, fail("stage ID conflicts with input dataset: %s", st.ID)
		}
		stages[st.ID] = st
		deps[st.ID] = 0
		if len(st.Inputs) == 0 {
			return p, fail("stage %s has no input edges", st.ID)
		}
		for k := range st.Source.Grouping {
			if !dims[k] {
				return p, fail("stage %s unknown grouping dimension %s", st.ID, k)
			}
		}
		for k := range st.Target {
			if !dims[k] {
				return p, fail("stage %s unknown target dimension %s", st.ID, k)
			}
		}
		for _, k := range st.ConservationKeys {
			if !dims[k] {
				return p, fail("stage %s unknown conservation dimension %s", st.ID, k)
			}
		}
		for _, d := range s.Dimensions {
			g, ok := st.Source.Grouping[d]
			if !ok || (g != "AS_IS" && g != "TOTAL") {
				return p, fail("stage %s dimension %s invalid grouping", st.ID, d)
			}
			t, ok := st.Target[d]
			if !ok {
				return p, fail("stage %s dimension %s missing target", st.ID, d)
			}
			switch t.Mode {
			case "SOURCE":
				if g != "AS_IS" {
					return p, fail("stage %s: SOURCE target requires AS_IS on %s", st.ID, d)
				}
			case "ELEMENT":
				if t.Value == "" {
					return p, fail("stage %s: ELEMENT missing value on %s", st.ID, d)
				}
			case "INCLUDE", "INCLUDE_EXCLUDE":
				if len(t.Include) == 0 {
					return p, fail("stage %s: INCLUDE missing members on %s", st.ID, d)
				}
			default:
				return p, fail("stage %s: unknown target mode %s for %s", st.ID, t.Mode, d)
			}
			if (has(s.Immutable, d) || has(st.ConservationKeys, d)) && (g != "AS_IS" || t.Mode != "SOURCE") {
				return p, fail("stage %s: conservation dimension %s must be AS_IS + SOURCE", st.ID, d)
			}
		}
		for k := range st.Source.Filters {
			if !dims[k] {
				return p, fail("stage %s unknown source filter %s", st.ID, k)
			}
		}
		if Enabled(st.Enabled) && (st.Operation == "" || st.Operation == "ALLOCATE") {
			if len(st.Allocations) == 0 {
				return p, fail("stage %s has no allocations", st.ID)
			}
			shares := int64(0)
			active := 0
			seenAlloc := map[string]bool{}
			for _, a := range st.Allocations {
				if !Enabled(a.Enabled) {
					continue
				}
				active++
				if a.ID == "" || seenAlloc[a.ID] {
					return p, fail("stage %s duplicate/empty allocation rule ID %s", st.ID, a.ID)
				}
				seenAlloc[a.ID] = true
				if a.DriverDataset == "" || a.WeightField == "" || s.Datasets.Drivers[a.DriverDataset] == "" {
					return p, fail("stage %s allocation %s missing driver dataset/weight", st.ID, a.ID)
				}
				if a.NegativePolicy != "" && a.NegativePolicy != "REJECT" && a.NegativePolicy != "ALLOW_SIGNED" {
					return p, fail("stage %s invalid negative policy", st.ID)
				}
				if a.NegativePolicy == "ALLOW_SIGNED" {
					reason := strings.TrimSpace(a.SignedDriverReason)
					if len(reason) < 8 || len(reason) > 256 || strings.ContainsAny(reason, "\r\n") {
						return p, fail("stage %s allocation %s: ALLOW_SIGNED requires a business reason (8..256 chars, one line)", st.ID, a.ID)
					}
				} else if a.SignedDriverReason != "" {
					return p, fail("stage %s allocation %s: signed_driver_reason requires ALLOW_SIGNED", st.ID, a.ID)
				}
				share := a.Share
				if share == "" {
					share = "1"
				}
				n, err := ParseScaled(share, 6)
				if err != nil || n < 0 || n > unit {
					return p, fail("stage %s invalid allocation share %s", st.ID, share)
				}
				shares += n
				for k := range a.DriverFilters {
					if !dims[k] {
						return p, fail("allocation %s unknown driver filter %s", a.ID, k)
					}
				}
			}
			if active == 0 || shares != unit {
				return p, fail("stage %s allocation shares must total exactly 1", st.ID)
			}
		}
	}
	outgoingSum := map[string]int64{}
	for _, st := range s.Stages {
		seenInput := map[string]bool{}
		for _, e := range st.Inputs {
			if e.From == "" || seenInput[e.From] {
				return p, fail("stage %s duplicate/blank input edge", st.ID)
			}
			seenInput[e.From] = true
			if e.From == st.ID {
				return p, fail("stage %s self cycle", st.ID)
			}
			if _, found := stages[e.From]; found {
				deps[st.ID]++
				edges[e.From] = append(edges[e.From], st.ID)
			} else if s.Datasets.Amounts[e.From] == "" {
				return p, fail("stage %s unknown input %s", st.ID, e.From)
			}
			share := e.Share
			if share == "" {
				share = "1"
			}
			n, err := ParseScaled(share, 6)
			if err != nil || n <= 0 || n > unit {
				return p, fail("stage %s invalid edge share %s", st.ID, share)
			}
			var er error
			outgoingSum[e.From], er = checkedAdd(outgoingSum[e.From], n)
			if er != nil {
				return p, er
			}
			p.Outgoing[e.From] = append(p.Outgoing[e.From], route{To: st.ID, Share: n})
		}
	}
	for source, total := range outgoingSum {
		if total != unit {
			return p, fail("source %s outgoing shares must total exactly 1 (actual %d/1000000)", source, total)
		}
	}
	for k := range p.Outgoing {
		sort.Slice(p.Outgoing[k], func(i, j int) bool { return p.Outgoing[k][i].To < p.Outgoing[k][j].To })
	}
	ready := []string{}
	for id, n := range deps {
		if n == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		p.Ordered = append(p.Ordered, stages[id])
		for _, to := range edges[id] {
			deps[to]--
			if deps[to] == 0 {
				ready = append(ready, to)
				sort.Strings(ready)
			}
		}
	}
	if len(p.Ordered) != len(s.Stages) {
		return p, fmt.Errorf("stage graph contains a cycle")
	}
	return p, nil
}
