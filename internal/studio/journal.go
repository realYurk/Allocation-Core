package studio

// Durable job journal: it stores the execution state, not an engine checkpoint.
// A crashed/forcibly stopped Studio never silently upgrades an old task to PASS.
import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func safeJobID(id string) bool {
	if len(id) != 24 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && id == strings.ToLower(id)
}

func journalName(multi bool) string {
	if multi {
		return "multi_job.json"
	}
	return "studio_job.json"
}

// atomicJobUpdate persists a complete state transition before responding to
// the caller, with a synced temp file and rename into the private job folder.
func atomicJobUpdate(j *Job, multi bool) error {
	if j == nil || !safeJobID(j.ID) {
		return errors.New("invalid journal job ID")
	}
	journalFolder := filepath.Join(filepath.Dir(j.Output)+"-journal", j.ID)
	if err := os.MkdirAll(journalFolder, 0700); err != nil {
		return err
	}
	copyJ := *j
	// Long diagnostics belong in engine evidence; keep the state journal bounded.
	if len(copyJ.Log) > 4096 {
		copyJ.Log = copyJ.Log[:4096]
	}
	raw, err := json.MarshalIndent(copyJ, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(journalFolder, ".job-state-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), filepath.Join(journalFolder, journalName(multi))); err != nil {
		return err
	}
	// Best-effort directory durability, operating systems differ on fsync(dir).
	if d, e := os.Open(journalFolder); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func readJournal(root, id string, multi bool) (Job, error) {
	if !safeJobID(id) {
		return Job{}, errors.New("invalid job id")
	}
	path := filepath.Join(root+"-journal", id, journalName(multi))
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		path = filepath.Join(root, id, journalName(multi))
	}
	st, err := os.Lstat(path)
	if err != nil {
		return Job{}, err
	}
	if !st.Mode().IsRegular() || st.Size() > 128*1024 {
		return Job{}, errors.New("invalid journal type/size")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Job{}, err
	}
	var j Job
	if err = json.Unmarshal(raw, &j); err != nil {
		return Job{}, err
	}
	if j.ID != id || j.Case == "" || j.Start == "" {
		return Job{}, errors.New("journal content mismatch")
	}
	j.Output = filepath.Join(root, id) // NEVER trust an on-disk path.
	return j, nil
}

func terminalJob(status string) bool {
	switch status {
	case "PASS", "FAILED", "CANCELED", "INTERRUPTED":
		return true
	}
	return false
}

func recoverJournals(root string, multi bool) (map[string]*Job, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	out := map[string]*Job{}
	for _, scanRoot := range []string{root, root + "-journal"} {
		entries, err := os.ReadDir(scanRoot)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			id := entry.Name()
			if !entry.IsDir() || !safeJobID(id) || out[id] != nil {
				continue
			}
			j, err := readJournal(root, id, multi)
			if err != nil {
				continue
			}
			if !terminalJob(j.Status) {
				old := j.Status
				j.Status = "INTERRUPTED"
				j.End = time.Now().UTC().Format(time.RFC3339)
				j.Error = fmt.Sprintf("Studio restarted while job was %s; NOT automatically resumed or published. Retry creates a new run with a fresh input snapshot.", old)
				if err := atomicJobUpdate(&j, multi); err != nil {
					return nil, fmt.Errorf("cannot persist recovery of %s: %w", id, err)
				}
			}
			if j.Status == "PASS" {
				raw, e := os.ReadFile(filepath.Join(j.Output, "run_summary.json"))
				var v struct {
					Status string `json:"status"`
				}
				if e != nil || json.Unmarshal(raw, &v) != nil || v.Status != "PASS" {
					j.Status = "INTERRUPTED"
					j.End = time.Now().UTC().Format(time.RFC3339)
					j.Error = "Stored PASS cannot be verified: authoritative run_summary.json missing or not PASS."
					if err := atomicJobUpdate(&j, multi); err != nil {
						return nil, err
					}
				}
			}
			cp := j
			out[id] = &cp
		}
	}
	return out, nil
}

func historicalJobs(root string, current map[string]*Job, multi bool, limit int) []*Job {
	out := make([]*Job, 0, len(current))
	known := make(map[string]bool, len(current))
	for _, j := range current {
		cp := *j
		cp.Log = ""
		known[j.ID] = true
		out = append(out, &cp)
	}
	for _, scanRoot := range []string{root, root + "-journal"} {
		entries, _ := os.ReadDir(scanRoot)
		for _, e := range entries {
			id := e.Name()
			if !e.IsDir() || known[id] || !safeJobID(id) {
				continue
			}
			saved, err := readJournal(root, id, multi)
			if err != nil {
				continue
			}
			saved.Log = ""
			known[id] = true
			out = append(out, &saved)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start > out[j].Start })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
