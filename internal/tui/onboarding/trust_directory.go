package onboarding

import "strings"

type TrustDirectorySelection string

const (
	TrustDirectorySelectionTrust TrustDirectorySelection = "trust"
	TrustDirectorySelectionQuit  TrustDirectorySelection = "quit"
)

type TrustDirectoryPrompt struct {
	CWD                          string
	TrustTarget                  string
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
	if strings.TrimSpace(p.TrustTarget) != "" && strings.TrimSpace(p.CWD) != strings.TrimSpace(p.TrustTarget) {
		lines = append(lines, "Note: You're in a subdirectory of a Git project. Trusting will apply to the repository root: "+strings.TrimSpace(p.TrustTarget), "")
	}
	lines = append(lines,
		"Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of prompt injection. Trusting the directory allows project-local config, hooks, and exec policies to load.",
		"",
		selectionLine("Yes, continue", p.Highlighted == TrustDirectorySelectionTrust),
		selectionLine("No, quit", p.Highlighted == TrustDirectorySelectionQuit),
		"",
	)
	if strings.TrimSpace(p.Error) != "" {
		lines = append(lines, strings.TrimSpace(p.Error), "")
	}
	if p.ShowWindowsCreateSandboxHint {
		lines = append(lines, "Press Enter to continue and create a sandbox...")
	} else {
		lines = append(lines, "Press Enter to continue")
	}
	return lines
}

func selectionLine(text string, highlighted bool) string {
	if highlighted {
		return "> " + text
	}
	return "  " + text
}
