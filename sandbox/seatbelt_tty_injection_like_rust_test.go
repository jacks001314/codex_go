package sandbox

import (
	"strings"
	"testing"
)

// TestHardenSeatbeltArgsForInheritedTerminalDeniesTIOCSTILikeRust covers Rust
// #42590: the sandbox CLI appends a TIOCSTI ioctl deny to the generated
// Seatbelt policy so a sandboxed child cannot inject input into the shell that
// resumes after Codex exits.
func TestHardenSeatbeltArgsForInheritedTerminalDeniesTIOCSTILikeRust(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	args, err := createSeatbeltCommandArgs([]string{"/bin/sh", "-c", "true"}, "/workspace", &profile, nil, "")
	if err != nil {
		t.Fatalf("createSeatbeltCommandArgs: %v", err)
	}
	originalPolicy := ""
	policyIndex := -1
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "-p" {
			policyIndex = index
			originalPolicy = args[index+1]
			break
		}
	}
	if policyIndex < 0 {
		t.Fatalf("generated args have no policy: %#v", args)
	}

	hardened, err := HardenSeatbeltArgsForInheritedTerminal(args)
	if err != nil {
		t.Fatalf("HardenSeatbeltArgsForInheritedTerminal: %v", err)
	}
	const deny = "(deny file-ioctl (ioctl-command TIOCSTI))"
	if !strings.HasSuffix(hardened[policyIndex+1], deny+"\n") {
		t.Fatalf("hardened policy does not end with the TIOCSTI deny:\n%s", hardened[policyIndex+1])
	}
	if strings.Contains(originalPolicy, deny) {
		t.Fatalf("helper mutated the generated policy: %q", originalPolicy)
	}
	if got, want := strings.Join(hardened[policyIndex+2:], "\x00"), strings.Join(args[policyIndex+2:], "\x00"); got != want {
		t.Fatalf("command tail changed: %q, want %q", hardened[policyIndex+2:], args[policyIndex+2:])
	}

	if _, err := HardenSeatbeltArgsForInheritedTerminal([]string{macOSSeatbeltExecutable, "/bin/true"}); err == nil {
		t.Fatal("helper accepted args without a generated policy")
	}
}
