package sandbox

import (
	"runtime"
	"strings"
	"testing"

	"codex_go/network"
)

func TestClassifyFileSystemSandboxViolationMatchesRust(t *testing.T) {
	for _, keyword := range []string{
		"operation not permitted",
		"permission denied",
		"read-only file system",
		"seccomp",
		"sandbox",
		"landlock",
		"failed to write file",
	} {
		if got := ClassifyFileSystemSandboxViolation(SandboxTypeLinuxSeccomp, SandboxExecOutput{ExitCode: 1, Stderr: keyword}); got == nil {
			t.Fatalf("keyword %q was not classified", keyword)
		}
	}

	for _, keyword := range []string{"seccomp", "sandbox", "landlock"} {
		got := ClassifyFileSystemSandboxViolation(SandboxTypeLinuxSeccomp, SandboxExecOutput{ExitCode: 1, Stderr: keyword})
		if got == nil || got.Reason != FileSystemSandboxViolationPolicyDenied || got.Backend != SandboxViolationBackendLinuxSandbox || got.OutputSnippet != keyword {
			t.Fatalf("backend keyword %q = %#v", keyword, got)
		}
	}
}

func TestClassifyFileSystemSandboxViolationPreservesRustOrdering(t *testing.T) {
	tests := []struct {
		name        string
		sandboxType SandboxType
		output      SandboxExecOutput
		want        bool
	}{
		{"quick reject", SandboxTypeLinuxSeccomp, SandboxExecOutput{ExitCode: 127, Stderr: "command not found"}, false},
		{"keyword before quick reject", SandboxTypeLinuxSeccomp, SandboxExecOutput{ExitCode: 127, Stderr: "Permission denied"}, true},
		{"zero exit", SandboxTypeLinuxSeccomp, SandboxExecOutput{ExitCode: 0, Stderr: "Operation not permitted"}, false},
		{"no sandbox", SandboxTypeNone, SandboxExecOutput{ExitCode: 1, Stderr: "Operation not permitted"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyFileSystemSandboxViolation(test.sandboxType, test.output) != nil; got != test.want {
				t.Fatalf("classified = %t, want %t", got, test.want)
			}
		})
	}
}

func TestClassifyFileSystemSandboxViolationExtractsPathAndMatchedStream(t *testing.T) {
	got := ClassifyFileSystemSandboxViolation(SandboxTypeMacosSeatbelt, SandboxExecOutput{
		ExitCode:         1,
		Stdout:           "bash: /private/tmp/İ-denied: Permission denied",
		Stderr:           "unrelated warning",
		AggregatedOutput: "unrelated warning\nbash: /private/tmp/İ-denied: Permission denied",
	})
	if got == nil || got.Path == nil || *got.Path != "/private/tmp/İ-denied" {
		t.Fatalf("violation path = %#v", got)
	}
	if got.Reason != FileSystemSandboxViolationPermissionDenied || got.OutputSnippet != "bash: /private/tmp/İ-denied: Permission denied" {
		t.Fatalf("violation = %#v", got)
	}

	aggregated := ClassifyFileSystemSandboxViolation(SandboxTypeMacosSeatbelt, SandboxExecOutput{
		ExitCode: 101, AggregatedOutput: "cargo failed: Read-only file system when writing target",
	})
	if aggregated == nil || aggregated.Reason != FileSystemSandboxViolationReadOnlyFileSystem {
		t.Fatalf("aggregated violation = %#v", aggregated)
	}
}

func TestClassifyFileSystemSandboxViolationTruncatesUnicodeSnippetByCharacter(t *testing.T) {
	text := strings.Repeat("界", outputSnippetMaxChars+10) + " permission denied"
	got := ClassifyFileSystemSandboxViolation(SandboxTypeWindowsRestrictedToken, SandboxExecOutput{ExitCode: 1, Stderr: text})
	if got == nil || len([]rune(got.OutputSnippet)) != outputSnippetMaxChars {
		t.Fatalf("snippet rune count = %d", len([]rune(got.OutputSnippet)))
	}
	if got.Backend != SandboxViolationBackendWindowsSandbox {
		t.Fatalf("backend = %q", got.Backend)
	}
}

func TestClassifyFileSystemSandboxViolationLinuxSIGSYS(t *testing.T) {
	exitCode, supported := sandboxSIGSYSExitCode()
	got := ClassifyFileSystemSandboxViolation(SandboxTypeLinuxSeccomp, SandboxExecOutput{ExitCode: exitCode})
	if supported {
		if got == nil || got.Reason != FileSystemSandboxViolationSignalSyscall {
			t.Fatalf("SIGSYS violation = %#v", got)
		}
	} else if got != nil {
		t.Fatalf("SIGSYS classified on %s: %#v", runtime.GOOS, got)
	}
}

func TestNetworkSandboxViolationFromBlockedRequestMatchesRust(t *testing.T) {
	mode := network.ProxyModeLimited
	port := uint16(443)
	blocked := network.ProxyBlockedRequest{
		Host: "example.com", Reason: "not_allowed", Client: "curl", Method: "CONNECT",
		Mode: &mode, Protocol: "https", Decision: "deny", Source: network.ProxyDecisionSourceBaselinePolicy,
		Port: &port, Timestamp: 42,
	}
	got := NetworkSandboxViolationFromBlockedRequest(blocked)
	if got.Backend != SandboxViolationBackendManagedNetworkProxy || got.Host != blocked.Host || got.Reason != blocked.Reason ||
		got.Client == nil || *got.Client != "curl" || got.Method == nil || *got.Method != "CONNECT" ||
		got.Mode == nil || *got.Mode != mode || got.Protocol != "https" || got.Decision == nil || *got.Decision != "deny" ||
		got.Source == nil || *got.Source != network.ProxyDecisionSourceBaselinePolicy || got.Port == nil || *got.Port != port || got.Timestamp != 42 {
		t.Fatalf("network violation = %#v", got)
	}
}
