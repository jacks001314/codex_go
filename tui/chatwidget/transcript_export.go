package chatwidget

// Transcript export picker mirrors Rust's /export popup (chatwidget
// transcript_export.rs): copy the complete Markdown transcript or save it to a
// file.
const (
	TranscriptExportPickerViewID    = "transcript-export"
	TranscriptExportOptionClipboard = "clipboard"
	TranscriptExportOptionFile      = "file"
)

// NewTranscriptExportView builds the /export destination picker.
func NewTranscriptExportView() SelectionView {
	return SelectionView{
		ViewID:      TranscriptExportPickerViewID,
		Title:       "Export conversation",
		Subtitle:    "Save the complete conversation as Markdown",
		FooterHint:  "Press enter to confirm or esc to go back",
		AllowCancel: true,
		Items: []SelectionItem{
			{
				ID:              TranscriptExportOptionClipboard,
				Name:            "Copy to clipboard",
				Description:     "Copy the complete Markdown transcript",
				DismissOnSelect: true,
			},
			{
				ID:              TranscriptExportOptionFile,
				Name:            "Save to file",
				Description:     "Choose a Markdown filename",
				DismissOnSelect: true,
			},
		},
	}
}
