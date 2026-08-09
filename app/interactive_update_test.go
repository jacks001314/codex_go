package app

import (
	"context"
	"io"
	"testing"
	"time"

	"codex_go/install"
	codextui "codex_go/tui"
)

func TestInteractiveUpdateFlowUsesCachedVersionAndRefreshesInBackground(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	if err := codextui.WriteVersionInfo(codextui.VersionFilePath(home), codextui.VersionInfo{
		LatestVersion: "2.0.0",
		LastCheckedAt: now.Add(-21 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	promptedVersion := ""
	fetched := false
	request := testInteractiveUpdateRequest(home, now)
	exited, err := runInteractiveUpdateFlow(context.Background(), request, interactiveUpdateDependencies{
		fetchLatest: func(context.Context, *install.UpdateCheckOptions) (string, error) {
			fetched = true
			return "3.0.0", nil
		},
		runPrompt: func(_ context.Context, screen *codextui.UpdatePromptScreen, _ io.Reader, _ io.Writer, _ bool) (codextui.UpdateSelection, error) {
			promptedVersion = screen.LatestVersion
			return codextui.UpdateSelectionNotNow, nil
		},
		schedule: func(work func()) { work() },
	}, nil, io.Discard, io.Discard)
	if err != nil || exited {
		t.Fatalf("flow exited=%v err=%v", exited, err)
	}
	if !fetched || promptedVersion != "2.0.0" {
		t.Fatalf("fetched=%v promptedVersion=%q", fetched, promptedVersion)
	}
	info, err := codextui.ReadVersionInfo(codextui.VersionFilePath(home))
	if err != nil {
		t.Fatal(err)
	}
	if info.LatestVersion != "3.0.0" {
		t.Fatalf("refreshed cache = %#v", info)
	}
}

func TestInteractiveUpdateFlowRunsSelectedUpdate(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	writeFreshUpdateCache(t, home, now)
	updateRan := false
	exited, err := runInteractiveUpdateFlow(context.Background(), testInteractiveUpdateRequest(home, now), interactiveUpdateDependencies{
		runPrompt: func(context.Context, *codextui.UpdatePromptScreen, io.Reader, io.Writer, bool) (codextui.UpdateSelection, error) {
			return codextui.UpdateSelectionUpdateNow, nil
		},
		runUpdate: func(context.Context, io.Writer, io.Writer) error {
			updateRan = true
			return nil
		},
	}, nil, io.Discard, io.Discard)
	if err != nil || !exited || !updateRan {
		t.Fatalf("exited=%v updateRan=%v err=%v", exited, updateRan, err)
	}
}

func TestInteractiveUpdateFlowDismissesCurrentVersion(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	writeFreshUpdateCache(t, home, now)
	exited, err := runInteractiveUpdateFlow(context.Background(), testInteractiveUpdateRequest(home, now), interactiveUpdateDependencies{
		runPrompt: func(context.Context, *codextui.UpdatePromptScreen, io.Reader, io.Writer, bool) (codextui.UpdateSelection, error) {
			return codextui.UpdateSelectionDontRemind, nil
		},
	}, nil, io.Discard, io.Discard)
	if err != nil || exited {
		t.Fatalf("exited=%v err=%v", exited, err)
	}
	info, err := codextui.ReadVersionInfo(codextui.VersionFilePath(home))
	if err != nil {
		t.Fatal(err)
	}
	if info.DismissedVersion == nil || *info.DismissedVersion != "2.0.0" {
		t.Fatalf("dismissed cache = %#v", info)
	}
}

func TestInteractiveUpdateFlowSkipsSourceBuildAndInitialPrompt(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	promptCalls := 0
	dependencies := interactiveUpdateDependencies{
		runPrompt: func(context.Context, *codextui.UpdatePromptScreen, io.Reader, io.Writer, bool) (codextui.UpdateSelection, error) {
			promptCalls++
			return codextui.UpdateSelectionNotNow, nil
		},
	}
	request := testInteractiveUpdateRequest(home, now)
	request.Config.CurrentVersion = "0.0.0"
	if exited, err := runInteractiveUpdateFlow(context.Background(), request, dependencies, nil, io.Discard, io.Discard); err != nil || exited {
		t.Fatalf("source flow exited=%v err=%v", exited, err)
	}

	request = testInteractiveUpdateRequest(home, now)
	request.InitialPrompt = "fix tests"
	writeFreshUpdateCache(t, home, now)
	if exited, err := runInteractiveUpdateFlow(context.Background(), request, dependencies, nil, io.Discard, io.Discard); err != nil || exited {
		t.Fatalf("prompt flow exited=%v err=%v", exited, err)
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d", promptCalls)
	}
}

func testInteractiveUpdateRequest(home string, now time.Time) interactiveUpdateRequest {
	return interactiveUpdateRequest{
		Config: codextui.UpdateCheckConfig{
			CheckForUpdateOnStartup: true,
			CurrentVersion:          "1.0.0",
			CodexHome:               home,
			Now:                     now,
		},
		InstallContext: &install.InstallContext{Method: install.InstallMethod{Kind: install.InstallNPM}},
		CanPrompt:      true,
	}
}

func writeFreshUpdateCache(t *testing.T, home string, now time.Time) {
	t.Helper()
	if err := codextui.WriteVersionInfo(codextui.VersionFilePath(home), codextui.VersionInfo{
		LatestVersion: "2.0.0",
		LastCheckedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}
