package tui

import (
	"codex_go/install"
	"strconv"
	"strings"
)

// Rust parity: codex-rs/tui/src/update_action.rs.

type UpdateAction string

const (
	UpdateActionNPMGlobalLatest  UpdateAction = "npm-global-latest"
	UpdateActionBunGlobalLatest  UpdateAction = "bun-global-latest"
	UpdateActionPnpmGlobalLatest UpdateAction = "pnpm-global-latest"
	UpdateActionBrewUpgrade      UpdateAction = "brew-upgrade"
	UpdateActionStandaloneUnix   UpdateAction = "standalone-unix"
	UpdateActionStandaloneWin    UpdateAction = "standalone-windows"

	UpdateActionInstall UpdateAction = "install"
	UpdateActionSkip    UpdateAction = "skip"
)

func (a UpdateAction) CommandArgs() (string, []string) {
	switch a {
	case UpdateActionNPMGlobalLatest, UpdateActionInstall:
		return "npm", []string{"install", "-g", install.NPMPackageName + "@latest"}
	case UpdateActionBunGlobalLatest:
		return "bun", []string{"install", "-g", install.NPMPackageName + "@latest"}
	case UpdateActionPnpmGlobalLatest:
		return "pnpm", []string{"add", "-g", install.NPMPackageName + "@latest"}
	case UpdateActionBrewUpgrade:
		return "brew", []string{"upgrade", "--cask", "codex"}
	case UpdateActionStandaloneUnix:
		return "sh", []string{"-c", "curl -fsSL https://chatgpt.com/codex/install.sh | CODEX_NON_INTERACTIVE=1 sh"}
	case UpdateActionStandaloneWin:
		return "powershell", []string{"-ExecutionPolicy", "Bypass", "-c", "$env:CODEX_NON_INTERACTIVE=1; irm https://chatgpt.com/codex/install.ps1 | iex"}
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
