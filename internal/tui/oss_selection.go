package tui

import "strings"

// Rust parity: codex-rs/tui/src/oss_selection.rs.

const (
	DefaultLMStudioPort = 1234
	DefaultOllamaPort   = 11434

	LMStudioOSSProviderID = "lmstudio"
	OllamaOSSProviderID   = "ollama"
	OSSCancelledProvider  = "__CANCELLED__"
)

type OSSSelection struct {
	Provider string
	Model    string
}

type OSSProviderStatus string

const (
	OSSProviderRunning    OSSProviderStatus = "running"
	OSSProviderNotRunning OSSProviderStatus = "not_running"
	OSSProviderUnknown    OSSProviderStatus = "unknown"
)

type OSSSelectOption struct {
	Label       string
	Description string
	Key         string
	ProviderID  string
}

type OSSProviderSelection struct {
	Provider         string
	ManuallySelected bool
}

type OSSSelectionWidget struct {
	Options       []OSSSelectOption
	SelectedIndex int
	Done          bool
	Selection     string
	LMStudio      OSSProviderStatus
	Ollama        OSSProviderStatus
}

func DefaultOSSSelectOptions() []OSSSelectOption {
	return []OSSSelectOption{
		{
			Label:       "LM Studio",
			Description: "Local LM Studio server (default port 1234)",
			Key:         "l",
			ProviderID:  LMStudioOSSProviderID,
		},
		{
			Label:       "Ollama",
			Description: "Local Ollama server (Responses API, default port 11434)",
			Key:         "o",
			ProviderID:  OllamaOSSProviderID,
		},
	}
}

func NewOSSSelectionWidget(lmstudioStatus OSSProviderStatus, ollamaStatus OSSProviderStatus) *OSSSelectionWidget {
	return &OSSSelectionWidget{
		Options:  DefaultOSSSelectOptions(),
		LMStudio: normalizeOSSProviderStatus(lmstudioStatus),
		Ollama:   normalizeOSSProviderStatus(ollamaStatus),
	}
}

func AutoSelectOSSProvider(lmstudioStatus OSSProviderStatus, ollamaStatus OSSProviderStatus) (OSSProviderSelection, bool) {
	lmstudioStatus = normalizeOSSProviderStatus(lmstudioStatus)
	ollamaStatus = normalizeOSSProviderStatus(ollamaStatus)
	switch {
	case lmstudioStatus == OSSProviderRunning && ollamaStatus == OSSProviderNotRunning:
		return OSSProviderSelection{Provider: LMStudioOSSProviderID, ManuallySelected: false}, true
	case lmstudioStatus == OSSProviderNotRunning && ollamaStatus == OSSProviderRunning:
		return OSSProviderSelection{Provider: OllamaOSSProviderID, ManuallySelected: false}, true
	default:
		return OSSProviderSelection{}, false
	}
}

func (w *OSSSelectionWidget) HandleKey(key string) (string, bool) {
	if w == nil || w.Done {
		if w == nil || w.Selection == "" {
			return "", false
		}
		return w.Selection, true
	}
	switch normalizeOSSKey(key) {
	case "ctrl-c":
		w.sendDecision(OSSCancelledProvider)
	case "left", "ctrl-h":
		w.Move(-1)
	case "right", "ctrl-l":
		w.Move(1)
	case "enter":
		if len(w.Options) > 0 {
			w.sendDecision(w.Options[w.SelectedIndex].ProviderID)
		}
	case "esc":
		w.sendDecision(LMStudioOSSProviderID)
	default:
		normalized := normalizeOSSKey(key)
		for _, option := range w.Options {
			if normalizeOSSKey(option.Key) == normalized {
				w.sendDecision(option.ProviderID)
				break
			}
		}
	}
	return w.Selection, w.Done
}

func (w *OSSSelectionWidget) Move(delta int) {
	if w == nil || len(w.Options) == 0 || delta == 0 {
		return
	}
	w.SelectedIndex = (w.SelectedIndex + delta) % len(w.Options)
	if w.SelectedIndex < 0 {
		w.SelectedIndex += len(w.Options)
	}
}

func (w *OSSSelectionWidget) IsComplete() bool {
	return w != nil && w.Done
}

func (w *OSSSelectionWidget) DesiredHeight() int {
	if w == nil {
		return 0
	}
	return 9 + len(w.Options)
}

func (w *OSSSelectionWidget) Rows(width int) []string {
	if w == nil {
		return nil
	}
	rows := []string{
		"? Select an open-source provider",
		"",
		"  Choose which local AI server to use for your session.",
		"",
		"  " + OSSProviderStatusSymbol(w.LMStudio) + " LM Studio ",
		"  " + OSSProviderStatusSymbol(w.Ollama) + " Ollama (Responses) ",
		"  " + OSSProviderStatusSymbol(w.Ollama) + " Ollama (Chat) ",
		"",
		"  Running / Not Running",
		"",
		"Select provider?",
	}
	for i, option := range w.Options {
		selected := i == w.SelectedIndex
		prefix := SelectionPrefix(selected)
		line := prefix + option.Label + " - " + option.Description
		if width > 0 {
			line = TruncateWithEllipsis(line, width)
		}
		if selected {
			line = RenderSelectedRow(line)
		}
		rows = append(rows, line)
	}
	return rows
}

func OSSProviderStatusSymbol(status OSSProviderStatus) string {
	switch normalizeOSSProviderStatus(status) {
	case OSSProviderRunning:
		return "●"
	case OSSProviderNotRunning:
		return "○"
	default:
		return "?"
	}
}

func (w *OSSSelectionWidget) sendDecision(selection string) {
	w.Selection = selection
	w.Done = true
}

func normalizeOSSProviderStatus(status OSSProviderStatus) OSSProviderStatus {
	switch status {
	case OSSProviderRunning, OSSProviderNotRunning, OSSProviderUnknown:
		return status
	default:
		return OSSProviderUnknown
	}
}

func normalizeOSSKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}
