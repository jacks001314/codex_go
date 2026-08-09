package app

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/doctor"
	"codex_go/install"
	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

type interactiveUpdateRequest struct {
	Config         codextui.UpdateCheckConfig
	InstallContext *install.InstallContext
	InitialPrompt  string
	CanPrompt      bool
	NoAltScreen    bool
}

type interactiveUpdateDependencies struct {
	fetchLatest func(context.Context, *install.UpdateCheckOptions) (string, error)
	runPrompt   func(context.Context, *codextui.UpdatePromptScreen, io.Reader, io.Writer, bool) (codextui.UpdateSelection, error)
	runUpdate   func(context.Context, io.Writer, io.Writer) error
	schedule    func(func())
}

func runInteractiveUpdatePromptIfNeeded(ctx context.Context, root *cli.RootOptions, stdin io.Reader, stdout, stderr io.Writer) (bool, error) {
	codexHome := auth.DefaultCodexHome()
	loaded, err := config.LoadEffectiveWithOptions(codexHome, interactiveKeymapLoadOptions(root))
	if err != nil {
		return false, nil
	}
	currentVersion := doctor.Version()
	request := interactiveUpdateRequest{
		Config: codextui.UpdateCheckConfig{
			CheckForUpdateOnStartup: interactiveUpdateCheckEnabled(loaded),
			CurrentVersion:          currentVersion,
			CodexHome:               codexHome,
			Now:                     time.Now().UTC(),
		},
		InstallContext: install.Current(),
		CanPrompt:      shouldRunInteractiveTUI(stdin, stdout) && strings.TrimSpace(os.Getenv("TERM")) != "dumb",
	}
	if root != nil {
		request.InitialPrompt = root.Prompt
		request.NoAltScreen = root.Shared.NoAltScreen
	}
	dependencies := interactiveUpdateDependencies{
		fetchLatest: install.FetchLatestVersion,
		runPrompt:   codextui.RunUpdatePrompt,
		runUpdate: func(ctx context.Context, stdout io.Writer, stderr io.Writer) error {
			return runUpdate(ctx, &cli.UpdateOptions{}, stdout, stderr)
		},
		schedule: func(work func()) {
			go work()
		},
	}
	return runInteractiveUpdateFlow(ctx, request, dependencies, stdin, stdout, stderr)
}

func runInteractiveUpdateFlow(
	ctx context.Context,
	request interactiveUpdateRequest,
	dependencies interactiveUpdateDependencies,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) (bool, error) {
	if !request.Config.CheckForUpdateOnStartup || codextui.IsSourceBuildVersion(request.Config.CurrentVersion) {
		return false, nil
	}

	var cached *codextui.VersionInfo
	if info, err := codextui.ReadVersionInfo(codextui.VersionFilePath(request.Config.CodexHome)); err == nil {
		cached = &info
	}
	if codextui.ShouldCheckForUpgrade(request.Config, cached) && dependencies.fetchLatest != nil && dependencies.schedule != nil {
		dependencies.schedule(func() {
			latest, err := dependencies.fetchLatest(ctx, &install.UpdateCheckOptions{Context: request.InstallContext})
			if err != nil {
				return
			}
			_ = refreshInteractiveVersionCache(request.Config.CodexHome, latest, time.Now().UTC())
		})
	}

	latest, available := codextui.GetUpgradeVersionForPopupFromInfo(cached, request.Config.CurrentVersion)
	if !available || !request.CanPrompt || strings.TrimSpace(request.InitialPrompt) != "" {
		return false, nil
	}
	action := codextui.UpdateActionFromInstall(install.ActionFromContext(request.InstallContext))
	if action == "" || dependencies.runPrompt == nil {
		return false, nil
	}
	screen := codextui.NewUpdatePromptScreen(latest, request.Config.CurrentVersion, action)
	selection, err := dependencies.runPrompt(ctx, screen, stdin, stdout, request.NoAltScreen)
	if err != nil {
		return false, err
	}
	switch selection {
	case codextui.UpdateSelectionUpdateNow:
		if dependencies.runUpdate == nil {
			return false, nil
		}
		if err := dependencies.runUpdate(ctx, stdout, stderr); err != nil {
			return false, err
		}
		return true, nil
	case codextui.UpdateSelectionDontRemind:
		_ = codextui.DismissVersion(request.Config.CodexHome, latest)
	}
	return false, nil
}

func interactiveUpdateCheckEnabled(loaded *config.Config) bool {
	if loaded == nil || loaded.Values == nil {
		return true
	}
	value, ok := loaded.Values["check_for_update_on_startup"].(bool)
	if !ok {
		return true
	}
	return value
}

func interactiveUpdateHistoryCells(root *cli.RootOptions) []historycell.HistoryCell {
	codexHome := auth.DefaultCodexHome()
	loaded, err := config.LoadEffectiveWithOptions(codexHome, interactiveKeymapLoadOptions(root))
	if err != nil || !interactiveUpdateCheckEnabled(loaded) {
		return nil
	}
	currentVersion := doctor.Version()
	latestVersion, available, err := codextui.GetUpgradeVersion(codextui.UpdateCheckConfig{
		CheckForUpdateOnStartup: true,
		CurrentVersion:          currentVersion,
		CodexHome:               codexHome,
	})
	if err != nil || !available {
		return nil
	}
	updateCommand := ""
	if action := install.ActionFromContext(install.Current()); action != nil {
		updateCommand = action.CommandLine()
	}
	return []historycell.HistoryCell{
		historycell.NewUpdateAvailable(currentVersion, latestVersion, updateCommand),
	}
}

func refreshInteractiveVersionCache(codexHome string, latestVersion string, checkedAt time.Time) error {
	versionFile := codextui.VersionFilePath(codexHome)
	previous, _ := codextui.ReadVersionInfo(versionFile)
	return codextui.WriteVersionInfo(versionFile, codextui.VersionInfo{
		LatestVersion:    strings.TrimSpace(latestVersion),
		LastCheckedAt:    checkedAt.UTC(),
		DismissedVersion: previous.DismissedVersion,
	})
}
