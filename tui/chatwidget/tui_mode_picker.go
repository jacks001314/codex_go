package chatwidget

const (
	// TUIModePickerViewID identifies the `/tui` next-launch picker.
	TUIModePickerViewID = "tui-mode-picker"
	// TUIModeScrollback / TUIModeFullscreen are the stored renderer choices.
	TUIModeScrollback = "scrollback"
	TUIModeFullscreen = "fullscreen"
)

// NewTUIModePickerView builds the `/tui` selection view (Rust #46883). The
// selected item requires explicit confirmation and the current marker follows
// the stored `tui.fullscreen_transcript` preference, not this session's
// effective terminal mode.
func NewTUIModePickerView(fullscreen bool) SelectionView {
	return SelectionView{
		ViewID:      TUIModePickerViewID,
		Title:       "TUI mode for next launch",
		FooterNote:  "Restart to apply. Launch overrides still apply.",
		AllowCancel: true,
		Items: []SelectionItem{
			{
				ID:                          TUIModeScrollback,
				Name:                        "Scrollback",
				Description:                 "Use your terminal's scrollback",
				IsCurrent:                   !fullscreen,
				RequireExplicitConfirmation: true,
				DismissOnSelect:             true,
			},
			{
				ID:                          TUIModeFullscreen,
				Name:                        "Fullscreen",
				Description:                 "Scroll within Codex's fullscreen view",
				IsCurrent:                   fullscreen,
				RequireExplicitConfirmation: true,
				DismissOnSelect:             true,
			},
		},
	}
}
