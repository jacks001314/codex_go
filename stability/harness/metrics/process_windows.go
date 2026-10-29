//go:build windows

package metrics

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

var (
	procGetProcessMemoryInfo  = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
	procGetProcessHandleCount = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
)

func readRSSBytes() (uint64, bool) {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return 0, false
	}
	var counters processMemoryCounters
	counters.CB = uint32(unsafe.Sizeof(counters))
	status, _, _ := procGetProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.CB))
	if status == 0 {
		return 0, false
	}
	return uint64(counters.WorkingSetSize), true
}

func readOpenFDs() (int, bool) {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return 0, false
	}
	var count uint32
	status, _, _ := procGetProcessHandleCount.Call(uintptr(handle), uintptr(unsafe.Pointer(&count)))
	if status == 0 {
		return 0, false
	}
	return int(count), true
}
