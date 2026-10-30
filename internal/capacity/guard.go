// Package capacity supplies a conservative production admission budget.
// It never edits financial calculations and is not an OS disk quota.
package capacity

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
)

type Policy struct {
	Pools               int64 `json:"pools"`
	TargetsPerPool      int64 `json:"targets_per_pool"`
	FactorPaths         int64 `json:"factor_paths"`
	ResultBytesPerRow   int64 `json:"result_bytes_per_row"`
	EvidenceBytesPerRow int64 `json:"evidence_bytes_per_row"`
	OverheadPercent     int64 `json:"overhead_percent"`
	MaxRows             int64 `json:"max_rows"`
	MaxPeakBytes        int64 `json:"max_peak_bytes"`
	ReserveFreeBytes    int64 `json:"reserve_free_bytes"`
}
type Report struct {
	Status             string   `json:"status"`
	EstimatedRows      string   `json:"estimated_rows"`
	EstimatedPeakBytes string   `json:"estimated_peak_bytes"`
	AvailableDiskBytes uint64   `json:"available_disk_bytes"`
	Reasons            []string `json:"reasons"`
	Limitations        []string `json:"limitations"`
}

func Load(path string) (Policy, error) {
	var p Policy
	f, e := os.Open(path)
	if e != nil {
		return p, e
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if e = d.Decode(&p); e != nil {
		return p, e
	}
	var other any
	if e = d.Decode(&other); e == nil {
		return p, fmt.Errorf("policy must contain exactly one JSON object")
	} else if e.Error() != "EOF" {
		return p, e
	}
	if p.Pools < 1 || p.TargetsPerPool < 1 || p.FactorPaths < 1 || p.ResultBytesPerRow < 1 || p.EvidenceBytesPerRow < 1 || p.OverheadPercent < 100 || p.MaxRows < 1 || p.MaxPeakBytes < 1 || p.ReserveFreeBytes < 0 {
		return p, fmt.Errorf("invalid capacity budget; all counts/limits must be positive and overhead_percent >= 100")
	}
	return p, nil
}
func Check(policyPath, outputDir string) (Report, error) {
	p, e := Load(policyPath)
	if e != nil {
		return Report{}, fmt.Errorf("capacity policy: %w", e)
	}
	where := outputDir
	if where == "" {
		where = "."
	}
	abs, e := filepath.Abs(where)
	if e != nil {
		return Report{}, e
	}
	for {
		info, statErr := os.Stat(abs)
		if statErr == nil {
			if !info.IsDir() {
				abs = filepath.Dir(abs)
			}
			break
		}
		if !os.IsNotExist(statErr) {
			return Report{}, statErr
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return Report{}, fmt.Errorf("no existing storage parent")
		}
		abs = parent
	}
	free, e := diskFree(abs)
	if e != nil {
		return Report{}, fmt.Errorf("disk free measurement failed: %w", e)
	}
	rows := new(big.Int).Mul(big.NewInt(p.Pools), big.NewInt(p.TargetsPerPool))
	rows.Mul(rows, big.NewInt(p.FactorPaths))
	perRow := new(big.Int).Add(big.NewInt(p.ResultBytesPerRow), big.NewInt(p.EvidenceBytesPerRow))
	peak := new(big.Int).Mul(rows, perRow)
	peak.Mul(peak, big.NewInt(p.OverheadPercent))
	peak.Add(peak, big.NewInt(99))
	peak.Div(peak, big.NewInt(100))
	r := Report{Status: "PASS", EstimatedRows: rows.String(), EstimatedPeakBytes: peak.String(), AvailableDiskBytes: free, Reasons: []string{}, Limitations: []string{"Inputs are trusted operator estimates, not an exact engine row count.", "No OS-level disk quota or runtime cancellation; use isolated volumes and external supervision.", "A policy configured via ALLOC_CAPACITY_POLICY is enforced on all engine execution paths."}}
	if rows.Cmp(big.NewInt(p.MaxRows)) > 0 {
		r.Reasons = append(r.Reasons, "estimated rows exceed max_rows")
	}
	if peak.Cmp(big.NewInt(p.MaxPeakBytes)) > 0 {
		r.Reasons = append(r.Reasons, "estimated output exceeds max_peak_bytes")
	}
	usable := new(big.Int).SetUint64(free)
	usable.Sub(usable, big.NewInt(p.ReserveFreeBytes))
	if usable.Sign() < 0 || peak.Cmp(usable) > 0 {
		r.Reasons = append(r.Reasons, "insufficient free disk after reserve")
	}
	if len(r.Reasons) > 0 {
		r.Status = "BLOCK"
	}
	return r, nil
}
func CheckEnv(outputDir string) error {
	policy := strings.TrimSpace(os.Getenv("ALLOC_CAPACITY_POLICY"))
	if policy == "" {
		return nil
	}
	report, e := Check(policy, outputDir)
	if e != nil {
		return e
	}
	if report.Status != "PASS" {
		return fmt.Errorf("capacity preflight BLOCK: %s (estimated rows=%s, estimated bytes=%s)", strings.Join(report.Reasons, "; "), report.EstimatedRows, report.EstimatedPeakBytes)
	}
	return nil
}
