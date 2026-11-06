//go:build windows

package windowssandbox

import (
	"errors"
	"testing"
)

// Rust #42309 prepare_elevated_sandbox: elevated provisioning only runs while
// setup is incomplete, and the ACL refresh always runs so the active permission
// profile supplies the read, write and deny roots.
func TestPrepareElevatedSandboxAlwaysRefreshesLikeRust(t *testing.T) {
	cases := []struct {
		name         string
		complete     bool
		wantElevated int
		wantRefresh  int
	}{
		{name: "incomplete provisions then refreshes", complete: false, wantElevated: 1, wantRefresh: 1},
		{name: "complete only refreshes", complete: true, wantElevated: 0, wantRefresh: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			elevatedCalls, refreshCalls := 0, 0
			restore := swapPrepareHooks(t)
			defer restore()
			prepareSandboxSetupIsComplete = func(string) (bool, error) { return tc.complete, nil }
			prepareRunElevatedSetup = func(req *SandboxSetupRequest) error {
				elevatedCalls++
				return nil
			}
			prepareRunSetupRefresh = func(req *SandboxSetupRequest) error {
				if req == nil || req.Permissions == nil {
					t.Fatal("refresh must receive the resolved request permissions")
				}
				refreshCalls++
				return nil
			}

			req := &SandboxSetupRequest{
				CodexHome:   t.TempDir(),
				CommandCWD:  `C:\\work`,
				Permissions: &ResolvedWindowsSandboxPermissions{},
			}
			if err := PrepareElevatedSandbox(req); err != nil {
				t.Fatalf("PrepareElevatedSandbox: %v", err)
			}

			if elevatedCalls != tc.wantElevated || refreshCalls != tc.wantRefresh {
				t.Fatalf("elevated=%d refresh=%d, want elevated=%d refresh=%d", elevatedCalls, refreshCalls, tc.wantElevated, tc.wantRefresh)
			}
		})
	}
}

func TestPrepareElevatedSandboxStopsOnElevatedFailureLikeRust(t *testing.T) {
	restore := swapPrepareHooks(t)
	defer restore()
	wantErr := errors.New("elevation failed")
	prepareSandboxSetupIsComplete = func(string) (bool, error) { return false, nil }
	prepareRunElevatedSetup = func(*SandboxSetupRequest) error { return wantErr }
	refreshCalled := false
	prepareRunSetupRefresh = func(*SandboxSetupRequest) error {
		refreshCalled = true
		return nil
	}

	err := PrepareElevatedSandbox(&SandboxSetupRequest{CodexHome: t.TempDir()})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if refreshCalled {
		t.Fatal("refresh must not run after a provisioning failure")
	}
}

func TestPrepareElevatedSandboxRejectsNilRequest(t *testing.T) {
	if err := PrepareElevatedSandbox(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
}

// swapPrepareHooks installs no-op hooks and returns a restore func.
func swapPrepareHooks(t *testing.T) func() {
	t.Helper()
	prevComplete := prepareSandboxSetupIsComplete
	prevElevated := prepareRunElevatedSetup
	prevRefresh := prepareRunSetupRefresh
	restored := false
	return func() {
		if restored {
			return
		}
		restored = true
		prepareSandboxSetupIsComplete = prevComplete
		prepareRunElevatedSetup = prevElevated
		prepareRunSetupRefresh = prevRefresh
	}
}
