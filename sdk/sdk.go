// Package sdk provides a file-based Go SDK for the existing Allocation Core.
// It does not implement allocation mathematics or change the engine contract.
package sdk

import (
	"allocationcore/internal/acceptance"
	"allocationcore/internal/engine"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// Error identifies the operation that failed. Unwrap retains the original error.
// These codes describe the SDK boundary, not a claim about the business cause.
type Error struct {
	Code, Operation string
	Err             error
}

func (e *Error) Error() string                 { return fmt.Sprintf("%s (%s): %v", e.Operation, e.Code, e.Err) }
func (e *Error) Unwrap() error                 { return e.Err }
func problem(code, op string, err error) error { return &Error{Code: code, Operation: op, Err: err} }

type InputReport = engine.InputReport
type InputCheck = engine.InputCheck
type Summary = engine.Summary
type StageSummary = engine.StageSummary
type Reconciliation = acceptance.Report
type ReconciliationOptions = acceptance.Options

// Inspect validates the rule and streams its local input files. PASS covers
// input shape and numeric precision only, not Driver eligibility or accounting
// correctness. Check both err and report.Status. Input files must remain fixed.
func Inspect(rulePath string) (InputReport, error) {
	if rulePath == "" {
		return InputReport{}, problem("INVALID_REQUEST", "inspect", errors.New("rule path is required"))
	}
	data, err := os.ReadFile(rulePath)
	if err != nil {
		return InputReport{}, problem("READ_RULE", "inspect", err)
	}
	spec, err := engine.ParseSpec(data)
	if err != nil {
		return InputReport{}, problem("INVALID_RULE", "inspect", err)
	}
	if _, err = engine.Compile(spec); err != nil {
		return InputReport{}, problem("INVALID_RULE", "inspect", err)
	}
	r := engine.InspectInputs(rulePath, spec)
	hash := sha256.Sum256(data)
	if r.RuleSHA256 != "" && r.RuleSHA256 != hex.EncodeToString(hash[:]) {
		return InputReport{}, problem("INPUT_CHANGED", "inspect", errors.New("rule changed during inspection"))
	}
	return r, nil
}

type Mode string

const (
	Memory Mode = "memory"
	Disk   Mode = "disk"
)

// RunRequest requires a new output directory; existing output is never replaced.
// Zero mode uses memory, zero workers uses one; disk partitions default to 8.
// Disk-only fields are rejected in memory mode rather than silently ignored.
type RunRequest struct {
	RulePath        string
	OutputDir       string
	Mode            Mode
	Workers         int
	DiskPartitions  int
	DriverDiskIndex bool
	DriverCacheDir  string
	CheckpointDir   string
	Resume          bool
	EvidenceGzip    bool
}

// Run invokes the same engine as Studio. Once started, in-process execution
// cannot be canceled with a context; use a managed worker process when needed.
func Run(req RunRequest) (Summary, error) {
	const op = "run"
	if req.RulePath == "" || req.OutputDir == "" {
		return Summary{}, problem("INVALID_REQUEST", op, errors.New("rule path and new output directory are required"))
	}
	mode := req.Mode
	if mode == "" {
		mode = Memory
	}
	if mode != Memory && mode != Disk {
		return Summary{}, problem("INVALID_REQUEST", op, errors.New("mode must be memory or disk"))
	}
	if req.Workers < 0 || req.Workers > 128 {
		return Summary{}, problem("INVALID_REQUEST", op, errors.New("workers must be 0..128"))
	}
	if mode == Memory && (req.DiskPartitions != 0 || req.DriverDiskIndex || req.DriverCacheDir != "" || req.CheckpointDir != "" || req.Resume) {
		return Summary{}, problem("INVALID_REQUEST", op, errors.New("disk options require disk mode"))
	}
	if mode == Disk && req.DriverCacheDir != "" && !req.DriverDiskIndex {
		return Summary{}, problem("INVALID_REQUEST", op, errors.New("driver cache requires driver disk index"))
	}
	if req.Resume && req.CheckpointDir == "" {
		return Summary{}, problem("INVALID_REQUEST", op, errors.New("resume requires a checkpoint directory"))
	}
	options := engine.RunOptions{Workers: req.Workers, DiskPartitions: req.DiskPartitions, DriverDiskIndex: req.DriverDiskIndex, DriverCacheDir: req.DriverCacheDir, CheckpointDir: req.CheckpointDir, Resume: req.Resume, EvidenceGzip: req.EvidenceGzip}
	var result engine.Summary
	var err error
	if mode == Disk {
		if options.DiskPartitions == 0 {
			options.DiskPartitions = 8
		}
		result, err = engine.RunDiskWithOptions(req.RulePath, req.OutputDir, options)
	} else {
		result, err = engine.RunWithOptions(req.RulePath, req.OutputDir, options)
	}
	if err != nil {
		return result, problem("EXECUTION_FAILED", op, err)
	}
	return result, nil
}

// Compare uses the existing independent coordinate comparator. Reference must
// be independently supplied by the caller, not generated from actual output.
// The report may have DIFFERENCES with nil error; inspect report.Status.
func Compare(ctx context.Context, actual, reference, outputDir string, options ReconciliationOptions) (Reconciliation, error) {
	if ctx == nil || actual == "" || reference == "" || outputDir == "" {
		return Reconciliation{}, problem("INVALID_REQUEST", "compare", errors.New("context and all paths are required"))
	}
	r, err := acceptance.Compare(ctx, actual, reference, outputDir, options)
	if err != nil {
		return r, problem("COMPARISON_FAILED", "compare", err)
	}
	return r, nil
}
