package sandbox

import (
	"strings"
	"testing"
)

// Rust #60517 moved the macOS inherited-terminal policy into the shared
// sandbox manager, so a prepared seatbelt plan is already hardened and other
// backends are untouched.
func TestCommandRunPlanAppliesInheritedTerminalPolicyLikeRust(t *testing.T) {
	policy := "(version 1)\n(allow default)\n"
	seatbelt := &CommandRunPlan{
		Command:     []string{"/usr/bin/sandbox-exec", "-p", policy, "echo", "hi"},
		SandboxType: SandboxTypeMacosSeatbelt,
	}
	if err := seatbelt.applyInheritedTerminalPolicy(); err != nil {
		t.Fatalf("applyInheritedTerminalPolicy() error = %v", err)
	}
	if len(seatbelt.Command) != 5 || !strings.Contains(seatbelt.Command[2], "deny file-ioctl") {
		t.Fatalf("seatbelt policy was not hardened: %#v", seatbelt.Command)
	}
	if !strings.HasPrefix(seatbelt.Command[0], "/usr/bin/sandbox-exec") {
		t.Fatalf("launcher changed: %#v", seatbelt.Command)
	}

	plain := &CommandRunPlan{Command: []string{"echo", "hi"}, SandboxType: SandboxTypeNone}
	if err := plain.applyInheritedTerminalPolicy(); err != nil {
		t.Fatalf("plain plan error = %v", err)
	}
	if len(plain.Command) != 2 || plain.Command[0] != "echo" {
		t.Fatalf("non-seatbelt plan mutated: %#v", plain.Command)
	}
}

// Rust #60517's WrappedSandboxCommand preserves the native executable path and
// the Linux helper's argv[0] instead of re-splitting a rendered command line.
func TestWrappedCommandPreservesNativeArgvLikeRust(t *testing.T) {
	plan := &CommandRunPlan{
		Command:             []string{"/opt/codex/bin/codex-linux-sandbox", "--sandbox-policy-cwd", "/work", "--", "echo", "hi"},
		CWD:                 "/work",
		CommandCWD:          "/work/sub",
		SandboxType:         SandboxTypeLinuxSeccomp,
		PermissionProfileID: "profile-1",
	}
	wrapped, err := plan.WrappedCommand()
	if err != nil {
		t.Fatalf("WrappedCommand() error = %v", err)
	}
	if len(wrapped.Argv) != 6 || wrapped.Argv[0] != "/opt/codex/bin/codex-linux-sandbox" || wrapped.Argv[5] != "hi" {
		t.Fatalf("argv = %#v", wrapped.Argv)
	}
	if wrapped.Dir != "/work/sub" {
		t.Fatalf("dir = %q, want the command cwd", wrapped.Dir)
	}
	if wrapped.RequiresSessionLauncher() {
		t.Fatal("a Linux helper launch must not require the Windows session launcher")
	}

	process := wrapped.ProcessCommand(t.Context())
	if process == nil || process.Path != "/opt/codex/bin/codex-linux-sandbox" {
		t.Fatalf("process = %#v", process)
	}
	if len(process.Args) != 6 || process.Args[0] != "/opt/codex/bin/codex-linux-sandbox" {
		t.Fatalf("process args = %#v", process.Args)
	}
}

// Native Windows token sandboxes must launch through the session wrapper.
func TestWrappedCommandRequiresSessionLauncherForWindowsTokenSandboxLikeRust(t *testing.T) {
	for _, sandboxType := range []SandboxType{SandboxTypeWindowsRestrictedToken, SandboxTypeWindowsMxc} {
		wrapped := &WrappedSandboxCommand{Argv: []string{"cmd.exe"}, SandboxType: sandboxType}
		if !wrapped.RequiresSessionLauncher() {
			t.Fatalf("%v must require the session launcher", sandboxType)
		}
	}
	if (&WrappedSandboxCommand{Argv: []string{"echo"}}).RequiresSessionLauncher() {
		t.Fatal("an unsandboxed launch must not require the session launcher")
	}
	if _, err := (*CommandRunPlan)(nil).WrappedCommand(); err == nil {
		t.Fatal("a nil plan must not produce a wrapped command")
	}
	if _, err := (&CommandRunPlan{}).WrappedCommand(); err == nil {
		t.Fatal("a plan without a command must not produce a wrapped command")
	}
}
