//go:build windows

package profiling

import (
	"syscall"
	"unsafe"
)

// Values are Windows IO_COUNTERS transfer counts, not physical disk-write
// counts. They include filesystem/cache transfers, temp files and profiles.
type ProcessIO struct {
	ReadBytes  uint64 `json:"read_transfer_bytes"`
	WriteBytes uint64 `json:"write_transfer_bytes"`
	ReadChars  uint64 `json:"read_operations"`
	WriteChars uint64 `json:"write_operations"`
}
type nativeIOCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var currentProcess = kernel32.NewProc("GetCurrentProcess")
var processCounters = kernel32.NewProc("GetProcessIoCounters")

func readProcessIO() *ProcessIO {
	h, _, _ := currentProcess.Call()
	if h == 0 {
		return nil
	}
	var v nativeIOCounters
	ok, _, _ := processCounters.Call(h, uintptr(unsafe.Pointer(&v)))
	if ok == 0 {
		return nil
	}
	return &ProcessIO{ReadBytes: v.ReadTransferCount, WriteBytes: v.WriteTransferCount, ReadChars: v.ReadOperationCount, WriteChars: v.WriteOperationCount}
}
