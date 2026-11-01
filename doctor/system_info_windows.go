//go:build windows

package doctor

import (
	"fmt"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Rust doctor/system.rs reports os_info::get(), whose Windows backend reads
// RtlGetVersion for the numeric version and the CurrentVersion registry key for
// the marketing edition.
var (
	doctorNtdllProcRtlGetVersion             = windows.NewLazySystemDLL("ntdll.dll").NewProc("RtlGetVersion")
	doctorKernel32ProcUserPreferredLanguages = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserPreferredUILanguages")
)

const doctorMUILanguageName = 0x00000008

type doctorOSVersionInfoEx struct {
	OSVersionInfoSize uint32
	MajorVersion      uint32
	MinorVersion      uint32
	BuildNumber       uint32
	PlatformID        uint32
	CSDVersion        [128]uint16
	ServicePackMajor  uint16
	ServicePackMinor  uint16
	SuiteMask         uint16
	ProductType       byte
	Reserved          byte
}

func doctorOSIdentity() (string, string, string) {
	major, minor, build, ok := doctorWindowsVersion()
	display := "Windows"
	version := "unknown"
	edition := ""
	if ok {
		version = fmt.Sprintf("%d.%d.%d", major, minor, build)
		edition = doctorWindowsEdition(major, build)
		display += " " + version
	}
	if edition != "" {
		display += " (" + edition + ")"
	}
	display += " [" + doctorBitnessLabel() + "]"
	return display, "windows", version
}

func doctorWindowsVersion() (uint32, uint32, uint32, bool) {
	info := doctorOSVersionInfoEx{OSVersionInfoSize: uint32(unsafe.Sizeof(doctorOSVersionInfoEx{}))}
	status, _, _ := doctorNtdllProcRtlGetVersion.Call(uintptr(unsafe.Pointer(&info)))
	if status != 0 {
		return 0, 0, 0, false
	}
	return info.MajorVersion, info.MinorVersion, info.BuildNumber, true
}

func doctorWindowsEdition(major uint32, build uint32) string {
	isWindows11 := major == 10 && build >= 22000
	if key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE); err == nil {
		defer key.Close()
		name := "ProductName"
		if isWindows11 {
			name = "EditionID"
		}
		if value, _, valueErr := key.GetStringValue(name); valueErr == nil && value != "" {
			if isWindows11 {
				return "Windows 11 " + value
			}
			return value
		}
	}
	if major == 10 {
		if isWindows11 {
			return "Windows 11"
		}
		return "Windows 10"
	}
	return ""
}

func doctorBitnessLabel() string {
	if strconv.IntSize == 64 {
		return "64-bit"
	}
	return "32-bit"
}

func doctorPreferredLocale() string {
	var languages uint32
	var bufferLength uint32
	if ok, _, _ := doctorKernel32ProcUserPreferredLanguages.Call(
		doctorMUILanguageName,
		uintptr(unsafe.Pointer(&languages)),
		0,
		uintptr(unsafe.Pointer(&bufferLength)),
	); ok == 0 || bufferLength == 0 {
		return ""
	}
	buffer := make([]uint16, bufferLength)
	if ok, _, _ := doctorKernel32ProcUserPreferredLanguages.Call(
		doctorMUILanguageName,
		uintptr(unsafe.Pointer(&languages)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&bufferLength)),
	); ok == 0 {
		return ""
	}
	return windows.UTF16ToString(buffer)
}
