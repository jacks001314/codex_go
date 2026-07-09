package tui

type ExternalEditorRequest struct {
	Editor string
	Text   string
}

func (r ExternalEditorRequest) HasEditor() bool {
	return r.Editor != ""
}
