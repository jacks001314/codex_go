package onboarding

import "strings"

type TrustDirectorySelection string

const (
	TrustDirectorySelectionTrust TrustDirectorySelection = "trust"
	TrustDirectorySelectionQuit  TrustDirectorySelection = "quit"
)

// TrustCancelAction selects what escaping the prompt does. Connected (remote)
// sessions return to the Agent Command Center; embedded sessions quit
// (Rust #44755 TrustCancelAction).
type TrustCancelAction string

const (
	TrustCancelQuit           TrustCancelAction = "quit"
	TrustCancelAgentsOverview TrustCancelAction = "agents_overview"
)

type TrustDirectoryPrompt struct {
	CWD         string
	TrustTarget string
	// Restricted marks a folder that stays untrusted: project-local config,
	// hooks, and exec policies remain disabled and no trust decision is saved
	// when the user opens it (Rust #44732).
	Restricted bool
	// ExistingTask marks a resumed task whose folder is untrusted. Such a task
	// may retain settings/history loaded while trusted, so the disclosure and
	// action label differ (Rust #44746).
	ExistingTask bool
	// Cancel selects the escape behavior (Rust #44755).
	Cancel                       TrustCancelAction
	Trusted                      bool
	ShowWindowsCreateSandboxHint bool
	ShouldQuit                   bool
	Selection                    TrustDirectorySelection
	Highlighted                  TrustDirectorySelection
	Error                        string
}

func NewTrustDirectoryPrompt(cwd string, trustTarget string) TrustDirectoryPrompt {
	cwd = strings.TrimSpace(cwd)
	trustTarget = strings.TrimSpace(trustTarget)
	if trustTarget == "" {
		trustTarget = cwd
	}
	return TrustDirectoryPrompt{
		CWD:         cwd,
		TrustTarget: trustTarget,
		Highlighted: TrustDirectorySelectionTrust,
	}
}

func (p *TrustDirectoryPrompt) MoveUp() {
	p.Highlighted = TrustDirectorySelectionTrust
}

func (p *TrustDirectoryPrompt) MoveDown() {
	p.Highlighted = TrustDirectorySelectionQuit
}

func (p *TrustDirectoryPrompt) Confirm() {
	switch p.Highlighted {
	case TrustDirectorySelectionQuit:
		p.Quit()
	default:
		p.Trust()
	}
}

func (p *TrustDirectoryPrompt) Trust() {
	p.Highlighted = TrustDirectorySelectionTrust
	p.Error = ""
	p.Selection = TrustDirectorySelectionTrust
	p.Trusted = true
	p.ShouldQuit = false
}

func (p *TrustDirectoryPrompt) Quit() {
	p.Highlighted = TrustDirectorySelectionQuit
	p.Selection = TrustDirectorySelectionQuit
	p.ShouldQuit = true
}

func (p TrustDirectoryPrompt) StepState() StepState {
	if p.Selection != "" || p.ShouldQuit {
		return StepComplete
	}
	return StepInProgress
}

func (p TrustDirectoryPrompt) RenderLines() []string {
	lines := []string{"> You are in " + strings.TrimSpace(p.CWD), ""}
	if !p.Restricted && strings.TrimSpace(p.TrustTarget) != "" && strings.TrimSpace(p.CWD) != strings.TrimSpace(p.TrustTarget) {
		lines = append(lines, "Note: You're in a subdirectory of a Git project. Trusting will apply to the repository root: "+strings.TrimSpace(p.TrustTarget), "")
	}
	disclosure := "Trust this folder? Codex can read, edit, and run files here, subject to your permission settings. Folder settings can run code automatically, even without a model request. Continue only if you trust these files. Your trust decision will be saved."
	firstOption := "Trust and continue"
	switch {
	case p.Restricted && p.ExistingTask:
		disclosure = "This existing task may retain settings and history, including project configuration or hooks loaded while it was trusted. To use restricted settings, start a new task. The folder's trust setting will not change."
		firstOption = "Open existing task"
	case p.Restricted:
		disclosure = "Config, hooks, and exec policies from untrusted folders stay disabled. Trusted project folders can still contribute settings. Skills still load, and tools follow your permission settings. Opening will not change saved trust."
		firstOption = "Open restricted"
	}
	cancelLabel := "Quit"
	cancelHint := "Press Enter to continue; esc to quit"
	if p.Cancel == TrustCancelAgentsOverview {
		cancelLabel = "Back to Agent Command Center"
		cancelHint = "Press Enter to continue; esc to go back"
	}
	lines = append(lines,
		disclosure,
		"",
		selectionLine(firstOption, p.Highlighted == TrustDirectorySelectionTrust),
		selectionLine(cancelLabel, p.Highlighted == TrustDirectorySelectionQuit),
		"",
	)
	if strings.TrimSpace(p.Error) != "" {
		lines = append(lines, strings.TrimSpace(p.Error), "")
	}
	if p.ShowWindowsCreateSandboxHint && !p.Restricted {
		lines = append(lines, "Press Enter to continue and create a sandbox...")
	} else {
		lines = append(lines, cancelHint)
	}
	return lines
}

func selectionLine(text string, highlighted bool) string {
	if highlighted {
		return "> " + text
	}
	return "  " + text
}
