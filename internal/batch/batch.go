// Package batch runs independent allocation specifications concurrently.
// A single job's atomic publish/failure isolation is provided by engine.RunWithOptions.
// This package is intentionally a local batch runner, NOT a distributed scheduler.
package batch

import (
	"allocation-core/internal/engine"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const Schema = "allocation-batch/1.0"

type JobSpec struct {
	ID   string `json:"id"`
	Rule string `json:"rule"`
}
type Manifest struct {
	Schema string    `json:"schema"`
	Jobs   []JobSpec `json:"jobs"`
}
type JobResult struct {
	ID      string          `json:"id"`
	State   string          `json:"state"`
	Output  string          `json:"output,omitempty"`
	Error   string          `json:"error,omitempty"`
	Summary *engine.Summary `json:"summary,omitempty"`
}
type Report struct {
	Schema         string      `json:"schema"`
	StartedUTC     string      `json:"started_utc"`
	EndedUTC       string      `json:"ended_utc"`
	ParallelJobs   int         `json:"parallel_jobs"`
	WorkersPerJob  int         `json:"workers_per_job"`
	DiskPartitions int         `json:"disk_partitions,omitempty"`
	Jobs           []JobResult `json:"jobs"`
	Succeeded      int         `json:"succeeded"`
	Failed         int         `json:"failed"`
}

func validID(s string) bool {
	if len(s) == 0 || len(s) > 90 || s[0] == '.' || s[0] == '-' {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func Read(path string) (Manifest, error) {
	var m Manifest
	raw, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err = d.Decode(&m); err != nil {
		return m, err
	}
	if m.Schema != Schema {
		return m, fmt.Errorf("expected schema %q", Schema)
	}
	if len(m.Jobs) == 0 || len(m.Jobs) > 10000 {
		return m, fmt.Errorf("expected 1..10000 jobs")
	}
	seen := map[string]bool{}
	for _, j := range m.Jobs {
		if !validID(j.ID) || j.Rule == "" || seen[j.ID] {
			return m, fmt.Errorf("invalid/duplicate job id or empty rule: %q", j.ID)
		}
		seen[j.ID] = true
	}
	return m, nil
}
func Run(manifestPath, outputRoot string, parallelJobs, workersPerJob int) (Report, error) {
	return RunWithDisk(manifestPath, outputRoot, parallelJobs, workersPerJob, 0)
}
func RunWithDisk(manifestPath, outputRoot string, parallelJobs, workersPerJob, diskPartitions int) (Report, error) {
	report := Report{Schema: Schema, StartedUTC: time.Now().UTC().Format(time.RFC3339), ParallelJobs: parallelJobs, WorkersPerJob: workersPerJob, DiskPartitions: diskPartitions}
	if diskPartitions != 0 && (diskPartitions < 2 || diskPartitions > 64) {
		return report, fmt.Errorf("disk partitions must be 2..64")
	}
	if parallelJobs < 1 || parallelJobs > 32 || workersPerJob < 1 || workersPerJob > 128 {
		return report, fmt.Errorf("jobs must be 1..32 and workers per job 1..128")
	}
	m, err := Read(manifestPath)
	if err != nil {
		return report, err
	}
	if _, err = os.Stat(outputRoot); err == nil {
		return report, fmt.Errorf("output root already exists: %s", outputRoot)
	} else if !os.IsNotExist(err) {
		return report, err
	}
	if err = os.MkdirAll(outputRoot, 0755); err != nil {
		return report, err
	}
	// Fixed job offsets allow deterministic report ordering regardless of scheduling.
	report.Jobs = make([]JobResult, len(m.Jobs))
	base := filepath.Dir(manifestPath)
	tasks := make(chan int)
	var wg sync.WaitGroup
	count := parallelJobs
	if count > len(m.Jobs) {
		count = len(m.Jobs)
	}
	for k := 0; k < count; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range tasks {
				spec := m.Jobs[idx]
				out := filepath.Join(outputRoot, spec.ID)
				config := spec.Rule
				if !filepath.IsAbs(config) {
					config = filepath.Join(base, filepath.FromSlash(config))
				}
				var summary engine.Summary
				var e error
				options := engine.RunOptions{Workers: workersPerJob, DiskPartitions: diskPartitions}
				if diskPartitions > 0 {
					summary, e = engine.RunDiskWithOptions(config, out, options)
				} else {
					summary, e = engine.RunWithOptions(config, out, options)
				}
				r := JobResult{ID: spec.ID, Output: spec.ID}
				if e != nil {
					r.State = "FAILED"
					r.Error = e.Error()
				} else {
					r.State = "SUCCEEDED"
					r.Summary = &summary
				}
				report.Jobs[idx] = r
			}
		}()
	}
	for idx := range m.Jobs {
		tasks <- idx
	}
	close(tasks)
	wg.Wait()
	for _, j := range report.Jobs {
		if j.State == "SUCCEEDED" {
			report.Succeeded++
		} else {
			report.Failed++
		}
	}
	report.EndedUTC = time.Now().UTC().Format(time.RFC3339)
	js, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return report, e
	}
	temp := filepath.Join(outputRoot, ".batch_report.incomplete")
	if e = os.WriteFile(temp, append(js, '\n'), 0644); e != nil {
		return report, e
	}
	if e = os.Rename(temp, filepath.Join(outputRoot, "batch_report.json")); e != nil {
		return report, e
	}
	if report.Failed > 0 {
		return report, fmt.Errorf("%d/%d jobs failed; inspect batch_report.json", report.Failed, len(report.Jobs))
	}
	return report, nil
}

// IDs returns known job ids for logs without exposing arbitrary file paths.
func IDs(m Manifest) []string {
	out := []string{}
	for _, j := range m.Jobs {
		out = append(out, j.ID)
	}
	sort.Strings(out)
	return out
}
