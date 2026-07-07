package appserverdaemon

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"codex_go/internal/install"
)

func TestUpdateModesForIdentities(t *testing.T) {
	same := ExecutableIdentityFromBytes([]byte("same"))
	runningChanged := ExecutableIdentityFromBytes([]byte("old"))
	managedChanged := ExecutableIdentityFromBytes([]byte("new"))

	mode, refresh := UpdateModesForIdentities(&same, &same)
	if mode != RestartIfVersionChanged || refresh != UpdaterRefreshNone {
		t.Fatalf("same identity modes = %s, %s", mode, refresh)
	}

	mode, refresh = UpdateModesForIdentities(&runningChanged, &managedChanged)
	if mode != RestartAlways || refresh != UpdaterRefreshReexecIfManagedBinaryChanged {
		t.Fatalf("changed identity modes = %s, %s", mode, refresh)
	}
}

func TestShouldReexecUpdater(t *testing.T) {
	if !ShouldReexecUpdater(UpdaterRefreshReexecIfManagedBinaryChanged, RestartRestarted) {
		t.Fatal("changed updater with restarted app-server should reexec")
	}
	for _, outcome := range []RestartIfRunningOutcome{RestartBusy, RestartNotRunning, RestartNotReady, RestartAlreadyCurrent} {
		if ShouldReexecUpdater(UpdaterRefreshReexecIfManagedBinaryChanged, outcome) {
			t.Fatalf("outcome %s unexpectedly reexecs updater", outcome)
		}
	}
	if ShouldReexecUpdater(UpdaterRefreshNone, RestartRestarted) {
		t.Fatal("refresh none unexpectedly reexecs updater")
	}
}

func TestCurrentUpdaterIdentityUsesExecutableBytes(t *testing.T) {
	identityOld := install.ExecutableIdentityFromBytes([]byte("old"))
	identityNew := install.ExecutableIdentityFromBytes([]byte("new"))
	options := &UpdateLoopOptions{
		CurrentExe: func() (string, error) { return "codex-old", nil },
		ReadFile: func(path string) ([]byte, error) {
			if path != "codex-old" {
				t.Fatalf("ReadFile path = %q", path)
			}
			return []byte("old"), nil
		},
	}
	identity, err := CurrentUpdaterIdentity(options)
	if err != nil {
		t.Fatalf("CurrentUpdaterIdentity error = %v", err)
	}
	if identity == nil || *identity != identityOld || *identity == identityNew {
		t.Fatalf("identity = %#v, want %#v", identity, identityOld)
	}
}

func TestUpdateOnceRestartsWhenUpdaterIdentityChanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("managed updater reexec is unsupported on Windows")
	}
	stubLifecycleManagedDaemon(t)
	home := t.TempDir()
	managedBin := filepath.Join(home, "packages", "standalone", "current", "codex")
	if err := os.MkdirAll(filepath.Dir(managedBin), 0o700); err != nil {
		t.Fatalf("MkdirAll managed bin error = %v", err)
	}
	if err := os.WriteFile(managedBin, []byte("new"), 0o700); err != nil {
		t.Fatalf("WriteFile managed bin error = %v", err)
	}
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }
	if _, err := runner.Run(LifecycleStart); err != nil {
		t.Fatalf("Run(start) error = %v", err)
	}
	options := &UpdateLoopOptions{
		InstallLatest: func(context.Context) error { return nil },
		ReadFile:      os.ReadFile,
		ReexecUpdater: func(path string) error {
			if path != managedBin {
				t.Fatalf("reexec path = %q, want %q", path, managedBin)
			}
			return nil
		},
	}
	runningIdentity := install.ExecutableIdentityFromBytes([]byte("old"))
	control, err := UpdateOnce(context.Background(), runner, &runningIdentity, options)
	if err != nil {
		t.Fatalf("UpdateOnce error = %v", err)
	}
	if control != UpdateLoopStop {
		t.Fatalf("control = %s", control)
	}
}
