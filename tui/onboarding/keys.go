package onboarding

type KeyBindingHint struct {
	Key   string
	Label string
}

var (
	MoveUp          = []KeyBindingHint{{Key: "Up", Label: "move up"}, {Key: "k", Label: "move up"}}
	MoveDown        = []KeyBindingHint{{Key: "Down", Label: "move down"}, {Key: "j", Label: "move down"}}
	SelectFirst     = []KeyBindingHint{{Key: "1", Label: "select first"}, {Key: "y", Label: "yes"}}
	SelectSecond    = []KeyBindingHint{{Key: "2", Label: "select second"}, {Key: "n", Label: "no"}}
	SelectThird     = []KeyBindingHint{{Key: "3", Label: "select third"}}
	Confirm         = []KeyBindingHint{{Key: "Enter", Label: "confirm"}}
	Cancel          = []KeyBindingHint{{Key: "Esc", Label: "cancel"}}
	Quit            = []KeyBindingHint{{Key: "q", Label: "quit"}, {Key: "Ctrl-C", Label: "quit"}, {Key: "Ctrl-D", Label: "quit"}}
	ToggleAnimation = []KeyBindingHint{{Key: "Ctrl-.", Label: "change animation"}, {Key: "Ctrl-Shift-.", Label: "change animation"}}
)
