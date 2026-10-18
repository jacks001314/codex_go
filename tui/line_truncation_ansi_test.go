package tui

import (
	"strings"
	"testing"

	"codex_go/utils"
)

// TestTruncateWithEllipsisCountsOnlyVisibleANSIColumns pins the status-line
// interaction used by #49037: SGR sequences must not consume terminal columns,
// otherwise a fitting right-aligned Plan mode hint is replaced by `Plan …`.
func TestTruncateWithEllipsisCountsOnlyVisibleANSIColumns(t *testing.T) {
	styled := "\x1b[38;2;233;144;169m\x1b[1mDeepSeek-Flash\x1b[m"
	hint := "Plan mode (shift+tab to cycle)"
	line := styled + strings.Repeat(" ", 80-DisplayWidth("DeepSeek-Flash")-DisplayWidth(hint)) + hint

	got := TruncateWithEllipsis(line, 80)
	if !strings.HasSuffix(got, hint) {
		t.Fatalf("truncated styled status = %q, want visible suffix %q", got, hint)
	}
	if width := DisplayWidth(utils.StripANSI(got)); width != 80 {
		t.Fatalf("visible status width = %d, want 80", width)
	}
}
