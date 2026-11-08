//go:build windows

package mxc

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNativeLaunchSmoke is the only test that really starts a process inside a
// process security environment; it is opt-in because it depends on the host
// build. Run with CODEX_MXC_SMOKE=1.
//
//	go test ./sandbox/mxc -run TestNativeLaunchSmoke -v
func TestNativeLaunchSmoke(t *testing.T) {
	if os.Getenv("CODEX_MXC_SMOKE") != "1" {
		t.Skip("set CODEX_MXC_SMOKE=1 to launch a real MXC sandboxed process")
	}
	support := ProbeNativeSupport()
	if !support.Available {
		t.Skipf("process security environment unavailable: %s", support.Reason)
	}
	temp := t.TempDir()
	if err := os.WriteFile(filepath.Join(temp, "visible.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(temp, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	request := &NativeRequest{
		ScriptCode:       `cmd.exe /c exit 7`,
		WorkingDirectory: temp,
		Policy: NativeContainerPolicy{
			Capabilities:     []string{"registryRead"},
			ReadWritePaths:   []string{temp},
			DeniedPaths:      []string{filepath.Join(temp, "secret.txt")},
			NetworkEgress:    NetworkEgressPolicy{Default: NetworkActionDeny},
			NetworkIngress:   NetworkIngressPolicy{Default: NetworkActionDeny, HostLoopback: NetworkActionAllow},
			NetworkSpecified: true,
			BaseProcessUI:    BaseProcessUI{Isolation: "desktop"},
		},
	}
	if code, err := launchNative(request); err != nil || code != 7 {
		t.Fatalf("launchNative(exit 7) = %d, %v; want 7, nil", code, err)
	}

	readable := *request
	readable.ScriptCode = `cmd.exe /c type visible.txt`
	if code, err := launchNative(&readable); err != nil || code != 0 {
		t.Fatalf("launchNative(type visible) = %d, %v; want 0, nil", code, err)
	}

	denied := *request
	denied.ScriptCode = `cmd.exe /c type secret.txt`
	code, err := launchNative(&denied)
	if err != nil {
		t.Fatalf("launchNative(type denied) error = %v", err)
	}
	if code == 0 && support.FileSystemDeny {
		t.Fatalf("reading a denied path succeeded; PSE_SUPPORT_FS_DENY is advertised")
	}
}
