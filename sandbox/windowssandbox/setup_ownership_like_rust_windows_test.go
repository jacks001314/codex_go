//go:build windows

package windowssandbox

import "testing"

// TestSetupRealUserUsesProcessTokenLikeRust pins Rust #45550's
// `current_setup_user`: setup ownership comes from the requesting process
// token, never from an environment value that elevation can change.
func TestSetupRealUserUsesProcessTokenLikeRust(t *testing.T) {
	tokenUser, err := currentSetupUser()
	if err != nil {
		t.Fatalf("currentSetupUser() error = %v", err)
	}
	if tokenUser == "" || tokenUser == "Administrators" {
		t.Fatalf("currentSetupUser() = %q", tokenUser)
	}
	t.Setenv("USERNAME", "environment-only-name")
	t.Setenv("USER", "environment-only-name")
	if got := setupRealUser(""); got != tokenUser {
		t.Fatalf("setupRealUser() = %q, want token identity %q", got, tokenUser)
	}
	if got := setupRealUser(`DOMAIN\alice`); got != `DOMAIN\alice` {
		t.Fatalf("setupRealUser(override) = %q", got)
	}
}
