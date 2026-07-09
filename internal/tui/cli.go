package tui

type TUICLIOptions struct {
	Prompt string
	Resume bool
	Remote bool
}

func (o TUICLIOptions) HasInitialPrompt() bool {
	return o.Prompt != ""
}
