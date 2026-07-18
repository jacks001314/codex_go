package tui

import (
	"encoding/base64"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestColorAndStyleParity(t *testing.T) {
	if !IsLight(RGB{R: 255, G: 255, B: 255}) || IsLight(RGB{}) {
		t.Fatal("IsLight did not classify white/black as expected")
	}
	if got := BlendRGB(RGB{R: 255, G: 255, B: 255}, RGB{}, 0.20); got != (RGB{R: 51, G: 51, B: 51}) {
		t.Fatalf("BlendRGB = %#v, want 51 gray", got)
	}
	if got := PerceptualDistance(RGB{R: 10, G: 20, B: 30}, RGB{R: 10, G: 20, B: 30}); got != 0 {
		t.Fatalf("PerceptualDistance identical = %f, want 0", got)
	}

	lightBG := RGB{R: 255, G: 255, B: 255}
	accent := AccentStyleFor(&lightBG, ColorTrue)
	if accent.Foreground.RGB == nil || *accent.Foreground.RGB != lightBGAccentRGB || !accent.Bold {
		t.Fatalf("light accent = %#v", accent)
	}
	separator := TableSeparatorStyleFor(&RGB{R: 255, G: 255, B: 255}, &RGB{}, ColorTrue)
	if separator.Foreground.RGB == nil || *separator.Foreground.RGB != (RGB{R: 51, G: 51, B: 51}) {
		t.Fatalf("separator = %#v", separator)
	}
	dim := TableSeparatorStyleFor(nil, &RGB{}, ColorTrue)
	if !dim.Dim {
		t.Fatalf("missing foreground should dim, got %#v", dim)
	}
	if bg := UserMessageBackground(RGB{}, ColorTrue); bg.RGB == nil || *bg.RGB != (RGB{R: 30, G: 30, B: 30}) {
		t.Fatalf("user bg = %#v", bg)
	}
}

func TestResizeReflowMaxRowsParity(t *testing.T) {
	cases := map[TerminalName]int{
		TerminalVSCode:          VSCodeResizeReflowMaxRows,
		TerminalWindowsTerminal: WindowsTerminalResizeReflowMaxRows,
		TerminalWezTerm:         WezTermResizeReflowMaxRows,
		TerminalAlacritty:       AlacrittyResizeReflowMaxRows,
		TerminalGhostty:         DefaultTerminalResizeReflowFallbackMaxRows,
		TerminalUnknown:         DefaultTerminalResizeReflowFallbackMaxRows,
	}
	for terminal, want := range cases {
		if got := AutoResizeReflowMaxRows(terminal, false); got != want {
			t.Fatalf("AutoResizeReflowMaxRows(%s) = %d, want %d", terminal, got, want)
		}
	}
	if got := AutoResizeReflowMaxRows(TerminalWindowsTerminal, true); got != VSCodeResizeReflowMaxRows {
		t.Fatalf("VS Code probe override = %d, want %d", got, VSCodeResizeReflowMaxRows)
	}
	if got, ok := ResizeReflowMaxRowsFor(ResizeReflowConfig{Mode: ResizeReflowLimit, Limit: 42}, TerminalVSCode, false); !ok || got != 42 {
		t.Fatalf("limit override = %d ok=%v", got, ok)
	}
	if _, ok := ResizeReflowMaxRowsFor(ResizeReflowConfig{Mode: ResizeReflowDisabled}, TerminalVSCode, false); ok {
		t.Fatal("disabled resize reflow returned ok=true")
	}
}

func TestMotionAndShimmerParity(t *testing.T) {
	if got := MotionModeFromAnimationsEnabled(true); got != MotionAnimated {
		t.Fatalf("animations enabled = %v", got)
	}
	if _, ok := ActivityIndicator(nil, MotionReduced, ReducedMotionHidden, false, time.Unix(0, 0)); ok {
		t.Fatal("reduced hidden indicator ok=true")
	}
	if got, ok := ActivityIndicator(nil, MotionReduced, ReducedMotionStaticBullet, false, time.Unix(0, 0)); !ok || got != "\u2022" {
		t.Fatalf("reduced bullet = %q ok=%v", got, ok)
	}
	start := time.Unix(0, 0)
	if got := AnimatedActivityIndicator(&start, false, start.Add(700*time.Millisecond)); got != "\u25e6" {
		t.Fatalf("animated fallback after 700ms = %q", got)
	}
	if got := ShimmerText("Loading", MotionReduced); len(got) != 1 || got[0].Text != "Loading" {
		t.Fatalf("reduced shimmer = %#v", got)
	}
	if spans := ShimmerSpansAt("abc", time.Second); len(spans) != 3 || spans[0].Text != "a" {
		t.Fatalf("shimmer spans = %#v", spans)
	}
}

func TestTableDetectionParity(t *testing.T) {
	if got, ok := ParseTableSegments("| A \\| B | C |"); !ok || !reflect.DeepEqual(got, []string{`A \| B`, "C"}) {
		t.Fatalf("ParseTableSegments escaped = %#v ok=%v", got, ok)
	}
	if _, ok := ParseTableSegments("just text"); ok {
		t.Fatal("plain line parsed as table")
	}
	if !IsTableHeaderLine("Name | Value") || IsTableHeaderLine("| | |") {
		t.Fatal("header detection mismatch")
	}
	for _, line := range []string{"| --- | --- |", "|:---:|---:|", "--- | --- | ---"} {
		if !IsTableDelimiterLine(line) {
			t.Fatalf("delimiter line rejected: %q", line)
		}
	}
	for _, line := range []string{"| A | B |", "| -- | -- |"} {
		if IsTableDelimiterLine(line) {
			t.Fatalf("delimiter line accepted unexpectedly: %q", line)
		}
	}

	tracker := NewFenceTracker()
	if tracker.Kind() != FenceOutside {
		t.Fatalf("initial fence kind = %v", tracker.Kind())
	}
	tracker.Advance("```Markdown")
	if tracker.Kind() != FenceMarkdown {
		t.Fatalf("markdown fence kind = %v", tracker.Kind())
	}
	tracker.Advance("| A | B |")
	if tracker.Kind() != FenceMarkdown {
		t.Fatalf("inside markdown fence kind = %v", tracker.Kind())
	}
	tracker.Advance("```")
	if tracker.Kind() != FenceOutside {
		t.Fatalf("closed fence kind = %v", tracker.Kind())
	}
	tracker.Advance("    ```sh")
	if tracker.Kind() != FenceOutside {
		t.Fatalf("indented fence kind = %v", tracker.Kind())
	}
	tracker.Advance("> ```sh")
	if tracker.Kind() != FenceOther {
		t.Fatalf("blockquote fence kind = %v", tracker.Kind())
	}
	tracker.Advance("> ```")
	if tracker.Kind() != FenceOutside {
		t.Fatalf("blockquote fence closed kind = %v", tracker.Kind())
	}
	if marker, length, ok := ParseFenceMarker("````rust"); !ok || marker != '`' || length != 4 {
		t.Fatalf("ParseFenceMarker = %q %d %v", marker, length, ok)
	}
	if got := StripBlockquotePrefix("> > nested"); got != "nested" {
		t.Fatalf("StripBlockquotePrefix = %q", got)
	}
}

func TestLiveWrapParity(t *testing.T) {
	builder := NewRowBuilder(10)
	builder.PushFragment("hello whirl this is a test")
	if got := builder.Rows(); !reflect.DeepEqual(got, []Row{
		{Text: "hello whir"},
		{Text: "l this is "},
	}) {
		t.Fatalf("Rows = %#v", got)
	}

	builder = NewRowBuilder(6)
	builder.PushFragment("🙂🙂 你好")
	if got := builder.Rows(); !reflect.DeepEqual(got, []Row{{Text: "🙂🙂 "}}) {
		t.Fatalf("wide rows = %#v", got)
	}

	all := NewRowBuilder(7)
	all.PushFragment("ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	chunks := NewRowBuilder(7)
	for i := 0; i < len("ABCDEFGHIJKLMNOPQRSTUVWXYZ"); i += 3 {
		end := i + 3
		if end > len("ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			end = len("ABCDEFGHIJKLMNOPQRSTUVWXYZ")
		}
		chunks.PushFragment("ABCDEFGHIJKLMNOPQRSTUVWXYZ"[i:end])
	}
	if !reflect.DeepEqual(all.Rows(), chunks.Rows()) {
		t.Fatalf("fragmentation mismatch all=%#v chunks=%#v", all.Rows(), chunks.Rows())
	}

	builder = NewRowBuilder(10)
	builder.PushFragment("hello\nworld")
	rows := builder.DisplayRows()
	if len(rows) < 2 || !rows[0].ExplicitBreak || rows[0].Text != "hello" || !strings.HasPrefix(rows[1].Text, "world") {
		t.Fatalf("newline rows = %#v", rows)
	}
	builder = NewRowBuilder(10)
	builder.PushFragment("abcdefghijK")
	builder.SetWidth(5)
	for _, row := range builder.Rows() {
		if row.Width() > 5 {
			t.Fatalf("rewrapped row too wide: %#v", row)
		}
	}
}

func TestMarkdownTextMergeParity(t *testing.T) {
	events := []MarkdownTextEvent{
		{Kind: MarkdownEventText, Text: "hel", Range: SourceRange{Start: 0, End: 3}},
		{Kind: MarkdownEventText, Text: "lo", Range: SourceRange{Start: 3, End: 5}},
		{Kind: MarkdownEventOther, Text: "*", Range: SourceRange{Start: 5, End: 6}},
		{Kind: MarkdownEventText, Text: "cod", Range: SourceRange{Start: 6, End: 9}},
		{Kind: MarkdownEventText, Text: "ex", Range: SourceRange{Start: 9, End: 11}},
	}
	got := MergeAdjacentTextEvents(events)
	want := []MarkdownTextEvent{
		{Kind: MarkdownEventText, Text: "hello", Range: SourceRange{Start: 0, End: 5}},
		{Kind: MarkdownEventOther, Text: "*", Range: SourceRange{Start: 5, End: 6}},
		{Kind: MarkdownEventText, Text: "codex", Range: SourceRange{Start: 6, End: 11}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MergeAdjacentTextEvents = %#v", got)
	}
}

func TestClipboardParity(t *testing.T) {
	sequence, err := OSC52Sequence("hello", false)
	if err != nil {
		t.Fatalf("OSC52Sequence error = %v", err)
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(sequence, "\x1b]52;c;"), "\x07")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != "hello" {
		t.Fatalf("OSC52 decoded = %q err=%v", string(decoded), err)
	}
	if tmux, err := OSC52Sequence("hello", true); err != nil || tmux != "\x1bPtmux;\x1b\x1b]52;c;aGVsbG8=\x07\x1b\\" {
		t.Fatalf("tmux OSC52 = %q err=%v", tmux, err)
	}
	if _, err := OSC52Sequence(strings.Repeat("x", OSC52MaxRawBytes+1), false); err == nil {
		t.Fatal("oversized OSC52 did not fail")
	}

	if got := ClipboardCopyOrder(ClipboardEnvironment{SSHSession: true, TmuxSession: true}); !reflect.DeepEqual(got, []ClipboardBackend{ClipboardTmux, ClipboardOSC52}) {
		t.Fatalf("remote tmux order = %#v", got)
	}
	if got := ClipboardCopyOrder(ClipboardEnvironment{WSLSession: true}); !reflect.DeepEqual(got, []ClipboardBackend{ClipboardNative, ClipboardWSL, ClipboardOSC52}) {
		t.Fatalf("local WSL order = %#v", got)
	}

	if got, ok := NormalizePastedSearchQuery("  alpha\n\tbeta\r\n gamma  "); !ok || got != "alpha beta gamma" {
		t.Fatalf("NormalizePastedSearchQuery = %q ok=%v", got, ok)
	}
	if got, ok := NormalizePastedPath(`"/home/user/My File.png"`); !ok || got != filepath.Clean("/home/user/My File.png") {
		t.Fatalf("quoted path = %q ok=%v", got, ok)
	}
	if got, ok := NormalizePastedPath(`/home/user/My\ File.png`); !ok || got != filepath.Clean("/home/user/My File.png") {
		t.Fatalf("escaped path = %q ok=%v", got, ok)
	}
	if _, ok := NormalizePastedPath(`/home/a.png /home/b.png`); ok {
		t.Fatal("multi-token path ok=true")
	}
	if runtime.GOOS == "windows" {
		got, ok := NormalizePastedPath("file:///C:/Temp/example.png")
		if !ok || got != filepath.Clean(`C:\Temp\example.png`) {
			t.Fatalf("file URL path = %q ok=%v", got, ok)
		}
	}
	for path, want := range map[string]EncodedImageFormat{
		"/a/b/c.PNG":  EncodedImagePNG,
		"/a/b/c.jpg":  EncodedImageJPEG,
		"/a/b/c.JPEG": EncodedImageJPEG,
		"/a/b/c.webp": EncodedImageOther,
	} {
		if got := PastedImageFormat(path); got != want {
			t.Fatalf("PastedImageFormat(%q) = %v, want %v", path, got, want)
		}
	}
}
