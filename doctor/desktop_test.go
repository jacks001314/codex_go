package doctor

import (
	"strings"
	"testing"
)

func TestDesktopChecksReportInstallationLikeRust(t *testing.T) {
	originalProbe := desktopInstallationProbe
	defer func() { desktopInstallationProbe = originalProbe }()
	desktopInstallationProbe = func() []desktopAppInstallation {
		return []desktopAppInstallation{
			{Name: "ChatGPT", Path: `C:\Program Files\OpenAI\ChatGPT.exe`, Version: "1.2.3", Platform: "windows"},
		}
	}
	checks := desktopChecks()
	if len(checks) != 3 {
		t.Fatalf("desktop checks = %d, want 3", len(checks))
	}
	var version *DoctorCheck
	for _, check := range checks {
		if check.ID == "desktop.app.version" {
			version = check
		}
	}
	if version == nil {
		t.Fatal("missing desktop.app.version check")
	}
	if version.Status != CheckStatusOK || version.Summary != "the desktop application is installed" {
		t.Fatalf("desktop.app.version = %+v", version)
	}
	joined := strings.Join(version.Details, "\n")
	for _, want := range []string{"ChatGPT", "1.2.3", "windows"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("desktop.app.version detail missing %q:\n%s", want, joined)
		}
	}
}

func TestDesktopChecksReportNoInstallationLikeRust(t *testing.T) {
	originalProbe := desktopInstallationProbe
	defer func() { desktopInstallationProbe = originalProbe }()
	desktopInstallationProbe = func() []desktopAppInstallation { return nil }
	checks := desktopChecks()
	if len(checks) != 3 {
		t.Fatalf("desktop checks = %d, want 3", len(checks))
	}
	byID := map[string]*DoctorCheck{}
	for _, check := range checks {
		byID[check.ID] = check
	}
	if got := byID["desktop.app.version"]; got == nil || got.Status != CheckStatusOK || !strings.Contains(got.Summary, "no desktop application installation detected") {
		t.Fatalf("desktop.app.version = %+v", got)
	}
	if got := byID["desktop.security"]; got == nil || !strings.Contains(strings.Join(got.Details, "\n"), "N/A") {
		t.Fatalf("desktop.security = %+v", got)
	}
	if got := byID["desktop.updates"]; got == nil || !strings.Contains(strings.Join(got.Details, "\n"), "N/A") {
		t.Fatalf("desktop.updates = %+v", got)
	}
}

func TestDesktopProductNameMatchesOpenAISurface(t *testing.T) {
	for _, name := range []string{"ChatGPT", "OpenAI", "Codex", "openai-chatgpt"} {
		if !desktopProductName(name) {
			t.Fatalf("desktopProductName(%q) = false", name)
		}
	}
	for _, name := range []string{"Visual Studio Code", "Slack", ""} {
		if desktopProductName(name) {
			t.Fatalf("desktopProductName(%q) = true", name)
		}
	}
}

func TestDesktopChecksRegisteredInBuilder(t *testing.T) {
	report := NewBuilder().Build(&Options{Summary: true})
	found := map[string]bool{}
	for _, check := range report.Checks {
		if strings.HasPrefix(check.ID, "desktop.") {
			found[check.ID] = true
		}
	}
	for _, id := range []string{"desktop.app.version", "desktop.security", "desktop.updates"} {
		if !found[id] {
			t.Fatalf("doctor report missing desktop check %q (checks=%v)", id, report.Checks)
		}
	}
}
