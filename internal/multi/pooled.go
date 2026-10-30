package multi

// Optional long-lived Core workers. Each worker remains a cancellable
// subprocess of the same AllocationCore executable, processing one rule group
// at a time. This preserves process cancellation without N process startups.
// The public financial engine, result manifest and evidence paths are unchanged.
import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"allocationcore/internal/engine"
)

type workerTask struct {
	Rule            string `json:"rule"`
	Out             string `json:"out"`
	Workers         int    `json:"workers"`
	DiskPartitions  int    `json:"disk_partitions"`
	DriverDiskIndex bool   `json:"driver_disk_index"`
	DriverCacheDir  string `json:"driver_cache_dir"`
	EvidenceGzip    bool   `json:"evidence_gzip"`
}
type workerReply struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// WorkerCLI is an internal line-oriented protocol, not a user-facing API.
// The worker prints JSON replies only, keeping stdout machine-readable.
func WorkerCLI() {
	dec := json.NewDecoder(bufio.NewReaderSize(os.Stdin, 128*1024))
	enc := json.NewEncoder(os.Stdout)
	for {
		var req workerTask
		err := dec.Decode(&req)
		if err == io.EOF {
			return
		}
		if err != nil {
			_ = enc.Encode(workerReply{Status: "FAILED", Error: "invalid worker request: " + err.Error()})
			return
		}
		if req.Rule == "" || req.Out == "" || req.Workers < 1 || req.Workers > 128 || req.DiskPartitions < 0 || req.DiskPartitions == 1 || req.DiskPartitions > 64 {
			_ = enc.Encode(workerReply{Status: "FAILED", Error: "invalid worker execution parameters"})
			return
		}
		opts := engine.RunOptions{Workers: req.Workers, DiskPartitions: req.DiskPartitions, DriverDiskIndex: req.DriverDiskIndex, DriverCacheDir: req.DriverCacheDir, EvidenceGzip: req.EvidenceGzip}
		var summary engine.Summary
		if req.DiskPartitions > 1 {
			summary, err = engine.RunDiskWithOptions(req.Rule, req.Out, opts)
		} else {
			summary, err = engine.RunWithOptions(req.Rule, req.Out, opts)
		}
		resp := workerReply{Status: "PASS"}
		if err != nil {
			resp = workerReply{Status: "FAILED", Error: err.Error()}
		} else if summary.Status != "PASS" {
			resp = workerReply{Status: "FAILED", Error: "missing PASS status from core"}
		}
		if err = enc.Encode(resp); err != nil {
			return
		}
		if resp.Status != "PASS" {
			return
		} // fail-closed: restart needed after a failed task
	}
}

// Bounded and synchronized, including when a canceled child is still draining stderr.
type workerLog struct {
	mu   sync.Mutex
	data []byte
}

func (l *workerLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.data) < 8192 {
		n := 8192 - len(l.data)
		if n > len(b) {
			n = len(b)
		}
		l.data = append(l.data, b[:n]...)
	}
	return len(b), nil
}
func (l *workerLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return string(l.data) }

type workerClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	dec    *json.Decoder
	enc    *json.Encoder
	stderr *workerLog
}

func startWorker(ctx context.Context, engineExe string, dir string) (*workerClient, error) {
	cmd := exec.CommandContext(ctx, engineExe, "multi-worker")
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	stderr := &workerLog{}
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		stdin.Close()
		return nil, err
	}
	return &workerClient{cmd: cmd, stdin: stdin, dec: json.NewDecoder(bufio.NewReaderSize(stdout, 128*1024)), enc: json.NewEncoder(stdin), stderr: stderr}, nil
}
func (w *workerClient) process(t workerTask) error {
	if err := w.enc.Encode(t); err != nil {
		return fmt.Errorf("worker request: %w", err)
	}
	var reply workerReply
	if err := w.dec.Decode(&reply); err != nil {
		return fmt.Errorf("worker response: %w (stderr: %.200s)", err, w.stderr.String())
	}
	if reply.Status != "PASS" {
		return fmt.Errorf("worker rejected rule: %s", reply.Error)
	}
	return nil
}
func (w *workerClient) close() error {
	if w == nil {
		return nil
	}
	_ = w.stdin.Close()
	return w.cmd.Wait()
}

func (p *project) executePooled(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	jobs := make(chan *Group)
	var wg sync.WaitGroup
	var first error
	var once sync.Once
	fail := func(g *Group, e error) {
		g.Status = "FAILED"
		g.Error = e.Error()
		once.Do(func() { first = fmt.Errorf("rule group %s: %w", g.ID, e); cancel() })
	}
	// We only start as many process workers as configured, and each process
	// loads multiple rule groups over the same checked protocol.
	for i := 0; i < p.cfg.Jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var worker *workerClient
			defer func() {
				if worker != nil {
					_ = worker.close()
				}
			}()
			for g := range jobs {
				if ctx.Err() != nil {
					return
				}
				start := time.Now()
				if worker == nil {
					w, e := startWorker(ctx, p.engine, p.dir)
					if e != nil {
						fail(g, e)
						return
					}
					worker = w
				}
				req := workerTask{Rule: g.Rule, Out: g.Output, Workers: p.cfg.Workers, DiskPartitions: p.cfg.DiskPartitions, DriverDiskIndex: p.cfg.DriverDiskIndex, EvidenceGzip: p.cfg.EvidenceGzip}
				if p.cfg.DriverDiskIndex {
					path, e := p.driverCacheDirectory()
					if e != nil {
						fail(g, e)
						return
					}
					req.DriverCacheDir = path
				}
				if e := worker.process(req); e != nil {
					fail(g, e)
					return
				}
				g.DurationMS = time.Since(start).Milliseconds()
				var summary map[string]any
				if e := readJSON(filepath.Join(g.Output, "run_summary.json"), &summary); e != nil || summary["status"] != "PASS" {
					fail(g, fmt.Errorf("missing/invalid run summary: %v", e))
					return
				}
				g.Summary = summary
				g.Status = "PASS"
			}
		}()
	}
loop:
	for _, g := range p.groups {
		select {
		case <-ctx.Done():
			break loop
		case jobs <- g:
		}
	}
	close(jobs)
	wg.Wait()
	if parent.Err() != nil {
		return parent.Err()
	}
	if first != nil {
		return first
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
