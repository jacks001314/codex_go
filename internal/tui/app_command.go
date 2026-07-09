package tui

type AppCommandKind string

const (
	AppCommandSubmitUserMessage AppCommandKind = "submit_user_message"
	AppCommandInterrupt         AppCommandKind = "interrupt"
	AppCommandUpdateModel       AppCommandKind = "update_model"
	AppCommandQuit              AppCommandKind = "quit"
)

type AppCommand struct {
	Kind AppCommandKind
	Text string
}
