//go:build darwin

package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Mirrors Rust macos_security_tests::only_matching_enforced_apple_security_events_are_failures.
func TestDoctorMacSecurityEventClassificationMatchesRust(t *testing.T) {
	for _, tc := range []struct {
		event string
		want  macSecurityEvidence
	}{
		{"denied com.openai.codex", macSecurityBlocked},
		{"denied /Applications/Codex.app/Contents/MacOS/Codex", macSecurityBlocked},
		{"malware detected ChatGPT.app", macSecurityMalware},
		{"audit token blocked ChatGPT.app", macSecurityBlocked},
		{"audit token XP_MALWARE_DETECTED ChatGPT.app", macSecurityMalware},
		{"XP_MALWARE_REMEDIATED ChatGPT.app", macSecurityMalware},
		{"denied .plugin-appserver", macSecurityBlocked},
		{"audit would block ChatGPT.app", macSecurityAudit},
		{"OSStatus 100024 ChatGPT.app", macSecurityExhausted},
		{"not blocked ChatGPT.app", macSecurityClear},
		{"denied EMFILE com.example.other", macSecurityClear},
		{"EMFILE\nmalware detected ChatGPT.app", macSecurityMalware},
	} {
		if got := classifyMacSecurityEvents(tc.event); got != tc.want {
			t.Errorf("classifyMacSecurityEvents(%q) = %d, want %d", tc.event, got, tc.want)
		}
	}
}

// Mirrors Rust macos_security_tests::gatekeeper_failures_require_actionable_security_evidence.
func TestDoctorMacSecurityGatekeeperClassificationMatchesRust(t *testing.T) {
	if got := classifyMacGatekeeper(false, "ChatGPT.app: rejected"); got != macSecurityBlocked {
		t.Fatalf("rejected stderr = %d, want blocked", got)
	}
	if got := classifyMacGatekeeper(false, "operation not permitted"); got != macSecurityUnavailable {
		t.Fatalf("unactionable stderr = %d, want unavailable", got)
	}
	if got := classifyMacGatekeeper(true, ""); got != macSecurityClear {
		t.Fatalf("successful assessment = %d, want clear", got)
	}
}

// Mirrors Rust macos_security_tests::exhaustion_and_unavailable_history_have_actionable_warnings.
func TestDoctorMacSecurityCheckShapeMatchesRust(t *testing.T) {
	for _, tc := range []struct {
		name     string
		gate     macSecurityEvidence
		events   macSecurityEvidence
		status   CheckStatus
		contains string
	}{
		{"clear", macSecurityClear, macSecurityClear, CheckStatusOK, "passed available macos security assessments"},
		{"malware", macSecurityClear, macSecurityMalware, CheckStatusFail, "XProtect blocked or remediated"},
		{"gatekeeper", macSecurityBlocked, macSecurityClear, CheckStatusFail, "gatekeeper rejected"},
		{"blocked event", macSecurityClear, macSecurityBlocked, CheckStatusFail, "security event blocked"},
		{"exhausted", macSecurityClear, macSecurityExhausted, CheckStatusWarning, "file descriptor exhaustion"},
		{"audit", macSecurityClear, macSecurityAudit, CheckStatusWarning, "audit-only"},
		{"gatekeeper unavailable", macSecurityUnavailable, macSecurityClear, CheckStatusWarning, "assessment was unavailable"},
		{"events unavailable", macSecurityClear, macSecurityUnavailable, CheckStatusWarning, "history was unavailable"},
	} {
		check := macSecurityEnforcementCheck(tc.gate, tc.events)
		if check == nil {
			t.Fatalf("%s: no check", tc.name)
		}
		if check.ID != "desktop.security.enforcement" || check.Category != "desktop" {
			t.Fatalf("%s: id/category = %q/%q", tc.name, check.ID, check.Category)
		}
		if check.Status != tc.status {
			t.Fatalf("%s: status = %v, want %v", tc.name, check.Status, tc.status)
		}
		if !strings.Contains(check.Summary, tc.contains) {
			t.Fatalf("%s: summary = %q, want contains %q", tc.name, check.Summary, tc.contains)
		}
		if tc.name == "clear" {
			if len(check.Details) != 1 || check.Details[0] != "gatekeeper: accepted" {
				t.Fatalf("clear details = %#v", check.Details)
			}
			if check.Remediation != nil {
				t.Fatalf("clear remediation = %q", *check.Remediation)
			}
			continue
		}
		if check.Remediation == nil || *check.Remediation == "" {
			t.Fatalf("%s: expected remediation", tc.name)
		}
	}
}

// The macOS desktop log root and the day directory must use "/" separators,
// otherwise the session probe never sees today's logs.
func TestDoctorMacDesktopLogRootUsesPlatformSeparator(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := desktopLogRoot(desktopMacBundleIdentifier)
	want := filepath.Join(home, "Library", "Logs", desktopMacBundleIdentifier)
	if root != want {
		t.Fatalf("desktopLogRoot = %q, want %q", root, want)
	}
	if got, want := redactedDesktopPath(root), "$HOME/Library/Logs/"+desktopMacBundleIdentifier; got != want {
		t.Fatalf("redactedDesktopPath = %q, want %q", got, want)
	}
	day := filepath.Join(root, "2026/11/08")
	if !strings.HasPrefix(day, want+string(os.PathSeparator)) {
		t.Fatalf("day directory = %q, want under %q", day, want)
	}
}
