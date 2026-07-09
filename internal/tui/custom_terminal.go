package tui

type CustomTerminalConfig struct {
	Name string
	Rows int
	Cols int
}

func (c CustomTerminalConfig) Size() (int, int) {
	return c.Cols, c.Rows
}
