package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeNullDeviceEnv(t *testing.T) {
	env := map[string]string{"A": "/dev/null", "B": `\\dev\\null`, "C": "keep"}
	NormalizeNullDeviceEnv(env)
	if env["A"] != "NUL" || env["B"] != "NUL" || env["C"] != "keep" {
		t.Fatalf("env = %#v", env)
	}
}

func TestEnsureNonInteractivePagerPreservesExisting(t *testing.T) {
	env := map[string]string{"PAGER": "cat"}
	EnsureNonInteractivePager(env)
	if env["PAGER"] != "cat" || env["GIT_PAGER"] != "more.com" {
		t.Fatalf("env = %#v", env)
	}
	if _, ok := env["LESS"]; !ok {
		t.Fatalf("LESS missing: %#v", env)
	}
}

func TestApplyNoNetworkToEnvCreatesDenybin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	env := map[string]string{"PATH": "C:\\Windows", "PATHEXT": ".COM;.EXE;.BAT;.CMD"}
	if err := ApplyNoNetworkToEnv(env); err != nil {
		t.Fatalf("ApplyNoNetworkToEnv() error = %v", err)
	}
	base := filepath.Join(home, ".sbx-denybin")
	if env["SBX_NONET_ACTIVE"] != "1" || env["HTTP_PROXY"] != "http://127.0.0.1:9" {
		t.Fatalf("env = %#v", env)
	}
	if env["PATH"][:len(base)] != base {
		t.Fatalf("PATH = %q, want prefix %q", env["PATH"], base)
	}
	if env["PATHEXT"] != ".BAT;.CMD;.COM;.EXE" {
		t.Fatalf("PATHEXT = %q", env["PATHEXT"])
	}
	for _, name := range []string{"ssh.bat", "ssh.cmd", "scp.bat", "scp.cmd"} {
		if _, err := os.Stat(filepath.Join(base, name)); err != nil {
			t.Fatalf("denybin %s missing: %v", name, err)
		}
	}
}
