//go:build !linux && !windows

package profiling

// Other operating systems do not expose /proc/self/io. Omit unavailable
// metrics rather than making up disk read/write totals.
type ProcessIO struct {
	ReadBytes  uint64 `json:"read_bytes"`
	WriteBytes uint64 `json:"write_bytes"`
	ReadChars  uint64 `json:"read_chars"`
	WriteChars uint64 `json:"write_chars"`
}

func readProcessIO() *ProcessIO { return nil }
