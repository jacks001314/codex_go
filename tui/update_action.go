package tui

import (
	"codex_go/install"
	"strconv"
	"strings"
)

// Rust parity: codex-rs/tui/src/update_action.rs.

type UpdateAction string

const (
	UpdateActionNPMGlobalLatest      UpdateAction = "npm-global-latest"
	UpdateActionBunGlobalLatest      UpdateAction = "bun-global-latest"
	UpdateActionVitePlusGlobalLatest UpdateAction = "vite-plus-global-latest"
	UpdateActionPnpmGlobalLatest     UpdateAction = "pnpm-global-latest"
	UpdateActionBrewUpgrade          UpdateAction = "brew-upgrade"
	UpdateActionStandaloneUnix       UpdateAction = "standalone-unix"
	UpdateActionStandaloneWin        UpdateAction = "standalone-windows"
	// The daemon update actions replace the local background server after the
	// TUI exits (Rust UpdateAction::Daemon(DaemonUpdateSource)).
	UpdateActionDaemonPublicStable UpdateAction = "daemon-public-stable"
	UpdateActionDaemonThisCli      UpdateAction = "daemon-this-cli"

	UpdateActionInstall UpdateAction = "install"
	UpdateActionSkip    UpdateAction = "skip"
)

// DaemonUpdateSource is the package source a user selected in the daemon menu
// (Rust tui/src/update_action.rs::DaemonUpdateSource).
type DaemonUpdateSource string

const (
	// DaemonUpdateSourcePublicStable installs the latest public stable release.
	DaemonUpdateSourcePublicStable DaemonUpdateSource = "public-stable"
	// DaemonUpdateSourceThisCli copies and pins this CLI's own package.
	DaemonUpdateSourceThisCli DaemonUpdateSource = "this-cli"
)

// CommandArgs returns the CLI arguments that perform the daemon update
// (Rust DaemonUpdateSource::command_args).
func (s DaemonUpdateSource) CommandArgs() []string {
	if s == DaemonUpdateSourceThisCli {
		return []string{"app-server", "daemon", "update", "--from-cli", "--yes"}
	}
	return []string{"app-server", "daemon", "update"}
}

// DaemonUpdateAction maps a selected source onto its update action; it returns
// "" for an unknown source.
func DaemonUpdateAction(source DaemonUpdateSource) UpdateAction {
	switch source {
	case DaemonUpdateSourcePublicStable:
		return UpdateActionDaemonPublicStable
	case DaemonUpdateSourceThisCli:
		return UpdateActionDaemonThisCli
	default:
		return ""
	}
}

// DaemonUpdateSourceOf returns the source an action was built from.
func (a UpdateAction) DaemonUpdateSourceOf() (DaemonUpdateSource, bool) {
	switch a {
	case UpdateActionDaemonPublicStable:
		return DaemonUpdateSourcePublicStable, true
	case UpdateActionDaemonThisCli:
		return DaemonUpdateSourceThisCli, true
	default:
		return "", false
	}
}

func (a UpdateAction) CommandArgs() (string, []string) {
	switch a {
	case UpdateActionNPMGlobalLatest, UpdateActionInstall:
		return "npm", []string{"install", "-g", install.NPMPackageName + "@latest"}
	case UpdateActionBunGlobalLatest:
		return "bun", []string{"install", "-g", install.NPMPackageName + "@latest"}
	case UpdateActionVitePlusGlobalLatest:
		return "vp", []string{"install", "-g", install.NPMPackageName + "@latest"}
	case UpdateActionPnpmGlobalLatest:
		return "pnpm", []string{"add", "-g", install.NPMPackageName + "@latest"}
	case UpdateActionBrewUpgrade:
		return "brew", []string{"upgrade", "--cask", "codex"}
	case UpdateActionStandaloneUnix:
		return "sh", []string{"-c", "curl -fsSL https://chatgpt.com/codex/install.sh | CODEX_NON_INTERACTIVE=1 sh"}
	case UpdateActionStandaloneWin:
		return "powershell", []string{"-ExecutionPolicy", "Bypass", "-c", "$env:CODEX_NON_INTERACTIVE=1; irm https://chatgpt.com/codex/install.ps1 | iex"}
	case UpdateActionDaemonPublicStable:
		return "codex", DaemonUpdateSourcePublicStable.CommandArgs()
	case UpdateActionDaemonThisCli:
		return "codex", DaemonUpdateSourceThisCli.CommandArgs()
	default:
		return "", nil
	}
}

func UpdateActionFromInstall(action *install.UpdateAction) UpdateAction {
	if action == nil {
		return ""
	}
	return UpdateAction(action.Kind)
}

func (a UpdateAction) CommandString() string {
	command, args := a.CommandArgs()
	if command == "" {
		return ""
	}
	return joinUpdateCommandLine(append([]string{command}, args...))
}

func joinUpdateCommandLine(parts []string) string {
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			quoted = append(quoted, `""`)
			continue
		}
		if strings.ContainsAny(part, " \t\r\n\"'|&;$()<>") {
			quoted = append(quoted, strconv.Quote(part))
			continue
		}
		quoted = append(quoted, part)
	}
	return strings.Join(quoted, " ")
}
