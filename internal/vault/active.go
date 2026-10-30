package vault

// An active pointer identifies the single official run serving one
// scenario/version. Publishing never changes calculations or archived blobs.
// Old official records remain available until an explicit retention operation.
import (
	"crypto/sha256"
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

const activeFormat = "allocation-core/active-official/1"

// ActivePointer is the currently published official run. A previous ID is
// recorded for operator convenience, not as a guarantee of infinite history.
type ActivePointer struct {
	Format        string `json:"format"`
	Scenario      string `json:"scenario"`
	Version       string `json:"version"`
	RunID         string `json:"run_id"`
	PreviousRunID string `json:"previous_run_id,omitempty"`
	PublishedUTC  string `json:"published_utc"`
}

func activePath(root, scenario, version string) string {
	key := sha256.Sum256([]byte(scenario + "\x00" + version))
	return filepath.Join(root, "active", hex.EncodeToString(key[:])+".json")
}

func readActive(root, scenario, version string) (ActivePointer, error) {
	var p ActivePointer
	if !validLabel(scenario) || !validLabel(version) {
		return p, fmt.Errorf("invalid scenario/version")
	}
	path := activePath(root, scenario, version)
	fi, err := os.Lstat(path)
	if err != nil {
		return p, err
	}
	if !fi.Mode().IsRegular() {
		return p, fmt.Errorf("unsafe active pointer: %s", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	if p.Format != activeFormat || p.Scenario != scenario || p.Version != version || !validLabel(p.RunID) || strings.ContainsAny(p.RunID, " .") || (p.PreviousRunID != "" && (!validLabel(p.PreviousRunID) || strings.ContainsAny(p.PreviousRunID, " .") || p.PreviousRunID == p.RunID)) {
		return p, fmt.Errorf("invalid active pointer: %s", path)
	}
	if _, err := time.Parse(time.RFC3339Nano, p.PublishedUTC); err != nil {
		return p, fmt.Errorf("invalid active publish timestamp: %w", err)
	}
	r, err := getRecord(root, p.RunID)
	if err != nil {
		return p, fmt.Errorf("active record missing: %w", err)
	}
	if r.Class != "official" || r.Scenario != scenario || r.Version != version {
		return p, fmt.Errorf("active record does not match pointer")
	}
	return p, nil
}

// Active returns the current published official run or os.ErrNotExist.
func Active(root, scenario, version string) (ActivePointer, error) {
	return readActive(root, scenario, version)
}

// Publish uses an explicit compare-and-swap lease on the expected previous
// run ID. For an initially empty pointer, expected must be the literal "none".
// Publish is serialized with all other vault mutations by the vault lock.
func Publish(root, scenario, version, runID, expected string) (ActivePointer, error) {
	var out ActivePointer
	if !validLabel(scenario) || !validLabel(version) || expected == "" {
		return out, fmt.Errorf("scenario/version and --expect are required")
	}
	if expected != "none" && (!validLabel(expected) || strings.ContainsAny(expected, " .")) {
		return out, fmt.Errorf("invalid expected run ID")
	}
	unlock, err := lock(root)
	if err != nil {
		return out, err
	}
	defer unlock()
	current, err := readActive(root, scenario, version)
	if errors.Is(err, os.ErrNotExist) {
		if expected != "none" {
			return out, fmt.Errorf("active pointer absent; expected %s", expected)
		}
	} else if err != nil {
		return out, err
	} else {
		if expected != current.RunID {
			return out, fmt.Errorf("active pointer changed: expected %s, actual %s", expected, current.RunID)
		}
	}
	record, err := getRecord(root, runID)
	if err != nil {
		return out, err
	}
	if record.Class != "official" || record.Scenario != scenario || record.Version != version {
		return out, fmt.Errorf("publish requires an official record with matching scenario/version")
	}
	// Verify evidence bytes before pointing the official entry at them.
	if err = Verify(root, runID); err != nil {
		return out, fmt.Errorf("cannot publish corrupt run: %w", err)
	}
	if current.RunID == runID {
		return current, nil
	}
	out = ActivePointer{Format: activeFormat, Scenario: scenario, Version: version, RunID: runID, PreviousRunID: current.RunID, PublishedUTC: time.Now().UTC().Format(time.RFC3339Nano)}
	if err = atomicJSON(activePath(root, scenario, version), out); err != nil {
		return ActivePointer{}, err
	}
	return out, nil
}

// activeIndex refuses to prune if ANY current pointer is malformed or points
// to a missing/wrong record. Only files named by the canonical SHA-256 format
// are accepted, and symlink pointers are rejected.
func activeIndex(root string) (map[string]ActivePointer, error) {
	index := map[string]ActivePointer{}
	dir := filepath.Join(root, "active")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return index, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		fi, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("unsafe active entry: %s", path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var p ActivePointer
		if err = json.Unmarshal(b, &p); err != nil {
			return nil, err
		}
		if !validLabel(p.Scenario) || !validLabel(p.Version) || path != activePath(root, p.Scenario, p.Version) {
			return nil, fmt.Errorf("noncanonical active entry: %s", path)
		}
		checked, err := readActive(root, p.Scenario, p.Version)
		if err != nil {
			return nil, err
		}
		index[p.Scenario+"\x00"+p.Version] = checked
	}
	return index, nil
}

type OfficialPrunePlan struct {
	DryRun           bool     `json:"dry_run"`
	RemoveIDs        []string `json:"remove_ids"`
	ProtectedActive  int      `json:"protected_active"`
	ProtectedHistory int      `json:"protected_history"`
	ProtectedDrafts  int      `json:"protected_drafts"`
	ReclaimableBytes int64    `json:"reclaimable_bytes"`
	RemainingRuns    int      `json:"remaining_runs"`
}

// PruneOfficials only removes historical official records AFTER each group has
// an explicit active pointer. keepPrevious is a minimum number of rollback
// generations per scenario/version, and olderThanDays provides an age floor.
// No draft is ever deleted. All official records without an active pointer
// cause fail-closed refusal, including in dry-run mode.
func PruneOfficials(root string, keepPrevious, olderThanDays int, apply bool) (OfficialPrunePlan, error) {
	plan := OfficialPrunePlan{DryRun: !apply, RemoveIDs: []string{}}
	if keepPrevious < 0 || olderThanDays < 0 {
		return plan, fmt.Errorf("retention values must be nonnegative")
	}
	unlock, err := lock(root)
	if err != nil {
		return plan, err
	}
	defer unlock()
	runs, err := List(root)
	if err != nil {
		return plan, err
	}
	pointers, err := activeIndex(root)
	if err != nil {
		return plan, err
	}
	groups := map[string][]Record{}
	keep := []Record{}
	remove := []Record{}
	for _, r := range runs {
		if r.Class == "draft" {
			plan.ProtectedDrafts++
			keep = append(keep, r)
			continue
		}
		key := r.Scenario + "\x00" + r.Version
		if _, ok := pointers[key]; !ok {
			return plan, fmt.Errorf("refusing historical official prune: %s / %s lacks an active pointer", r.Scenario, r.Version)
		}
		groups[key] = append(groups[key], r)
	}
	cutoff := time.Now().UTC().Add(-time.Duration(olderThanDays) * 24 * time.Hour)
	for key, records := range groups {
		current := pointers[key].RunID
		historicalKept := 0
		for _, r := range records { // List is sorted newest-first.
			if r.ID == current {
				plan.ProtectedActive++
				keep = append(keep, r)
				continue
			}
			date, err := time.Parse(time.RFC3339Nano, r.CreatedUTC)
			if err != nil {
				return plan, err
			}
			if historicalKept < keepPrevious || date.After(cutoff) {
				plan.ProtectedHistory++
				keep = append(keep, r)
			} else {
				remove = append(remove, r)
				plan.RemoveIDs = append(plan.RemoveIDs, r.ID)
			}
			historicalKept++
		}
	}
	// Count shared objects exactly, not summing duplicate logical sizes.
	keepPath := map[string]bool{}
	for _, r := range keep {
		for _, ref := range r.Files {
			keepPath[objectPath(root, ref)] = true
		}
	}
	doomed := map[string]int64{}
	for _, r := range remove {
		for _, ref := range r.Files {
			doomed[objectPath(root, ref)] = ref.StoredBytes
		}
	}
	for path, size := range doomed {
		if !keepPath[path] {
			plan.ReclaimableBytes += size
		}
	}
	plan.RemainingRuns = len(keep)
	sort.Strings(plan.RemoveIDs)
	if !apply {
		return plan, nil
	}
	for _, r := range remove {
		if err = os.Remove(filepath.Join(root, "runs", r.ID+".json")); err != nil {
			return plan, err
		}
	}
	// Delete only unshared objects; interrupted cleanup can be recovered by gc.
	for path := range doomed {
		if !keepPath[path] {
			if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return plan, err
			}
		}
	}
	return plan, nil
}
