package tool

import (
	"strings"
	"testing"

	"codex_go/execserver"
)

func TestRemoteCredentialMaskingWarningsLikeRust(t *testing.T) {
	var warnings []string
	req := &ShellRequest{
		UnifiedExecEnvironmentID: "remote-a",
		RemoteNetworkProxy: &execserver.RemoteNetworkProxyLaunchConfig{
			Proxy: execserver.RemoteNetworkProxyConfig{Enabled: true, CredentialMasking: true},
		},
		Shell: ShellInvocation{Shell: &Shell{Type: ShellBash}},
		WarningSink: func(message string) {
			warnings = append(warnings, message)
		},
	}
	warnRemoteCredentialMaskingUnavailable(req, true, execserver.RemoteCredentialMaskingDisabled)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "does not support it") {
		t.Fatalf("capability warning = %#v", warnings)
	}

	warnings = nil
	req.Shell.Shell.Type = ShellZsh
	warnRemoteCredentialMaskingUnavailable(req, true, execserver.RemoteCredentialMaskingBuiltInOnly)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "shell startup") {
		t.Fatalf("shell warning = %#v", warnings)
	}

	warnings = nil
	req.Shell.Shell.Type = ShellBash
	req.Shell.UseLoginShell = true
	warnRemoteCredentialMaskingUnavailable(req, true, execserver.RemoteCredentialMaskingBuiltInOnly)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "shell startup") {
		t.Fatalf("startup warning = %#v", warnings)
	}

	warnings = nil
	req.Shell.UseLoginShell = false
	warnRemoteCredentialMaskingUnavailable(req, true, execserver.RemoteCredentialMaskingBuiltInOnly)
	if len(warnings) != 0 {
		t.Fatalf("supported launch warned: %#v", warnings)
	}

	warnings = nil
	req.Shell.Shell.Type = ShellZsh
	req.Shell.UseLoginShell = true
	req.RemoteShellSnapshot = &execserver.ShellSnapshotRequest{ScopeID: "scope"}
	warnRemoteCredentialMaskingUnavailable(req, true, execserver.RemoteCredentialMaskingBuiltInOnly)
	if len(warnings) != 0 {
		t.Fatalf("zsh snapshot launch warned: %#v", warnings)
	}
}

func TestRemoteCredentialMaskingConfigurationWarningLikeRust(t *testing.T) {
	var warnings []string
	req := &ShellRequest{
		UnifiedExecEnvironmentID: "remote-a",
		RemoteNetworkProxy: &execserver.RemoteNetworkProxyLaunchConfig{
			Proxy: execserver.RemoteNetworkProxyConfig{Enabled: true},
		},
		Shell: ShellInvocation{Shell: &Shell{Type: ShellBash}},
		WarningSink: func(message string) {
			warnings = append(warnings, message)
		},
	}
	warnRemoteCredentialMaskingUnavailable(req, true, execserver.RemoteCredentialMaskingBuiltInOnly)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "network proxy configuration") {
		t.Fatalf("configuration warning = %#v", warnings)
	}

	warnings = nil
	req.RemoteNetworkProxy.Proxy.CredentialMasking = true
	warnRemoteCredentialMaskingUnavailable(req, false, execserver.RemoteCredentialMaskingBuiltInOnly)
	if len(warnings) != 0 {
		t.Fatalf("unrequested masking warned: %#v", warnings)
	}

	warnings = nil
	req.RemoteNetworkProxy.Proxy.Enabled = false
	warnRemoteCredentialMaskingUnavailable(req, true, execserver.RemoteCredentialMaskingDisabled)
	if len(warnings) != 0 {
		t.Fatalf("disabled proxy warned: %#v", warnings)
	}
}
