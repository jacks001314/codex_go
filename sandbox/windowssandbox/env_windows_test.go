//go:build windows

package windowssandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyNoNetworkToEnvCreatesDenybin pins the Windows denybin: the helper
// writes .bat/.cmd shims and prepends them with the Windows path-list separator,
// so the assertion is only meaningful on a Windows host.
func TestApplyNoNetworkToEnvCreatesDenybin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	env := map[string]string{"PATH": "C:\\Windows", "PATHEXT": ".COM;.EXE;.BAT;.CMD"}
	if err := ApplyNoNetworkToEnv(env); err != nil {
		t.Fatalf("ApplyNoNetworkToEnv() error = %v", err)
	}
	if env["SBX_NONET_ACTIVE"] != "1" || env["HTTP_PROXY"] != "http://127.0.0.1:9" {
		t.Fatalf("env = %#v", env)
	}
	// homedir caches the first lookup, so a repeated run can still resolve the
	// previous temporary home. Assert on the directory that actually landed on
	// PATH instead of rebuilding it from this iteration's HOME.
	head := strings.Split(env["PATH"], string(os.PathListSeparator))[0]
	if filepath.Base(head) != ".sbx-denybin" {
		t.Fatalf("PATH head = %q, want the denybin directory", head)
	}
	if env["PATH"] != head+string(os.PathListSeparator)+"C:\\Windows" {
		t.Fatalf("PATH = %q, want the denybin prepended to the original PATH", env["PATH"])
	}
	if env["PATHEXT"] != ".BAT;.CMD;.COM;.EXE" {
		t.Fatalf("PATHEXT = %q", env["PATHEXT"])
	}
	for _, name := range []string{"ssh.bat", "ssh.cmd", "scp.bat", "scp.cmd"} {
		if _, err := os.Stat(filepath.Join(head, name)); err != nil {
			t.Fatalf("denybin %s missing: %v", name, err)
		}
	}
}
