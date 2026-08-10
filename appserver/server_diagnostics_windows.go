//go:build windows

package appserver

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type hookProcessMemoryCounters struct {
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

var procGetProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

func processWorkingSetBytes() (uint64, bool) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(os.Getpid()))
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(handle)
	var counts hookProcessMemoryCounters
	counts.CB = uint32(unsafe.Sizeof(counts))
	status, _, callErr := procGetProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counts)), uintptr(counts.CB))
	if status == 0 {
		_ = callErr
		return 0, false
	}
	return uint64(counts.WorkingSetSize), true
}
