package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateVersionsMatchRust(t *testing.T) {
	version, err := ExtractVersionFromLatestTag("rust-v1.5.0")
	if err != nil {
		t.Fatalf("ExtractVersionFromLatestTag() error = %v", err)
	}
	if version != "1.5.0" {
		t.Fatalf("version = %q, want 1.5.0", version)
	}
	if _, err := ExtractVersionFromLatestTag("v1.5.0"); err == nil {
		t.Fatal("latest tag without prefix returned nil error")
	}
	assertBoolPtrTUI(t, IsNewerVersion("0.11.1", "0.11.0"), true)
	assertBoolPtrTUI(t, IsNewerVersion("0.11.0", "0.11.1"), false)
	assertBoolPtrTUI(t, IsNewerVersion("1.0.0", "0.9.9"), true)
	assertBoolPtrTUI(t, IsNewerVersion("0.9.9", "1.0.0"), false)
	if got := IsNewerVersion("1.0.0-rc.1", "1.0.0"); got != nil {
		t.Fatalf("pre-release comparison = %v, want nil", *got)
	}
	if got := IsNewerVersion(" 1.2.3 ", "1.2.2"); got == nil || !*got {
		t.Fatalf("whitespace comparison = %v, want true", got)
	}
	if !IsSourceBuildVersion("0.0.0") || IsSourceBuildVersion("0.0.1") {
		t.Fatal("IsSourceBuildVersion mismatch")
	}
}

func TestUpdateAvailableUsesRustSemverRules(t *testing.T) {
	if !UpdateAvailable(UpdateVersions{Current: "1.2.3", Latest: "1.2.4"}) {
		t.Fatal("expected newer plain semver to be available")
	}
	if UpdateAvailable(UpdateVersions{Current: "1.2.3", Latest: "1.2.3"}) {
		t.Fatal("same version should not be available")
	}
	if UpdateAvailable(UpdateVersions{Current: "1.2.3", Latest: "1.2.4-beta.1"}) {
		t.Fatal("pre-release latest should be unknown, not available")
	}
	if UpdateAvailable(UpdateVersions{Current: "0.0.0", Latest: "9.9.9"}) {
		t.Fatal("source build current version should not check updates")
	}
}

func TestUpdateActionCommandStringsMatchRust(t *testing.T) {
	command, args := UpdateActionStandaloneUnix.CommandArgs()
	if command != "sh" || len(args) != 2 || args[1] != "curl -fsSL https://chatgpt.com/codex/install.sh | CODEX_NON_INTERACTIVE=1 sh" {
		t.Fatalf("StandaloneUnix CommandArgs = %q %#v", command, args)
	}
	command, args = UpdateActionStandaloneWin.CommandArgs()
	if command != "powershell" || len(args) != 4 || args[3] != "$env:CODEX_NON_INTERACTIVE=1; irm https://chatgpt.com/codex/install.ps1 | iex" {
		t.Fatalf("StandaloneWin CommandArgs = %q %#v", command, args)
	}
	if got := UpdateActionNPMGlobalLatest.CommandString(); got != "npm install -g @openai/codex" {
		t.Fatalf("CommandString = %q", got)
	}
}

func TestUpdatesCacheDismissVersionMatchesRust(t *testing.T) {
	home := t.TempDir()
	if got := VersionFilePath(home); got != filepath.Join(home, "version.json") {
		t.Fatalf("VersionFilePath = %q", got)
	}
	if err := DismissVersion(home, "999.0.0"); err != nil {
		t.Fatalf("DismissVersion() error = %v", err)
	}
	info, err := ReadVersionInfo(VersionFilePath(home))
	if err != nil {
		t.Fatalf("ReadVersionInfo() error = %v", err)
	}
	if !info.LastCheckedAt.Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("LastCheckedAt = %s, want Unix epoch", info.LastCheckedAt)
	}
	if info.LatestVersion != "999.0.0" || info.DismissedVersion == nil || *info.DismissedVersion != "999.0.0" {
		t.Fatalf("info = %#v", info)
	}
	raw, err := os.ReadFile(VersionFilePath(home))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("cache file should end with newline: %q", string(raw))
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("cache JSON invalid: %v", err)
	}
	if decoded["latest_version"] != "999.0.0" || decoded["dismissed_version"] != "999.0.0" {
		t.Fatalf("cache JSON = %#v", decoded)
	}
}

func TestUpdatesCacheDismissVersionPreservesExistingInfo(t *testing.T) {
	home := t.TempDir()
	checkedAt := time.Date(2026, 7, 8, 1, 2, 3, 0, time.UTC)
	if err := WriteVersionInfo(VersionFilePath(home), VersionInfo{
		LatestVersion: "2.0.0",
		LastCheckedAt: checkedAt,
	}); err != nil {
		t.Fatalf("WriteVersionInfo() error = %v", err)
	}
	if err := DismissVersion(home, "2.0.0"); err != nil {
		t.Fatalf("DismissVersion() error = %v", err)
	}
	info, err := ReadVersionInfo(VersionFilePath(home))
	if err != nil {
		t.Fatalf("ReadVersionInfo() error = %v", err)
	}
	if info.LatestVersion != "2.0.0" || !info.LastCheckedAt.Equal(checkedAt) || info.DismissedVersion == nil || *info.DismissedVersion != "2.0.0" {
		t.Fatalf("info = %#v", info)
	}
}

