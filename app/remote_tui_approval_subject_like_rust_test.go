package app

import (
	"testing"

	"codex_go/appserver"
)

// Rust #59256: approval notifications name terminal input and network hosts
// instead of always showing a command.
func TestRemoteCommandExecutionApprovalSubjectLikeRust(t *testing.T) {
	writeStdin := &appserver.CommandExecutionRequestApprovalParams{Kind: appserver.ExecApprovalKindWriteStdin}
	if got := remoteCommandExecutionApprovalSubject(writeStdin); got != "send terminal input" {
		t.Fatalf("write stdin subject = %q", got)
	}
	network := &appserver.CommandExecutionRequestApprovalParams{
		Kind:                   appserver.ExecApprovalKindCommand,
		NetworkApprovalContext: map[string]any{"host": "example.com"},
		Command:                stringPtr("curl https://example.com"),
	}
	if got := remoteCommandExecutionApprovalSubject(network); got != "network access to example.com" {
		t.Fatalf("network subject = %q", got)
	}
	command := &appserver.CommandExecutionRequestApprovalParams{
		Kind:    appserver.ExecApprovalKindCommand,
		Command: stringPtr("python3 -c 'print(1)'"),
	}
	if got := remoteCommandExecutionApprovalSubject(command); got != "python3 -c 'print(1)'" {
		t.Fatalf("command subject = %q", got)
	}
	if got := remoteCommandExecutionApprovalSubject(nil); got != "" {
		t.Fatalf("nil subject = %q", got)
	}
}
