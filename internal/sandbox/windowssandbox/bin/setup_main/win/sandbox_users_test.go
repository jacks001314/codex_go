package win

import "testing"

func TestRandomSandboxPassword(t *testing.T) {
	got, err := RandomSandboxPassword()
	if err != nil {
		t.Fatalf("RandomSandboxPassword() error = %v", err)
	}
	if len(got) != 24 {
		t.Fatalf("password length = %d, want 24", len(got))
	}
}