func TestUpdatesPopupEligibilityMatchesRust(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	info := &VersionInfo{
		LatestVersion: "2.0.0",
		LastCheckedAt: now.Add(-19 * time.Hour),
	}
	if VersionInfoNeedsRefresh(info, now) {
		t.Fatal("cache checked 19 hours ago should not refresh")
	}
	info.LastCheckedAt = now.Add(-21 * time.Hour)
	if !VersionInfoNeedsRefresh(info, now) {
		t.Fatal("cache checked 21 hours ago should refresh")
	}
	if !ShouldCheckForUpgrade(UpdateCheckConfig{CheckForUpdateOnStartup: true, CurrentVersion: "1.0.0", Now: now}, info) {
		t.Fatal("expected stale normal build to check")
	}
	if ShouldCheckForUpgrade(UpdateCheckConfig{CheckForUpdateOnStartup: true, CurrentVersion: "0.0.0", Now: now}, info) {
		t.Fatal("source build should not check")
	}
	latest, ok := GetUpgradeVersionForPopupFromInfo(info, "1.0.0")
	if !ok || latest != "2.0.0" {
		t.Fatalf("popup latest = %q ok=%v", latest, ok)
	}
	info.DismissedVersion = stringPtr("2.0.0")
	if latest, ok := GetUpgradeVersionForPopupFromInfo(info, "1.0.0"); ok || latest != "" {
		t.Fatalf("dismissed popup latest = %q ok=%v, want none", latest, ok)
	}
}

func TestUpdatePromptScreenMatchesRustKeys(t *testing.T) {
	screen := NewUpdatePromptScreen("9.9.9", "1.0.0", UpdateActionNPMGlobalLatest)
	if screen.Highlighted != UpdateSelectionUpdateNow || screen.IsDone() {
		t.Fatalf("initial screen = %#v", screen)
	}
	screen.HandleKey("up")
	if screen.Highlighted != UpdateSelectionDontRemind {
		t.Fatalf("highlight after up = %s", screen.Highlighted)
	}
	screen.HandleKey("down")
	if screen.Highlighted != UpdateSelectionUpdateNow {
		t.Fatalf("highlight after down = %s", screen.Highlighted)
	}
	screen.HandleKey("down")
	screen.HandleKey("enter")
	if !screen.IsDone() || screen.Selection == nil || *screen.Selection != UpdateSelectionNotNow {
		t.Fatalf("selection = %#v", screen.Selection)
	}
	if outcome, action := screen.Outcome(); outcome != UpdatePromptOutcomeContinue || action != "" {
		t.Fatalf("outcome = %s action=%s", outcome, action)
	}

	screen = NewUpdatePromptScreen("9.9.9", "1.0.0", UpdateActionBrewUpgrade)
	screen.HandleKey("3")
	if screen.Selection == nil || *screen.Selection != UpdateSelectionDontRemind {
		t.Fatalf("number selection = %#v", screen.Selection)
	}
	screen = NewUpdatePromptScreen("9.9.9", "1.0.0", UpdateActionBrewUpgrade)
	screen.HandleKey("ctrl-c")
	if screen.Selection == nil || *screen.Selection != UpdateSelectionNotNow {
		t.Fatalf("ctrl-c selection = %#v", screen.Selection)
	}
	screen = NewUpdatePromptScreen("9.9.9", "1.0.0", UpdateActionBrewUpgrade)
	screen.HandleKey("1")
	if outcome, action := screen.Outcome(); outcome != UpdatePromptOutcomeRunUpdate || action != UpdateActionBrewUpgrade {
		t.Fatalf("outcome = %s action=%s", outcome, action)
	}
}

func TestUpdatePromptRowsUseSelectedColorBar(t *testing.T) {
	screen := NewUpdatePromptScreen("9.9.9", "1.0.0", UpdateActionNPMGlobalLatest)
	rows := strings.Join(screen.Rows(120), "\n")
	for _, want := range []string{
		"Update available! 1.0.0 -> 9.9.9",
		"Release notes: " + ReleaseNotesURL,
		NumberedSelectionPrefix(0, true) + "Update now (runs `npm install -g @openai/codex`)",
		"\x1b[",
	} {
		if !strings.Contains(rows, want) {
			t.Fatalf("rows missing %q:\n%s", want, rows)
		}
	}
	screen.HandleKey("down")
	rows = strings.Join(screen.Rows(120), "\n")
	if !strings.Contains(rows, NumberedSelectionPrefix(1, true)+"Skip") {
		t.Fatalf("selected row did not move:\n%s", rows)
	}
}

func assertBoolPtrTUI(t *testing.T, got *bool, want bool) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("bool pointer = %v, want %v", got, want)
	}
}
