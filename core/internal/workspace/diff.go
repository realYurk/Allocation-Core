package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type Change struct {
	Path   string `json:"path"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}
type RuleDiff struct {
	RuleID  string   `json:"rule_id"`
	Left    string   `json:"left_revision"`
	Right   string   `json:"right_revision"`
	Changed int      `json:"changed_fields"`
	Changes []Change `json:"changes"`
}

func diffValue(path string, a, b any, changes *[]Change) {
	if reflect.DeepEqual(a, b) {
		return
	}
	if am, ok := a.(map[string]any); ok {
		if bm, ok := b.(map[string]any); ok {
			keys := map[string]bool{}
			for k := range am {
				keys[k] = true
			}
			for k := range bm {
				keys[k] = true
			}
			ordered := make([]string, 0, len(keys))
			for k := range keys {
				ordered = append(ordered, k)
			}
			sort.Strings(ordered)
			for _, k := range ordered {
				diffValue(path+"/"+strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1"), am[k], bm[k], changes)
			}
			return
		}
	}
	if aa, ok := a.([]any); ok {
		if bb, ok := b.([]any); ok {
			n := len(aa)
			if len(bb) > n {
				n = len(bb)
			}
			for i := 0; i < n; i++ {
				var x, y any
				if i < len(aa) {
					x = aa[i]
				}
				if i < len(bb) {
					y = bb[i]
				}
				diffValue(path+"/"+strconv.Itoa(i), x, y, changes)
			}
			return
		}
	}
	*changes = append(*changes, Change{Path: path, Before: a, After: b})
}

// DiffPublishedRules compares immutable rule revisions. No source business data is read.
func DiffPublishedRules(root, ruleID, left, right string) (RuleDiff, error) {
	if !safeID(ruleID) || len(left) != 64 || len(right) != 64 || !onlyHex(left) || !onlyHex(right) {
		return RuleDiff{}, fmt.Errorf("invalid rule reference")
	}
	get := func(rev string) (any, error) {
		path := filepath.Join(root, "rules", ruleID, rev, "rule.json")
		data, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		if hashBytes(data) != rev {
			return nil, fmt.Errorf("rule revision file is not immutable: %s", rev)
		}
		var v any
		if e = json.Unmarshal(data, &v); e != nil {
			return nil, e
		}
		return v, nil
	}
	a, e := get(left)
	if e != nil {
		return RuleDiff{}, e
	}
	b, e := get(right)
	if e != nil {
		return RuleDiff{}, e
	}
	r := RuleDiff{RuleID: ruleID, Left: left, Right: right, Changes: []Change{}}
	diffValue("", a, b, &r.Changes)
	r.Changed = len(r.Changes)
	return r, nil
}
func onlyHex(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			if r < 'a' || r > 'f' {
				return false
			}
		}
	}
	return true
}
