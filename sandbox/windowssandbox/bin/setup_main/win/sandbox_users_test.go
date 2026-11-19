package win

import (
	"strings"
	"testing"
)

// Rust random_password (#62094): every generated password satisfies the sandbox
// account policy, so assert over many draws rather than on one random value.
func TestRandomSandboxPasswordMeetsSandboxAccountPolicyLikeRust(t *testing.T) {
	const symbols = "!@#$%^&*()-_=+"
	const upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	const lower = "abcdefghijklmnopqrstuvwxyz"
	const digits = "0123456789"
	for draw := 0; draw < 256; draw++ {
		password, err := RandomSandboxPassword()
		if err != nil {
			t.Fatalf("RandomSandboxPassword() error = %v", err)
		}
		if len(password) != SandboxPasswordLength {
			t.Fatalf("password length = %d, want %d", len(password), SandboxPasswordLength)
		}
		allowed := upper + lower + digits + symbols
		for _, r := range password {
			if !strings.ContainsRune(allowed, r) {
				t.Fatalf("password %q contains %q outside the sandbox alphabet", password, r)
			}
		}
		for name, class := range map[string]string{"uppercase": upper, "lowercase": lower, "digit": digits, "symbol": symbols} {
			if !strings.ContainsAny(password, class) {
				t.Fatalf("password %q has no %s character", password, name)
			}
		}
	}
}
