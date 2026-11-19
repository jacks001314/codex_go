package windowssandbox

import (
	"strings"
	"testing"

	coresandbox "codex_go/sandbox"
)

// Rust #62141: valid cached credentials must not skip setup when protected Core
// is requested but its registration is missing or stale, and setup must end with
// a ready registration before credentials are returned.
func TestRequireLogonSandboxCredsRunsSetupForUnreadyCoreRegistrationLikeRust(t *testing.T) {
	oldSetup := runElevatedSetupForCredentials
	oldRefresh := runSetupRefreshForCredentials
	oldSelect := selectSandboxCredentialsForCredentials
	oldRepair := storedCredentialsNeedRepairForCredentials
	oldRegistration := registrationSetupRequiredForCredentials
	defer func() {
		runElevatedSetupForCredentials = oldSetup
		runSetupRefreshForCredentials = oldRefresh
		selectSandboxCredentialsForCredentials = oldSelect
		storedCredentialsNeedRepairForCredentials = oldRepair
		registrationSetupRequiredForCredentials = oldRegistration
	}()

	cases := []struct {
		name           string
		unreadyBefore  bool
		unreadyAfter   bool
		wantSetupCalls int
		wantErr        string
	}{
		{name: "ready registration keeps cached credentials", wantSetupCalls: 0},
		{name: "unready registration runs setup once", unreadyBefore: true, wantSetupCalls: 1},
		{name: "unready registration after setup fails", unreadyBefore: true, unreadyAfter: true, wantSetupCalls: 1, wantErr: "protected Core setup did not publish ready runtime ownership"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			setupCalls := 0
			runElevatedSetupForCredentials = func(*SandboxSetupRequest) error {
				setupCalls++
				return nil
			}
			runSetupRefreshForCredentials = func(*SandboxSetupRequest) error { return nil }
			storedCredentialsNeedRepairForCredentials = func(string) (bool, error) { return false, nil }
			selectSandboxCredentialsForCredentials = func(string, SandboxNetworkIdentity) (*SandboxCredentials, error) {
				return &SandboxCredentials{Username: OfflineUsername, Password: "pw", Domain: "."}, nil
			}
			registrationChecks := 0
			registrationSetupRequiredForCredentials = func(string) bool {
				registrationChecks++
				if registrationChecks == 1 {
					return test.unreadyBefore
				}
				return test.unreadyAfter
			}

			home := t.TempDir()
			if err := WriteSetupMarker(home, &SetupMarker{
				Version:         SetupVersion,
				OfflineUsername: OfflineUsername,
				OnlineUsername:  OnlineUsername,
			}); err != nil {
				t.Fatalf("WriteSetupMarker() error = %v", err)
			}
			profile := coresandbox.ReadOnlyPermissionProfile()
			permissions, err := ResolvePermissions(&profile, []string{`C:\repo`})
			if err != nil {
				t.Fatalf("ResolvePermissions() error = %v", err)
			}
			_, err = RequireLogonSandboxCredsForPermissions(
				permissions,
				`C:\repo`,
				map[string]string{},
				home,
				nil,
				false,
				false,
				nil,
				false,
				nil,
				nil,
				false,
				ProxySettingsPreserve,
			)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("RequireLogonSandboxCredsForPermissions() error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, test.wantErr)
			}
			if setupCalls != test.wantSetupCalls {
				t.Fatalf("setup calls = %d, want %d", setupCalls, test.wantSetupCalls)
			}
		})
	}
}
