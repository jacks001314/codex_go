package win

import (
	"errors"
	"strings"
	"testing"
)

func withSandboxUserFlags(t *testing.T, read func(string) (uint32, bool, error)) {
	t.Helper()
	previous := readSandboxUserFlags
	readSandboxUserFlags = read
	t.Cleanup(func() { readSandboxUserFlags = previous })
}

// Mirrors Rust #46043: a missing, disabled, or password-expired account needs
// full setup; an ordinary account does not.
func TestSandboxAccountNeedsFullSetupLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		flags uint32
		found bool
		want  bool
	}{
		{name: "missing account", found: false, want: true},
		{name: "normal account", flags: 0x0200, found: true},
		{name: "password-never-expires flag", flags: 0x10000, found: true},
		{name: "disabled account", flags: ufAccountDisable, found: true, want: true},
		{name: "expired password", flags: ufPasswordExpired, found: true, want: true},
		{name: "disabled and expired", flags: ufAccountDisable | ufPasswordExpired, found: true, want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := sandboxAccountNeedsFullSetup(testCase.flags, testCase.found); got != testCase.want {
				t.Fatalf("sandboxAccountNeedsFullSetup(%#x, %v) = %v, want %v", testCase.flags, testCase.found, got, testCase.want)
			}
		})
	}
}

// Mirrors Rust's registration refresh gate: refresh-only passes never repair
// or replace a sandbox account.
func TestRejectUnrepairableSandboxAccountsDuringRefreshLikeRust(t *testing.T) {
	const offline, online = "CodexSandboxOffline", "CodexSandboxOnline"
	for _, testCase := range []struct {
		name    string
		flags   map[string]uint32
		missing map[string]bool
		readErr error
		wantErr string
	}{
		{name: "healthy accounts", flags: map[string]uint32{offline: 0x0200, online: 0x0200}},
		{name: "expired offline password", flags: map[string]uint32{offline: ufPasswordExpired, online: 0x0200}, wantErr: "registration refresh cannot repair or replace a sandbox account"},
		{name: "expired online password", flags: map[string]uint32{offline: 0x0200, online: ufPasswordExpired}, wantErr: "registration refresh cannot repair or replace a sandbox account"},
		{name: "disabled offline account", flags: map[string]uint32{offline: ufAccountDisable, online: 0x0200}, wantErr: "registration refresh cannot repair or replace a sandbox account"},
		{name: "missing online account", flags: map[string]uint32{offline: 0x0200}, missing: map[string]bool{online: true}, wantErr: "registration refresh cannot repair or replace a sandbox account"},
		{name: "flag read failed", readErr: errors.New("access denied"), wantErr: "read local sandbox user"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			withSandboxUserFlags(t, func(username string) (uint32, bool, error) {
				if testCase.readErr != nil {
					return 0, false, testCase.readErr
				}
				if testCase.missing[username] {
					return 0, false, nil
				}
				return testCase.flags[username], true, nil
			})
			err := rejectUnrepairableSandboxAccountsDuringRefresh([]string{offline, online})
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("error = %v, want %q", err, testCase.wantErr)
			}
		})
	}
}

// Mirrors Rust's post-setup verification: both passwords were rotated, so
// neither account may still be flagged expired; unreadable accounts are
// ignored rather than treated as failures.
func TestVerifySandboxAccountPasswordsRepairedLikeRust(t *testing.T) {
	const offline, online = "CodexSandboxOffline", "CodexSandboxOnline"
	for _, testCase := range []struct {
		name    string
		flags   map[string]uint32
		missing map[string]bool
		readErr error
		wantErr bool
	}{
		{name: "repaired accounts", flags: map[string]uint32{offline: 0x0200, online: 0x0200}},
		{name: "other flags preserved", flags: map[string]uint32{offline: 0x0210, online: 0x0201}},
		{name: "offline still expired", flags: map[string]uint32{offline: ufPasswordExpired, online: 0x0200}, wantErr: true},
		{name: "online still expired", flags: map[string]uint32{offline: 0x0200, online: ufPasswordExpired}, wantErr: true},
		{name: "unreadable account ignored", missing: map[string]bool{offline: true}, flags: map[string]uint32{online: 0x0200}},
		{name: "read error ignored", readErr: errors.New("access denied")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			withSandboxUserFlags(t, func(username string) (uint32, bool, error) {
				if testCase.readErr != nil {
					return 0, false, testCase.readErr
				}
				if testCase.missing[username] {
					return 0, false, nil
				}
				return testCase.flags[username], true, nil
			})
			err := verifySandboxAccountPasswordsRepaired([]string{offline, online})
			if !testCase.wantErr {
				if err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "still expired after setup") {
				t.Fatalf("error = %v, want still-expired failure", err)
			}
		})
	}
}
