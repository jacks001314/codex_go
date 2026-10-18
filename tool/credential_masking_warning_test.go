package tool

import (
	"strings"
	"testing"
)

func TestRemoteCredentialMaskingWarningsLikeRust(t *testing.T) {
	var warnings []string
	req := &ShellRequest{
		UnifiedExecEnvironmentID: "remote-a",
		Shell:                    ShellInvocation{Shell: &Shell{Type: ShellBash}},
		WarningSink: func(message string) {
			warnings = append(warnings, message)
		},
	}
	warnRemoteCredentialMaskingUnavailable(req, true, false)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "does not support it") {
		t.Fatalf("capability warning = %#v", warnings)
	}

	warnings = nil
	req.Shell.Shell.Type = ShellZsh
	warnRemoteCredentialMaskingUnavailable(req, true, true)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "shell `zsh`") {
		t.Fatalf("shell warning = %#v", warnings)
	}

	warnings = nil
	req.Shell.Shell.Type = ShellBash
	req.Shell.UseLoginShell = true
	warnRemoteCredentialMaskingUnavailable(req, true, true)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "shell startup") {
		t.Fatalf("startup warning = %#v", warnings)
	}

	warnings = nil
	req.Shell.UseLoginShell = false
	warnRemoteCredentialMaskingUnavailable(req, true, true)
	if len(warnings) != 0 {
		t.Fatalf("supported launch warned: %#v", warnings)
	}
}
