package win

import "fmt"

const (
	ufAccountDisable  uint32 = 0x0002
	ufPasswordExpired uint32 = 0x800000
)

// readSandboxUserFlags mirrors Rust's local_user_flags (NetUserGetInfo level 1)
// and is replaced in tests.
var readSandboxUserFlags = ReadLocalUserFlags

// sandboxAccountNeedsFullSetup mirrors Rust #46043: an ACL refresh cannot
// rotate sandbox account passwords or rewrite the stored credentials, so a
// missing, disabled, or password-expired account requires full setup before
// logon.
func sandboxAccountNeedsFullSetup(flags uint32, found bool) bool {
	return !found || flags&(ufAccountDisable|ufPasswordExpired) != 0
}

// rejectUnrepairableSandboxAccountsDuringRefresh mirrors the registration
// refresh gate: a refresh-only pass must never repair or replace a sandbox
// account, so an account that needs full setup fails the refresh instead of
// having ACLs applied on top of unusable credentials.
func rejectUnrepairableSandboxAccountsDuringRefresh(usernames []string) error {
	for _, username := range usernames {
		flags, found, err := readSandboxUserFlags(username)
		if err != nil {
			return fmt.Errorf("read local sandbox user %s: %w", username, err)
		}
		if sandboxAccountNeedsFullSetup(flags, found) {
			return fmt.Errorf("registration refresh cannot repair or replace a sandbox account")
		}
	}
	return nil
}

// verifySandboxAccountPasswordsRepaired mirrors Rust's post-setup check: setup
// just rotated both passwords, so neither account may still carry
// UF_PASSWORD_EXPIRED. Unreadable or missing accounts are ignored, matching
// Rust's `if let Ok(Some(flags))`.
func verifySandboxAccountPasswordsRepaired(usernames []string) error {
	for _, username := range usernames {
		flags, found, err := readSandboxUserFlags(username)
		if err != nil || !found {
			continue
		}
		if flags&ufPasswordExpired != 0 {
			return fmt.Errorf("Windows sandbox account password is still expired after setup")
		}
	}
	return nil
}
