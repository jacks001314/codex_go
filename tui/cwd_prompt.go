package tui

type CWDPromptKind string

const (
	CWDPromptTrust CWDPromptKind = "trust"
	CWDPromptFork  CWDPromptKind = "fork"
)

type CWDPrompt struct {
	Kind CWDPromptKind
	CWD  string
}
