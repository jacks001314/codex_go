package tui

type ExecCommandDisplay struct {
	Command string
	CWD     string
}

func (d ExecCommandDisplay) Label() string {
	if d.CWD == "" {
		return d.Command
	}
	return d.Command + " (" + d.CWD + ")"
}
