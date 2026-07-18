package tui

type AppEventKind string

const (
	AppEventCommand AppEventKind = "command"
	AppEventRedraw  AppEventKind = "redraw"
	AppEventExit    AppEventKind = "exit"
)

type AppEvent struct {
	Kind    AppEventKind
	Command *AppCommand
}
