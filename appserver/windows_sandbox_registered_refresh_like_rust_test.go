package appserver

import (
	"errors"
	"os"
	"runtime"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

// TestWindowsSandboxReadinessRefreshesStaleRegisteredCoreLikeRust pins the
// readiness half of Rust's windows_sandbox_processor refresh: only an opted-in
// registered Core without a workload identity refreshes, a current receipt
// skips the service, the refresh runs once per router, and a failed refresh
// still answers readiness so manual setup stays available.
func TestWindowsSandboxReadinessRefreshesStaleRegisteredCoreLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows sandbox readiness is Windows-only")
	}
	newRouter := func(t *testing.T) *RuntimeRouter {
		t.Helper()
		home := t.TempDir()
		if err := os.WriteFile(config.ConfigPath(home), []byte("[windows]\nsandbox = \"elevated\"\n"), 0o600); err != nil {
			t.Fatalf("write config fixture: %v", err)
		}
		return NewRuntimeRouter(RuntimeServices{
			Windows: sandbox.NewWindowsManager(sandbox.WindowsReadinessNotConfigured),
			Config:  config.NewConfigService(home),
		})
	}
	readiness := func(t *testing.T, router *RuntimeRouter) {
		t.Helper()
		response := router.Handle(requestWithParams(t, IntID(1), MethodWindowsSandboxReadiness, map[string]any{}))
		if response.Error != nil {
			t.Fatalf("readiness error: %+v", response.Error)
		}
	}

	t.Run("not requested", func(t *testing.T) {
		refreshNeeded, refreshes := 0, 0
		installWindowsSandboxRefreshSeams(t, false, false,
			func(string) (bool, error) { refreshNeeded++; return true, nil },
			func(string, provisionservice.Settings, provisionservice.Listeners) error { refreshes++; return nil },
		)
		readiness(t, newRouter(t))
		if refreshNeeded != 0 || refreshes != 0 {
			t.Fatalf("refresh without the registered Core opt-in: needsRefresh=%d refreshes=%d", refreshNeeded, refreshes)
		}
	})

	t.Run("workload identity", func(t *testing.T) {
		refreshNeeded, refreshes := 0, 0
		installWindowsSandboxRefreshSeams(t, true, true,
			func(string) (bool, error) { refreshNeeded++; return true, nil },
			func(string, provisionservice.Settings, provisionservice.Listeners) error { refreshes++; return nil },
		)
		readiness(t, newRouter(t))
		if refreshNeeded != 0 || refreshes != 0 {
			t.Fatalf("workload identity refreshed: needsRefresh=%d refreshes=%d", refreshNeeded, refreshes)
		}
	})

	t.Run("current receipt", func(t *testing.T) {
		refreshes := 0
		installWindowsSandboxRefreshSeams(t, true, false,
			func(string) (bool, error) { return false, nil },
			func(string, provisionservice.Settings, provisionservice.Listeners) error { refreshes++; return nil },
		)
		readiness(t, newRouter(t))
		if refreshes != 0 {
			t.Fatalf("refreshed a current receipt %d time(s)", refreshes)
		}
	})

	t.Run("stale receipt refreshes once per router", func(t *testing.T) {
		refreshes := 0
		installWindowsSandboxRefreshSeams(t, true, false,
			func(string) (bool, error) { return true, nil },
			func(string, provisionservice.Settings, provisionservice.Listeners) error { refreshes++; return nil },
		)
		router := newRouter(t)
		readiness(t, router)
		readiness(t, router)
		if refreshes != 1 {
			t.Fatalf("refreshes = %d, want 1", refreshes)
		}
	})

	t.Run("failed refresh keeps manual setup", func(t *testing.T) {
		refreshes := 0
		installWindowsSandboxRefreshSeams(t, true, false,
			func(string) (bool, error) { return true, nil },
			func(string, provisionservice.Settings, provisionservice.Listeners) error {
				refreshes++
				return errors.New("service unavailable")
			},
		)
		readiness(t, newRouter(t))
		if refreshes != 1 {
			t.Fatalf("refreshes = %d, want 1", refreshes)
		}
	})
}

func installWindowsSandboxRefreshSeams(
	t *testing.T,
	registeredCore bool,
	workloadIdentity bool,
	needsRefresh func(string) (bool, error),
	refresh func(string, provisionservice.Settings, provisionservice.Listeners) error,
) {
	t.Helper()
	prevRegistered := windowsSandboxRegisteredCoreRequested
	prevWorkload := windowsSandboxWorkloadIdentitySelected
	prevNeeds := windowsSandboxRegisteredCoreNeedsRefresh
	prevRefresh := refreshWindowsSandboxRegisteredCore
	windowsSandboxRegisteredCoreRequested = func() bool { return registeredCore }
	windowsSandboxWorkloadIdentitySelected = func() bool { return workloadIdentity }
	windowsSandboxRegisteredCoreNeedsRefresh = needsRefresh
	refreshWindowsSandboxRegisteredCore = refresh
	t.Cleanup(func() {
		windowsSandboxRegisteredCoreRequested = prevRegistered
		windowsSandboxWorkloadIdentitySelected = prevWorkload
		windowsSandboxRegisteredCoreNeedsRefresh = prevNeeds
		refreshWindowsSandboxRegisteredCore = prevRefresh
	})
}
