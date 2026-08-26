package sandbox

import "testing"

func TestParseSetupCommand(t *testing.T) {
	cmd, ok, err := ParseSetupCommand([]string{"setup", "--elevated", "--user", `DOMAIN\alice`, "--codex-home", `C:\Users\alice\.gcode`})
	if err != nil || !ok {
		t.Fatalf("ParseSetupCommand() = %#v %v %v", cmd, ok, err)
	}
	if cmd.User != `DOMAIN\alice` || !cmd.Elevated {
		t.Fatalf("unexpected command: %#v", cmd)
	}
}

func TestParseIgnoresNonSetup(t *testing.T) {
	cmd, ok, err := ParseSetupCommand([]string{"echo", "hello"})
	if err != nil || ok || cmd != nil {
		t.Fatalf("ParseSetupCommand(non setup) = %#v %v %v", cmd, ok, err)
	}
}

func TestResolveCurrentUserIdentity(t *testing.T) {
	t.Setenv("USERNAME", "alice")
	cmd := &SetupCommand{Elevated: true, CurrentUser: true}
	identity, err := ResolveSetupIdentity(cmd, `/tmp/codex`)
	if err != nil {
		t.Fatalf("ResolveSetupIdentity() error = %v", err)
	}
	if identity.RealUser != "alice" || identity.CodexHome == "" {
		t.Fatalf("unexpected identity: %#v", identity)
	}
}

func TestValidateRequiresCodexHomeForManagedUser(t *testing.T) {
	err := (&SetupCommand{Elevated: true, User: "alice"}).Validate()
	if err == nil {
		t.Fatalf("expected missing codex home error")
	}
}
