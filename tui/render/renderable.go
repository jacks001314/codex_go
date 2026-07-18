package render

type Renderable interface {
	Render(width int) []string
}

type Lines []string

func (l Lines) Render(width int) []string {
	return append([]string(nil), l...)
}
