package bottompane

import (
	"strings"

	"codex_go/internal/tui"
)

// Rust parity: codex-rs/tui/src/bottom_pane/unified_exec_footer.rs.

type UnifiedExecFooterState struct {
	Processes []string
}

type UnifiedExecFooter struct {
	processes []string
}

func NewUnifiedExecFooter() *UnifiedExecFooter {
	return &UnifiedExecFooter{}
}

func (f *UnifiedExecFooter) SetProcesses(processes []string) bool {
	if f == nil {
		return false
	}
	copied := append([]string(nil), processes...)
	if sameStringSlice(f.processes, copied) {
		return false
	}
	f.processes = copied
	return true
}

func (f *UnifiedExecFooter) Processes() []string {
	if f == nil {
		return nil
	}
	return append([]string(nil), f.processes...)
}

func (f *UnifiedExecFooter) IsEmpty() bool {
	return f == nil || len(f.processes) == 0
}

func (f *UnifiedExecFooter) SummaryText() (string, bool) {
	if f == nil || len(f.processes) == 0 {
		return "", false
	}
	count := len(f.processes)
	plural := "s"
	if count == 1 {
		plural = ""
	}
	return strings.Join([]string{
		formatInt(count) + " background terminal" + plural + " running",
		"/ps to view",
		"/stop to close",
	}, " · "), true
}

func (f *UnifiedExecFooter) RenderLines(width int) []string {
	if f == nil || width < 4 {
		return nil
	}
	summary, ok := f.SummaryText()
	if !ok {
		return nil
	}
	return []string{truncateFooterRunes("  "+summary, width)}
}

func (f *UnifiedExecFooter) DesiredHeight(width int) int {
	return len(f.RenderLines(width))
}

func (s UnifiedExecFooterState) SummaryText() (string, bool) {
	footer := NewUnifiedExecFooter()
	footer.SetProcesses(s.Processes)
	return footer.SummaryText()
}

func formatInt(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

func sameStringSlice(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for idx := range a {
		if a[idx] != b[idx] {
			return false
		}
	}
	return true
}

func truncateFooterRunes(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if tui.DisplayWidth(value) <= width {
		return value
	}
	return tui.TruncateToWidth(value, width)
}
