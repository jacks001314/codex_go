package telemetry

import (
	"strings"
	"testing"

	"codex_go/protocol"
)

// Mirrors Rust's telemetry_preview_returns_original_within_limits: output that
// already fits must be returned unchanged (including a literal truncation
// marker that came from the tool itself).
func TestTelemetryPreviewReturnsOriginalWithinLimits(t *testing.T) {
	limits := protocol.DefaultToolResultLogConfig()
	for _, content := range []string{"", "first\r\nsecond\r\n", ToolResultTruncationNotice} {
		preview := TelemetryPreview(content, limits)
		if preview.Text != content || preview.Truncated {
			t.Fatalf("TelemetryPreview(%q) = %#v", content, preview)
		}
	}
}

// Mirrors Rust's telemetry_preview_truncates_by_bytes.
func TestTelemetryPreviewTruncatesByBytes(t *testing.T) {
	limits := protocol.DefaultToolResultLogConfig()
	content := strings.Repeat("x", limits.MaxBytes+8)
	preview := TelemetryPreview(content, limits)
	if !preview.Truncated || !strings.Contains(preview.Text, ToolResultTruncationNotice) {
		t.Fatalf("preview = %#v", preview)
	}
	if len(preview.Text) > limits.MaxBytes+len(ToolResultTruncationNotice)+1 {
		t.Fatalf("preview length = %d", len(preview.Text))
	}
	if got := TelemetryPreview(content, protocol.ToolResultLogConfig{MaxBytes: len(content)}); got.Text != content || got.Truncated {
		t.Fatalf("exact-budget preview = %#v", got)
	}
	// A multi-byte rune that does not fit is dropped, and the notice follows on
	// its own line.
	multibyte := "\u00e9\u00e9"
	if got := TelemetryPreview(multibyte, protocol.ToolResultLogConfig{MaxBytes: 3}); got != (ToolResultPreview{
		Text:      "\u00e9\n" + ToolResultTruncationNotice,
		Truncated: true,
	}) {
		t.Fatalf("multibyte preview = %#v", got)
	}
	for _, testCase := range []struct {
		content   string
		maxBytes  int
		text      string
		truncated bool
	}{
		{"", 0, "", false},
		{"\nsecret", 0, ToolResultTruncationNotice, true},
		{"first\r\nsecond", 7, "first\r\n" + ToolResultTruncationNotice, true},
		{"first\r\n", 7, "first\r\n", false},
		{"\n\nsecret", 1, "\n" + ToolResultTruncationNotice, true},
	} {
		got := TelemetryPreview(testCase.content, protocol.ToolResultLogConfig{MaxBytes: testCase.maxBytes})
		if got.Text != testCase.text || got.Truncated != testCase.truncated {
			t.Fatalf("TelemetryPreview(%q, %d) = %#v", testCase.content, testCase.maxBytes, got)
		}
	}
}
