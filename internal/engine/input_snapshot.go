package engine

// Each execution consumes private byte-for-byte input snapshots. The digest
// recorded in the summary is derived while copying those very bytes, and each
// source is checked again after its copy. This provides per-file snapshot
// identity; it does not claim a cross-file database transaction.
import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type inputSnapshot struct {
	dir                string
	originalToSnapshot map[string]string
	hashes             map[string]string
}

func (s *inputSnapshot) close() {
	if s != nil {
		_ = os.RemoveAll(s.dir)
	}
}

type snapshotError struct {
	message string
	cause   error
}

func (e *snapshotError) Error() string { return e.message }
func (e *snapshotError) Unwrap() error { return e.cause }
func (s *inputSnapshot) restoreError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for original, captured := range s.originalToSnapshot {
		message = strings.ReplaceAll(message, captured, original)
	}
	if message == err.Error() {
		return err
	}
	return &snapshotError{message: message, cause: err}
}

func snapshotInputs(spec *Spec, plan Planned, base, parent string, expected map[string]string) (snap *inputSnapshot, err error) {
	dir, e := os.MkdirTemp(parent, ".allocation-inputs-")
	if e != nil {
		return nil, e
	}
	abs, e := filepath.Abs(dir)
	if e != nil {
		_ = os.RemoveAll(dir)
		return nil, e
	}
	dir = abs
	snap = &inputSnapshot{dir: dir, originalToSnapshot: map[string]string{}, hashes: map[string]string{}}
	created := snap
	defer func() {
		if err != nil {
			created.close()
		}
	}()
	sources := map[string]bool{}
	drivers := map[string]bool{}
	for name := range spec.Datasets.Amounts {
		if len(plan.Outgoing[name]) > 0 {
			sources[name] = true
		}
	}
	for _, st := range plan.Ordered {
		if !Enabled(st.Enabled) || (st.Operation != "" && st.Operation != "ALLOCATE") {
			continue
		}
		for _, a := range st.Allocations {
			if Enabled(a.Enabled) {
				drivers[a.DriverDataset] = true
			}
		}
	}
	type item struct{ kind, name, path string }
	items := []item{}
	for name := range sources {
		p, e := Resolve(base, spec.Datasets.Amounts[name])
		if e != nil {
			return nil, e
		}
		items = append(items, item{"amount", name, p})
	}
	for name := range drivers {
		p, e := Resolve(base, spec.Datasets.Drivers[name])
		if e != nil {
			return nil, e
		}
		items = append(items, item{"driver", name, p})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].path != items[j].path {
			return items[i].path < items[j].path
		}
		if items[i].kind != items[j].kind {
			return items[i].kind < items[j].kind
		}
		return items[i].name < items[j].name
	})
	amounts := make(map[string]string, len(spec.Datasets.Amounts))
	for k, v := range spec.Datasets.Amounts {
		amounts[k] = v
	}
	driverPaths := make(map[string]string, len(spec.Datasets.Drivers))
	for k, v := range spec.Datasets.Drivers {
		driverPaths[k] = v
	}
	for _, it := range items {
		dest, ok := snap.originalToSnapshot[it.path]
		if !ok {
			src, e := os.Open(it.path)
			if e != nil {
				return nil, fmt.Errorf("snapshot %s %s: %w", it.kind, it.name, e)
			}
			info, e := src.Stat()
			if e != nil || !info.Mode().IsRegular() {
				src.Close()
				return nil, fmt.Errorf("snapshot source is not a regular file: %s", it.path)
			}
			dest = filepath.Join(dir, fmt.Sprintf("input-%06d.csv", len(snap.hashes)))
			out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				src.Close()
				return nil, e
			}
			h := sha256.New()
			_, e = io.Copy(out, io.TeeReader(src, h))
			ce := out.Close()
			src.Close()
			if e != nil {
				return nil, e
			}
			if ce != nil {
				return nil, ce
			}
			hash := hex.EncodeToString(h.Sum(nil))
			after, e := fileSHA(it.path)
			if e != nil || hash != after {
				return nil, fmt.Errorf("input changed while taking snapshot: %s", it.path)
			}
			if expected != nil && expected[it.path] != hash {
				return nil, fmt.Errorf("checkpoint input changed while taking snapshot: %s", it.path)
			}
			snap.originalToSnapshot[it.path] = dest
			snap.hashes[it.path] = hash
		}
		if it.kind == "amount" {
			amounts[it.name] = dest
		} else {
			driverPaths[it.name] = dest
		}
	}
	spec.Datasets.Amounts = amounts
	spec.Datasets.Drivers = driverPaths
	return snap, nil
}
