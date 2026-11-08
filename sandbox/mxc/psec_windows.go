//go:build windows

package mxc

import (
	"fmt"
	"sync"

	coresandbox "codex_go/sandbox"
	"golang.org/x/sys/windows"
)

// PSEC is the Windows "process security environment" API that Microsoft's MXC
// BaseContainerRunner launches through. It is exported by
// `%SystemRoot%\System32\processmodel.dll` and gated behind the API set
// `api-win-appmodel-processmodel~securityenvironment`, so it must be resolved
// at runtime rather than linked.
//
// The declarations mirror the MXC SDK's own bindings
// (mxc/src/mxc-sdk/src/backends/process_container/common/secenv.rs):
//
//	HRESULT CreateProcessSecurityEnvironment(LPCVOID spec, DWORD specSize,
//	    PROCESS_SECURITY_ENVIRONMENT_FLAGS flags, HPROCESS_SECURITY_ENVIRONMENT* out);
//	HRESULT QueryProcessSecurityEnvironmentSupport(UINT64* supportFlags);
//	HRESULT IsProcessSecurityEnvironmentVersionSupported(DWORD major, BOOLEAN* available,
//	    DWORD* minor);
//	void CloseProcessSecurityEnvironment(HPROCESS_SECURITY_ENVIRONMENT environment);
//
// `spec`/`specSize` carry a "PSEC" FlatBuffer, and a launch attaches the
// returned handle through the `PROC_THREAD_ATTRIBUTE_SECURITY_ENVIRONMENT`
// process attribute of a normal `CreateProcessW`.
const psecMajorVersion = 1

var (
	psecDLL = windows.NewLazySystemDLL("processmodel.dll")

	psecIsVersionSupported = psecDLL.NewProc("IsProcessSecurityEnvironmentVersionSupported")
	psecQuerySupport       = psecDLL.NewProc("QueryProcessSecurityEnvironmentSupport")
	psecCreateEnvironment  = psecDLL.NewProc("CreateProcessSecurityEnvironment")
	psecCloseEnvironment   = psecDLL.NewProc("CloseProcessSecurityEnvironment")
)

// NativeSupport reports what this host's process security environment can do.
// It answers the question Rust's `mxc_sandbox::is_available()` and
// `BaseContainerRunner::supports_native_denied_paths()` answer, without building
// a policy or launching a process.
type NativeSupport struct {
	// Available is true when the PSEC exports resolve and the version MXC
	// requires is supported.
	Available bool
	// Major and Minor are the highest PSEC version the host reports.
	Major uint32
	Minor uint32
	// SupportFlags is the raw QueryProcessSecurityEnvironmentSupport bit set.
	SupportFlags uint64
	// FileSystemDeny gates native deny paths, i.e. MXC's
	// PSE_SUPPORT_FS_DENY capability.
	FileSystemDeny bool
	// FileSystemEnumerate gates enumeration-only filesystem grants.
	FileSystemEnumerate bool
	// NetworkIngress gates the ingress policy table.
	NetworkIngress bool
	// Reason explains why the host cannot run native MXC, empty when available.
	Reason string
}

var (
	nativeSupportOnce sync.Once
	nativeSupport     NativeSupport
)

// ProbeNativeSupport resolves the PSEC entry points once per process. Go has no
// native MXC launcher yet, so this probe is the honest availability signal:
// WindowsMXCAvailable stays false (selection must not pick a backend that
// cannot launch), but the capability bits are recorded for the executor's
// `codex.windows_mxc.available` diagnostics.
func ProbeNativeSupport() NativeSupport {
	nativeSupportOnce.Do(func() {
		nativeSupport = probeNativeSupportUncached()
	})
	return nativeSupport
}

func probeNativeSupportUncached() NativeSupport {
	report := NativeSupport{}
	if err := psecDLL.Load(); err != nil {
		report.Reason = fmt.Sprintf("process security environment DLL unavailable: %v", err)
		return report
	}
	for _, proc := range []*windows.LazyProc{psecIsVersionSupported, psecQuerySupport, psecCreateEnvironment, psecCloseEnvironment} {
		if err := proc.Find(); err != nil {
			report.Reason = fmt.Sprintf("process security environment export %s unavailable: %v", proc.Name, err)
			return report
		}
	}
	var available uint8
	var minor uint32
	hr, _, _ := psecIsVersionSupported.Call(uintptr(psecMajorVersion), uintptr(unsafePointer(&available)), uintptr(unsafePointer(&minor)))
	if err := hresultError(hr); err != nil {
		report.Reason = fmt.Sprintf("process security environment version probe failed: %v", err)
		return report
	}
	if available == 0 {
		report.Reason = fmt.Sprintf("process security environment v%d is not supported on this host", psecMajorVersion)
		return report
	}
	var flags uint64
	if hr, _, _ := psecQuerySupport.Call(uintptr(unsafePointer(&flags))); hresultError(hr) == nil {
		report.SupportFlags = flags
	}
	report.Available = true
	report.Major = psecMajorVersion
	report.Minor = minor
	report.FileSystemDeny = flags&psecSupportFileSystemDeny != 0
	report.FileSystemEnumerate = flags&psecSupportFileSystemEnumerate != 0
	report.NetworkIngress = flags&psecSupportNetworkIngress != 0
	return report
}

// hresultError renders a failed HRESULT the way the native layer reports it;
// success returns nil.
func hresultError(hr uintptr) error {
	signed := int32(uint32(hr))
	if signed >= 0 {
		return nil
	}
	return fmt.Errorf("HRESULT 0x%08X", uint32(signed))
}

// init publishes the launcher's availability so sandbox.WindowsMXCAvailable()
// and launch() cannot disagree.
func init() {
	coresandbox.RegisterWindowsMXCAvailabilityProbe(NativeLaunchSupported)
}
