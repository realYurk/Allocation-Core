//go:build linux

package profiling

import (
	"os"
	"strconv"
	"strings"
)

type ProcessIO struct {
	ReadBytes  uint64 `json:"read_bytes"`
	WriteBytes uint64 `json:"write_bytes"`
	ReadChars  uint64 `json:"read_chars"`
	WriteChars uint64 `json:"write_chars"`
}

func readProcessIO() *ProcessIO {
	raw, e := os.ReadFile("/proc/self/io")
	if e != nil {
		return nil
	}
	var io ProcessIO
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		n, e := strconv.ParseUint(f[1], 10, 64)
		if e != nil {
			continue
		}
		switch f[0] {
		case "read_bytes:":
			io.ReadBytes = n
		case "write_bytes:":
			io.WriteBytes = n
		case "rchar:":
			io.ReadChars = n
		case "wchar:":
			io.WriteChars = n
		}
	}
	return &io
}
