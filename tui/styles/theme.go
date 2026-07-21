package styles

// DefaultDark returns the default dark theme Styles.
// All color values match the previously hardcoded values exactly,
// ensuring backward compatibility with snapshot tests.
func DefaultDark() Styles {
	return Styles{
		IsDark: true,
		Chat: ChatStyles{
			UserMessageBG:      ANSIEscape(ColorUserMessageBG),
			UserMessagePrefix:  ANSIEscape(ColorUserMessagePrefix),
			UserMessagePostfix: ANSIEscape(ColorUserMessagePostfix),
			UserMessageReset:   ANSIEscape(ColorReset),
			DimText:            ANSIEscape(ColorDim),
			BrightText:         "", // uses default terminal foreground
		},
		Editor: EditorStyles{
			BorderColor: ANSIEscape(ColorBorder),
		},
		Status: StatusStyles{
			HeaderBold:  true,
			FooterColor: ANSIEscape(ColorDim),
			BottomColor: "",
		},
		Dialog: DialogStyles{
			DimText:     ANSIEscape(ColorDim),
			SelectedRow: ANSIEscape(ColorSelected),
			Highlight:   ANSIEscape(ColorAccent),
			BrightText:  ANSIEscape(ColorBright),
		},
		ExecCell: ExecCellStyles{
			Reset:   ANSIEscape(ColorReset),
			Bold:    "\x1b[1m",
			Dim:     ANSIEscape(ColorDim),
			Error:   ANSIEscape(ColorError),
			Success: ANSIEscape(ColorSuccess),
		},
	}
}

// DefaultLight returns the default light theme Styles.
// Currently returns the same palette as dark; light theme support
// will be added later when full theme switching is implemented.
func DefaultLight() Styles {
	s := DefaultDark()
	s.IsDark = false
	return s
}
