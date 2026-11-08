//go:build windows

package mxc

import (
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestSecurityEnvironmentSpecAcceptedByNativeApi is the authoritative layout
// check: processmodel.dll parses the buffer, validates the "PSEC" identifier and
// the required version struct, and only then returns an environment handle. A
// wrong slot order or scalar type fails here even though the Go decoder test
// cannot see it.
func TestSecurityEnvironmentSpecAcceptedByNativeApi(t *testing.T) {
	support := ProbeNativeSupport()
	if !support.Available {
		t.Skipf("process security environment unavailable: %s", support.Reason)
	}
	policy := NativeContainerPolicy{
		Capabilities:     []string{"registryRead"},
		ReadWritePaths:   []string{`C:\Windows\Temp`},
		DeniedPaths:      []string{`C:\Windows\Temp\mxc-denied`},
		NetworkEgress:    NetworkEgressPolicy{Default: NetworkActionDeny},
		NetworkIngress:   NetworkIngressPolicy{Default: NetworkActionDeny, HostLoopback: NetworkActionAllow},
		NetworkSpecified: true,
		BaseProcessUI:    BaseProcessUI{Isolation: "desktop"},
	}
	spec, err := BuildSecurityEnvironmentSpec(policy.securityEnvironmentSpec())
	if err != nil {
		t.Fatalf("BuildSecurityEnvironmentSpec() error = %v", err)
	}
	environment, err := createSecurityEnvironment(spec)
	if err != nil {
		t.Fatalf("CreateProcessSecurityEnvironment rejected the spec: %v", err)
	}
	defer environment.Close()

	startup, err := newSecurityEnvironmentStartupInfo(environment.handle, false)
	if err != nil {
		t.Fatalf("newSecurityEnvironmentStartupInfo() error = %v", err)
	}
	defer startup.Close()
	if got, want := startup.startup.Cb, uint32(unsafe.Sizeof(windows.StartupInfoEx{})); got != want {
		t.Fatalf("StartupInfoEx.Cb = %d, want %d", got, want)
	}
	if startup.startup.ProcThreadAttributeList == nil {
		t.Fatal("StartupInfoEx.ProcThreadAttributeList = nil, want the attribute list")
	}
	if startup.startup.Flags&windows.STARTF_USESTDHANDLES != 0 {
		t.Fatal("attribute-only startup info unexpectedly set STARTF_USESTDHANDLES")
	}
}

// TestSecurityEnvironmentStartupInfoRejectsMissingHandle locks the fail-closed
// path: without a security environment handle there is nothing to attach, and
// the helper refuses to build an attribute list that would launch unsandboxed.
func TestSecurityEnvironmentStartupInfoRejectsMissingHandle(t *testing.T) {
	if _, err := newSecurityEnvironmentStartupInfo(0, true); err == nil ||
		!strings.Contains(err.Error(), "process security environment handle") {
		t.Fatalf("error = %v, want missing-handle failure", err)
	}
	if _, err := createSecurityEnvironment(nil); err == nil ||
		!strings.Contains(err.Error(), "non-empty process security environment specification") {
		t.Fatalf("error = %v, want empty-spec failure", err)
	}
}

// TestLaunchNativeFailsClosed locks the policy gates that keep launch() from
// starting a command when the sandbox cannot enforce what was requested.
func TestLaunchNativeFailsClosed(t *testing.T) {
	if _, err := launchNative(nil); err == nil || !strings.Contains(err.Error(), "invalid MXC launcher request") {
		t.Fatalf("nil request error = %v", err)
	}
	permissive := NativeContainerPolicy{Capabilities: []string{"permissiveLearningMode"}}
	if err := validateLaunchablePolicy(permissive, NativeSupport{Available: true}); err == nil ||
		!strings.Contains(err.Error(), "does not support permissiveLearningMode") {
		t.Fatalf("permissiveLearningMode error = %v", err)
	}
	denyPath := NativeContainerPolicy{DeniedPaths: []string{`C:\secret`}}
	if err := validateLaunchablePolicy(denyPath, NativeSupport{Available: true, FileSystemDeny: false}); err == nil ||
		!strings.Contains(err.Error(), "cannot enforce native MXC deny paths") {
		t.Fatalf("deny path without capability error = %v", err)
	}
	if err := validateLaunchablePolicy(denyPath, NativeSupport{Available: true, FileSystemDeny: true}); err != nil {
		t.Fatalf("deny path with capability error = %v", err)
	}
	badNetwork := NativeContainerPolicy{
		NetworkSpecified: true,
		NetworkEgress: NetworkEgressPolicy{
			Default: NetworkActionDeny,
			Allow:   []NetworkRule{{To: []NetworkPeer{{CIDR: NetworkCIDR{Address: "not-an-address", PrefixLen: 8}}}}},
		},
	}
	if err := validateLaunchablePolicy(badNetwork, NativeSupport{Available: true}); err == nil ||
		!strings.Contains(err.Error(), "invalid MXC network peer") {
		t.Fatalf("invalid CIDR error = %v", err)
	}
}
