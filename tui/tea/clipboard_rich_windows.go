//go:build windows

package tea

import (
	"context"
	"os/exec"
	"strings"
)

// defaultRichClipboardWriter returns a Windows native rich-text clipboard
// writer. It sends the HTML fragment through the .NET Clipboard with
// TextDataFormat.Html (which adds the CF_HTML wrapper) and falls back to the
// plain Markdown writer when PowerShell or the clipboard is unavailable.
func defaultRichClipboardWriter(plain func(text string) error) func(html string, text string) error {
	if plain == nil {
		return nil
	}
	return func(html string, text string) error {
		script := "Add-Type -AssemblyName System.Windows.Forms; $clipHtml = [Console]::In.ReadToEnd(); [System.Windows.Forms.Clipboard]::SetText($clipHtml, [System.Windows.Forms.TextDataFormat]::Html)"
		cmd := exec.CommandContext(context.Background(), "powershell", "-NoProfile", "-STA", "-Command", script)
		cmd.Stdin = strings.NewReader(html)
		if err := cmd.Run(); err == nil {
			return nil
		}
		return plain(text)
	}
}
