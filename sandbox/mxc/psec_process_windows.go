//go:build windows

package mxc

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// `ProcThreadAttributeSecurityEnvironment` (enum value 35) encoded with
// `PROC_THREAD_ATTRIBUTE_INPUT`, matching mxc-sdk
// (`backends/process_container/common/secenv.rs`).
const procThreadAttributeSecurityEnvironment = 0x0002_0000 | 35

// nativeSecurityEnvironment owns a `HPROCESS_SECURITY_ENVIRONMENT` created from
// a "PSEC" FlatBuffer.
type nativeSecurityEnvironment struct {
	handle windows.Handle
}

// createSecurityEnvironment wraps `CreateProcessSecurityEnvironment(spec,
// specSize, flags, &handle)`. The buffer must be a "PSEC" FlatBuffer produced by
// BuildSecurityEnvironmentSpec.
func createSecurityEnvironment(spec []byte) (*nativeSecurityEnvironment, error) {
	if len(spec) == 0 {
		return nil, fmt.Errorf("MXC requires a non-empty process security environment specification")
	}
	if err := psecCreateEnvironment.Find(); err != nil {
		return nil, fmt.Errorf("process security environment unavailable: %w", err)
	}
	var handle windows.Handle
	hr, _, _ := psecCreateEnvironment.Call(
		uintptr(unsafePointer(&spec[0])),
		uintptr(uint32(len(spec))),
		0, // PROCESS_SECURITY_ENVIRONMENT_FLAG_NONE
		uintptr(unsafePointer(&handle)),
	)
	if err := hresultError(hr); err != nil {
		return nil, fmt.Errorf("CreateProcessSecurityEnvironment: %w", err)
	}
	if handle == 0 {
		return nil, fmt.Errorf("CreateProcessSecurityEnvironment returned no environment handle")
	}
	return &nativeSecurityEnvironment{handle: handle}, nil
}

// Close releases the native environment; safe to call more than once.
func (e *nativeSecurityEnvironment) Close() {
	if e == nil || e.handle == 0 {
		return
	}
	if err := psecCloseEnvironment.Find(); err == nil {
		_, _, _ = psecCloseEnvironment.Call(uintptr(e.handle))
	}
	e.handle = 0
}

// securityEnvironmentStartupInfo owns the STARTUPINFOEX attribute list that
// binds the security environment to a CreateProcess call, mirroring mxc-sdk's
// `SecurityEnvironmentStartupInfo`.
type securityEnvironmentStartupInfo struct {
	attributes *windows.ProcThreadAttributeListContainer
	startup    *windows.StartupInfoEx
	// environment must keep its address stable until Delete runs.
	environment windows.Handle
}

// newSecurityEnvironmentStartupInfo builds the attribute list with the security
// environment handle attached through
// PROC_THREAD_ATTRIBUTE_SECURITY_ENVIRONMENT.
func newSecurityEnvironmentStartupInfo(environment windows.Handle, inheritStdio bool) (*securityEnvironmentStartupInfo, error) {
	if environment == 0 {
		return nil, fmt.Errorf("MXC requires a process security environment handle")
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, fmt.Errorf("InitializeProcThreadAttributeList: %w", err)
	}
	info := &securityEnvironmentStartupInfo{attributes: attributes, environment: environment}
	if err := attributes.Update(
		procThreadAttributeSecurityEnvironment,
		unsafe.Pointer(&info.environment),
		unsafe.Sizeof(info.environment),
	); err != nil {
		attributes.Delete()
		return nil, fmt.Errorf("UpdateProcThreadAttribute(SecurityEnvironment): %w", err)
	}
	startup := &windows.StartupInfoEx{
		StartupInfo:             windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))},
		ProcThreadAttributeList: attributes.List(),
	}
	if inheritStdio {
		// The helper runs with inherited stdio (Rust `StdioMode::Inherit`).
		startup.Flags |= windows.STARTF_USESTDHANDLES
		startup.StdInput = windows.Handle(os.Stdin.Fd())
		startup.StdOutput = windows.Handle(os.Stdout.Fd())
		startup.StdErr = windows.Handle(os.Stderr.Fd())
	}
	info.startup = startup
	return info, nil
}

// Close releases the attribute list storage.
func (s *securityEnvironmentStartupInfo) Close() {
	if s == nil || s.attributes == nil {
		return
	}
	s.attributes.Delete()
	s.attributes = nil
	s.startup = nil
}

