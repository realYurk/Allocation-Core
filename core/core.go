// Package core exposes the existing allocation engine to external Go programs.
// All amounts, validation, conservation and publication semantics belong to
// the same engine used by the CLI and Studio; this package adds no algorithm.
package core

import "allocationcore/internal/engine"

type Spec = engine.Spec
type Summary = engine.Summary
type StageSummary = engine.StageSummary
type RunOptions = engine.RunOptions

// ParseSpec parses the allocation-ir/2.0 rule without accepting unknown fields.
func ParseSpec(data []byte) (Spec, error) { return engine.ParseSpec(data) }

// Validate checks a rule using the same planner as the engine.
func Validate(spec Spec) error { _, err := engine.Compile(spec); return err }

// Run executes the existing engine. The rule and its CSV datasets must be
// available on the local filesystem as required by the Core contract.
func Run(rulePath, outputDir string, options RunOptions) (Summary, error) {
	return engine.RunWithOptions(rulePath, outputDir, options)
}

// RunDisk uses the existing disk-backed executor and its normal safeguards.
func RunDisk(rulePath, outputDir string, options RunOptions) (Summary, error) {
	return engine.RunDiskWithOptions(rulePath, outputDir, options)
}
