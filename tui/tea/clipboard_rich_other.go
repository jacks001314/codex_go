//go:build !windows

package tea

// defaultRichClipboardWriter is a no-op on platforms without a native
// multi-format clipboard writer; rich text is only produced when the host
// supplies OnClipboardWriteRich.
func defaultRichClipboardWriter(plain func(text string) error) func(html string, text string) error {
	return nil
}
