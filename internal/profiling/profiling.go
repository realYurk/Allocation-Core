// Package profiling measures complete allocation tasks (not just microbenchmarks).
// Profiles and file-system metrics stay local; never upload them to GitHub.
package profiling

import (
	"allocationcore/internal/engine"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"
)

type Report struct {
	Status             string     `json:"status"`
	Rule               string     `json:"rule"`
	ElapsedMS          int64      `json:"elapsed_ms"`
	PeakHeapAllocBytes uint64     `json:"sampled_peak_heap_alloc_bytes"`
	AllocatedBytes     uint64     `json:"allocated_bytes_during_run"`
	FinalHeapInUse     uint64     `json:"final_heap_inuse_bytes"`
	OutputBytes        int64      `json:"persisted_output_bytes"`
	Files              int        `json:"persisted_output_files"`
	ProcessIOStart     *ProcessIO `json:"process_io_start,omitempty"`
	ProcessIOEnd       *ProcessIO `json:"process_io_end,omitempty"`
	CPUProfile         string     `json:"cpu_profile"`
	HeapProfile        string     `json:"heap_profile"`
	EngineStatus       string     `json:"engine_status"`
	Note               string     `json:"note"`
}

func Measure(rule, out string, opts engine.RunOptions) (Report, error) {
	report := Report{Rule: rule, Status: "FAILED", Note: "Heap high-watermark sampled every 20ms; process IO only where OS exposes it; CPU profiling changes runtime cost"}
	if _, e := os.Stat(out); e == nil {
		return report, fmt.Errorf("output exists: %s", out)
	} else if !os.IsNotExist(e) {
		return report, e
	}
	profDir := out + ".profile"
	if _, e := os.Stat(profDir); e == nil {
		return report, fmt.Errorf("profile directory exists: %s", profDir)
	} else if !os.IsNotExist(e) {
		return report, e
	}
	if e := os.MkdirAll(profDir, 0700); e != nil {
		return report, e
	}
	report.CPUProfile = filepath.Join(profDir, "cpu.pprof")
	report.HeapProfile = filepath.Join(profDir, "heap.pprof")
	cpu, e := os.Create(report.CPUProfile)
	if e != nil {
		return report, e
	}
	if e = pprof.StartCPUProfile(cpu); e != nil {
		cpu.Close()
		return report, e
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	report.ProcessIOStart = readProcessIO()
	peak := before.HeapAlloc
	var guard sync.Mutex
	done := make(chan struct{})
	var stopped sync.WaitGroup
	stopped.Add(1)
	go func() {
		defer stopped.Done()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				guard.Lock()
				if m.HeapAlloc > peak {
					peak = m.HeapAlloc
				}
				guard.Unlock()
			}
		}
	}()
	start := time.Now()
	var summary engine.Summary
	var runErr error
	if opts.DiskPartitions >= 2 {
		summary, runErr = engine.RunDiskWithOptions(rule, out, opts)
	} else {
		summary, runErr = engine.RunWithOptions(rule, out, opts)
	}
	report.ElapsedMS = time.Since(start).Milliseconds()
	close(done)
	stopped.Wait()
	pprof.StopCPUProfile()
	if e := cpu.Close(); e != nil && runErr == nil {
		runErr = e
	}
	runtime.ReadMemStats(&after)
	guard.Lock()
	if after.HeapAlloc > peak {
		peak = after.HeapAlloc
	}
	report.PeakHeapAllocBytes = peak
	guard.Unlock()
	if after.TotalAlloc >= before.TotalAlloc {
		report.AllocatedBytes = after.TotalAlloc - before.TotalAlloc
	}
	report.FinalHeapInUse = after.HeapInuse
	report.ProcessIOEnd = readProcessIO()
	report.EngineStatus = summary.Status
	// Heap profile captures allocations still alive at the end of the run.
	heap, e := os.Create(report.HeapProfile)
	if e != nil && runErr == nil {
		runErr = e
	} else if e == nil {
		if e = pprof.WriteHeapProfile(heap); e != nil && runErr == nil {
			runErr = e
		}
		if e = heap.Close(); e != nil && runErr == nil {
			runErr = e
		}
	}
	if runErr == nil {
		e = filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, e := d.Info()
			if e != nil {
				return e
			}
			report.OutputBytes += info.Size()
			report.Files++
			return nil
		})
		if e != nil {
			runErr = e
		}
	}
	if runErr == nil {
		report.Status = "PASS"
	}
	b, e := json.MarshalIndent(report, "", "  ")
	if e == nil {
		if writeErr := os.WriteFile(filepath.Join(profDir, "metrics.json"), append(b, '\n'), 0600); writeErr != nil && runErr == nil {
			runErr = writeErr
		}
	}
	return report, runErr
}

func CLI(args []string) error {
	flags := flag.NewFlagSet("profile", flag.ContinueOnError)
	workers := flags.Int("workers", 1, "parallel pools")
	disk := flags.Int("disk-partitions", 0, "disk partition count (2..64); default memory executor")
	gz := flags.Bool("evidence-gzip", false, "compress audit evidence")
	if len(args) < 2 {
		return errors.New("usage: AllocationCore profile RULE.json OUT_DIR [--workers 4] [--disk-partitions 8] [--evidence-gzip]")
	}
	if e := flags.Parse(args[2:]); e != nil {
		return e
	}
	if *workers < 1 || *workers > 128 || (*disk != 0 && (*disk < 2 || *disk > 64)) {
		return fmt.Errorf("invalid workers/disk-partitions")
	}
	r, e := Measure(args[0], args[1], engine.RunOptions{Workers: *workers, DiskPartitions: *disk, EvidenceGzip: *gz})
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	return e
}
