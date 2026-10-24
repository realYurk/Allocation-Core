//go:build windows

package capacity

import (
	"fmt"
	"syscall"
	"unsafe"
)

func diskFree(path string) (uint64, error) {
	var free uint64
	ptr, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return 0, e
	}
	dll := syscall.NewLazyDLL("kernel32.dll")
	p := dll.NewProc("GetDiskFreeSpaceExW")
	ok, _, err := p.Call(uintptr(unsafe.Pointer(ptr)), uintptr(unsafe.Pointer(&free)), 0, 0)
	if ok == 0 {
		return 0, fmt.Errorf("GetDiskFreeSpaceExW: %v", err)
	}
	return free, nil
}