// launchNative starts the prepared command inside a PSEC sandbox and returns
// its exit code. It fails closed: any step that cannot be reproduced faithfully
// (unsupported policy feature, missing capability bit, native error) returns an
// error instead of launching a weaker sandbox.
func launchNative(request *NativeRequest) (int, error) {
	if request == nil {
		return 0, fmt.Errorf("invalid MXC launcher request")
	}
	support := nativeLaunchSupport()
	if !support.Available {
		return 0, fmt.Errorf("%s: %s", NativeUnavailableMessage, support.Reason)
	}
	if err := validateLaunchablePolicy(request.Policy, support); err != nil {
		return 0, err
	}
	spec, err := BuildSecurityEnvironmentSpec(request.Policy.securityEnvironmentSpec())
	if err != nil {
		return 0, fmt.Errorf("build process security environment: %w", err)
	}
	environment, err := createSecurityEnvironment(spec)
	if err != nil {
		return 0, err
	}
	defer environment.Close()
	startup, err := newSecurityEnvironmentStartupInfo(environment.handle, true)
	if err != nil {
		return 0, err
	}
	defer startup.Close()

	commandLine, err := windows.UTF16PtrFromString(request.ScriptCode)
	if err != nil {
		return 0, fmt.Errorf("MXC command line: %w", err)
	}
	var workingDirectory *uint16
	if request.WorkingDirectory != "" {
		if workingDirectory, err = windows.UTF16PtrFromString(request.WorkingDirectory); err != nil {
			return 0, fmt.Errorf("MXC working directory: %w", err)
		}
	}
	environmentBlock, err := buildEnvironmentBlock(request.Env)
	if err != nil {
		return 0, err
	}
	var environmentPointer *uint16
	if len(environmentBlock) > 0 {
		environmentPointer = &environmentBlock[0]
	}

	var processInfo windows.ProcessInformation
	creationFlags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	if err := windows.CreateProcess(nil, commandLine, nil, nil, true, creationFlags, environmentPointer, workingDirectory, &startup.startup.StartupInfo, &processInfo); err != nil {
		// Error 87 means the PSEC handle was rejected: report it as a launch
		// failure, never retry outside the sandbox.
		return 0, fmt.Errorf("CreateProcessW(SecurityEnvironment): %w", err)
	}
	defer windows.CloseHandle(processInfo.Process)
	defer windows.CloseHandle(processInfo.Thread)

	job, err := newSandboxJob(request.Policy)
	if err != nil {
		_ = windows.TerminateProcess(processInfo.Process, 1)
		return 0, err
	}
	defer windows.CloseHandle(job)
	if err := windows.AssignProcessToJobObject(job, processInfo.Process); err != nil {
		_ = windows.TerminateProcess(processInfo.Process, 1)
		return 0, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	if _, err := windows.ResumeThread(processInfo.Thread); err != nil {
		_ = windows.TerminateProcess(processInfo.Process, 1)
		return 0, fmt.Errorf("ResumeThread: %w", err)
	}
	if _, err := windows.WaitForSingleObject(processInfo.Process, windows.INFINITE); err != nil {
		return 0, fmt.Errorf("waiting for the MXC command: %w", err)
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(processInfo.Process, &exitCode); err != nil {
		return 0, fmt.Errorf("GetExitCodeProcess: %w", err)
	}
	return int(exitCode), nil
}

// validateLaunchablePolicy fails closed for policies this launcher cannot
// reproduce faithfully in the PSEC spec.
func validateLaunchablePolicy(policy NativeContainerPolicy, support NativeSupport) error {
	for _, capability := range policy.Capabilities {
		if strings.EqualFold(strings.TrimSpace(capability), "permissiveLearningMode") {
			return fmt.Errorf("MXC native launch does not support permissiveLearningMode")
		}
	}
	if len(policy.DeniedPaths) > 0 && !support.FileSystemDeny {
		return fmt.Errorf("this Windows build cannot enforce native MXC deny paths")
	}
	if policy.ProxyURL != "" {
		if policy.NetworkEgress.Default == NetworkActionAllow || len(policy.NetworkEgress.Allow) > 0 {
			return fmt.Errorf("MXC proxy mode requires deny-by-default egress with no direct allow rules")
		}
		return nil
	}
	if policy.NetworkSpecified {
		for _, rule := range policy.NetworkEgress.Allow {
			if err := ValidateNetworkCIDR(rule.To); err != nil {
				return err
			}
		}
	}
	return nil
}

// buildEnvironmentBlock renders KEY=VALUE entries as a double-NUL-terminated
// UTF-16 block. An empty request inherits the parent environment, matching the
// helper's `(!env.is_empty()).then_some(env)`. The returned slice must stay
// alive until CreateProcess returns; callers pass &block[0].
func buildEnvironmentBlock(env []string) ([]uint16, error) {
	if len(env) == 0 {
		return nil, nil
	}
	resolved, err := withHostReservedEnvironment(env)
	if err != nil {
		return nil, err
	}
	entries := resolved
	sort.Strings(entries)
	var block []uint16
	for _, entry := range entries {
		units, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, fmt.Errorf("MXC environment block entry %q: %w", entry, err)
		}
		block = append(block, units...)
	}
	// UTF16FromString already terminates each entry; add the block terminator.
	block = append(block, 0)
	return block, nil
}

// reservedProcessEnvironmentKeys are owned by the host: MXC replaces caller
// values with the validated host values (Rust windows-mxc README) and fails when
// the host value is missing, because PSEC rejects an explicit environment that
// omits them.
var reservedProcessEnvironmentKeys = []string{"SystemDrive", "LOCALAPPDATA"}

func withHostReservedEnvironment(env []string) ([]string, error) {
	merged := make([]string, 0, len(env)+len(reservedProcessEnvironmentKeys))
	for _, entry := range env {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			return nil, fmt.Errorf("MXC environment entry %q must be KEY=VALUE", entry)
		}
		if environmentKeyIn(name, reservedProcessEnvironmentKeys) {
			continue
		}
		merged = append(merged, name+"="+value)
	}
	for _, key := range reservedProcessEnvironmentKeys {
		value := os.Getenv(key)
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("MXC requires the host %s environment variable", key)
		}
		merged = append(merged, key+"="+value)
	}
	return merged, nil
}

func environmentKeyIn(name string, keys []string) bool {
	for _, key := range keys {
		if strings.EqualFold(name, key) {
			return true
		}
	}
	return false
}

// newKillOnCloseJob creates a job object that terminates the sandboxed process
// when the handle closes, so a crashed helper cannot leak the workload.
func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObject: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("SetInformationJobObject(kill on close): %w", err)
	}
	return job, nil
}

// nativeLaunchSupport is a test seam: the launcher asks this for availability so
// the fail-closed path can be exercised on a host where PSEC is present.
var nativeLaunchSupport = ProbeNativeSupport
