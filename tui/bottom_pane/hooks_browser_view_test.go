package bottompane

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/appserver"
	"codex_go/tui"
)

func TestHooksBrowserEventCountsAndDefaultSelectionMatchRust(t *testing.T) {
	untrusted := hookBrowserTestHook("path:untrusted", appserver.HookEventPermissionRequest, false, false, 1)
	untrusted.TrustStatus = appserver.HookTrustUntrusted
	view := NewHooksBrowserView(appserver.HookListEntry{CWD: "/repo", Hooks: []appserver.HookMetadata{
		hookBrowserTestHook("path:trusted", appserver.HookEventPreToolUse, true, false, 0),
		hookBrowserTestHook("path:managed", appserver.HookEventPreToolUse, true, true, 2),
		untrusted,
	}})

	rows := view.EventRows()
	pre := findHookEventRow(rows, appserver.HookEventPreToolUse)
	if pre.Installed != 2 || pre.Active != 2 || pre.NeedsReview != 0 {
		t.Fatalf("pre tool row = %#v", pre)
	}
	perm := findHookEventRow(rows, appserver.HookEventPermissionRequest)
	if perm.Installed != 1 || perm.Active != 0 || perm.NeedsReview != 1 {
		t.Fatalf("permission row = %#v", perm)
	}
	selected, ok := view.SelectedEvent()
	if !ok || selected != appserver.HookEventPermissionRequest {
		t.Fatalf("selected event = %q ok=%v", selected, ok)
	}
}

func TestHooksBrowserRowsOpenReturnAndSelectionColorBar(t *testing.T) {
	view := NewHooksBrowserView(appserver.HookListEntry{Hooks: []appserver.HookMetadata{
		hookBrowserTestHook("path:trusted", appserver.HookEventPreToolUse, true, false, 0),
	}})
	rows := view.Rows(120)
	want := tui.RenderSelectedRow(formatHookEventRow(findHookEventRow(view.EventRows(), appserver.HookEventPreToolUse), false))
	if !bottomPaneContainsRow(rows, want) {
		t.Fatalf("event rows missing selected row:\n%s", strings.Join(rows, "\n"))
	}
	view.HandleKey("enter")
	if view.Page != HooksBrowserPageHandlers || view.HandlerEvent != appserver.HookEventPreToolUse {
		t.Fatalf("page=%s event=%s", view.Page, view.HandlerEvent)
	}
	rows = view.Rows(80)
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow("[x] \x1b[2m 1\x1b[0m  Unnamed hook")) {
		t.Fatalf("handler rows missing selected hook:\n%s", strings.Join(rows, "\n"))
	}
	view.HandleKey("esc")
	selected, ok := view.SelectedEvent()
	if view.Page != HooksBrowserPageEvents || !ok || selected != appserver.HookEventPreToolUse {
		t.Fatalf("returned page=%s selected=%s ok=%v", view.Page, selected, ok)
	}
}

func TestHooksBrowserToggleTrustAndTrustAllMatchRust(t *testing.T) {
	untrusted := hookBrowserTestHook("path:untrusted", appserver.HookEventPreToolUse, false, false, 0)
	untrusted.TrustStatus = appserver.HookTrustUntrusted
	modified := hookBrowserTestHook("path:modified", appserver.HookEventStop, false, false, 1)
	modified.TrustStatus = appserver.HookTrustModified
	view := NewHooksBrowserView(appserver.HookListEntry{Hooks: []appserver.HookMetadata{
		untrusted,
		modified,
		hookBrowserTestHook("path:trusted", appserver.HookEventPreToolUse, true, false, 2),
	}})

	view.HandleKey("t")
	if len(view.Events) != 1 || view.Events[0].Kind != HooksBrowserEventTrustHooks {
		t.Fatalf("trust all events = %#v", view.Events)
	}
	wantUpdates := []tui.HookTrustUpdate{
		{Key: "path:untrusted", CurrentHash: "sha256:path:untrusted"},
		{Key: "path:modified", CurrentHash: "sha256:path:modified"},
	}
	if !reflect.DeepEqual(view.Events[0].Updates, wantUpdates) {
		t.Fatalf("updates = %#v, want %#v", view.Events[0].Updates, wantUpdates)
	}

	view = NewHooksBrowserView(appserver.HookListEntry{Hooks: []appserver.HookMetadata{
		hookBrowserTestHook("path:trusted", appserver.HookEventPreToolUse, true, false, 0),
	}})
	view.HandleKey("enter")
	view.HandleKey("space")
	if len(view.Events) != 1 || view.Events[0].Kind != HooksBrowserEventSetEnabled || view.Events[0].Enabled {
		t.Fatalf("toggle event = %#v", view.Events)
	}
}

func TestHooksBrowserManagedAndReviewNeededHandlersDoNotToggle(t *testing.T) {
	managed := hookBrowserTestHook("path:managed", appserver.HookEventPreToolUse, true, true, 0)
	untrusted := hookBrowserTestHook("path:untrusted", appserver.HookEventPreToolUse, true, false, 1)
	untrusted.TrustStatus = appserver.HookTrustUntrusted
	view := NewHooksBrowserView(appserver.HookListEntry{Hooks: []appserver.HookMetadata{managed, untrusted}})
	view.HandleKey("enter")
	view.HandleKey("space")
	if len(view.Events) != 0 {
		t.Fatalf("managed hook should not toggle: %#v", view.Events)
	}
	view.HandleKey("down")
	view.HandleKey("space")
	if len(view.Events) != 0 {
		t.Fatalf("review needed hook should not toggle: %#v", view.Events)
	}
	view.HandleKey("t")
	if len(view.Events) != 1 || view.Events[0].Kind != HooksBrowserEventTrustHook || view.Events[0].Key != "path:untrusted" {
		t.Fatalf("trust selected event = %#v", view.Events)
	}
}

func TestHooksBrowserHelpersMatchRustLabels(t *testing.T) {
	if !HookIsActive(hookBrowserTestHook("path:trusted", appserver.HookEventPreToolUse, true, false, 0)) {
		t.Fatalf("trusted enabled hook should be active")
	}
	untrusted := hookBrowserTestHook("path:untrusted", appserver.HookEventPreToolUse, true, false, 0)
	untrusted.TrustStatus = appserver.HookTrustUntrusted
	if HookIsActive(untrusted) || !HookNeedsReviewMetadata(untrusted) {
		t.Fatalf("untrusted enabled hook active=%v needsReview=%v", HookIsActive(untrusted), HookNeedsReviewMetadata(untrusted))
	}
	if label := HookTrustLabel(appserver.HookTrustModified); label != "Modified since last trusted - review required" {
		t.Fatalf("trust label = %q", label)
	}
	if message, ok := ReviewNeededMessage(2); !ok || message != "2 hooks need review before they can run." {
		t.Fatalf("review message = %q ok=%v", message, ok)
	}
}

func TestHooksBrowserDetailRowsPreserveRustFormatting(t *testing.T) {
	matcher := ""
	command := ""
	pluginID := ""
	pluginHook := hookBrowserTestHook("plugin:empty", appserver.HookEventPreToolUse, true, false, 0)
	pluginHook.Matcher = &matcher
	pluginHook.Command = &command
	pluginHook.Source = appserver.HookSourcePlugin
	pluginHook.PluginID = &pluginID
	view := NewHooksBrowserView(appserver.HookListEntry{Hooks: []appserver.HookMetadata{pluginHook}})
	view.HandleKey("enter")

	rows := view.Rows(80)
	for _, want := range []string{
		"Event     PreToolUse",
		"Matcher   ",
		"Source    Plugin - ",
		"Command   ",
		"Timeout   5s",
		"Trust     Trusted",
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("rows missing %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}

	userHook := hookBrowserTestHook("path:empty-source", appserver.HookEventPreToolUse, true, false, 0)
	userHook.SourcePath = ""
	if got := HookSourceDetail(userHook); got != "User config - " {
		t.Fatalf("user source detail = %q", got)
	}
	if got := truncateHookRow("abcdef", 4); got != "abc…" {
		t.Fatalf("truncated row = %q", got)
	}
}

// TestHookHandlerRowAlignsDimmedIndexLikeRust pins the row shape from Rust
// #51433: the status marker, the right-aligned (dimmed) row number sized to the
// largest index, the title, and the trust suffix.
func TestHookHandlerRowAlignsDimmedIndexLikeRust(t *testing.T) {
	hook := hookBrowserTestHook("path:x", appserver.HookEventPreToolUse, true, false, 0)
	row := HookHandlerRow(hook, 0, 3)
	if row != "[x]   1  Unnamed hook" {
		t.Fatalf("HookHandlerRow = %q", row)
	}
	if dimmed := dimHookHandlerIndex(row, 3); dimmed != "[x] \x1b[2m  1\x1b[0m  Unnamed hook" {
		t.Fatalf("dimHookHandlerIndex = %q", dimmed)
	}
	// A short row (already narrower than the index span) is left untouched.
	if got := dimHookHandlerIndex("[x]", 3); got != "[x]" {
		t.Fatalf("dimHookHandlerIndex(short) = %q", got)
	}
}

// TestHooksBrowserHandlerTitlesFromStatusMessageLikeRust mirrors Rust #51433:
// handler titles come from the trimmed status message, blank and missing
// messages fall back to "Unnamed hook", and the trust suffix is retained.
func TestHooksBrowserHandlerTitlesFromStatusMessageLikeRust(t *testing.T) {
	wide := hookBrowserTestHook("path:one", appserver.HookEventPreToolUse, true, false, 0)
	wideMessage := "  检查 🦀 shell commands  "
	wide.StatusMessage = &wideMessage
	blank := hookBrowserTestHook("path:two", appserver.HookEventPreToolUse, true, false, 1)
	blankMessage := " \t\n "
	blank.StatusMessage = &blankMessage
	modified := hookBrowserTestHook("path:three", appserver.HookEventPreToolUse, true, false, 2)
	modified.TrustStatus = appserver.HookTrustModified
	modifiedMessage := "Review shell"
	modified.StatusMessage = &modifiedMessage

	view := NewHooksBrowserView(appserver.HookListEntry{Hooks: []appserver.HookMetadata{wide, blank, modified}})
	view.OpenSelectedEvent()
	rows := view.Rows(120)
	joined := strings.Join(rows, "\n")
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow("[x] \x1b[2m 1\x1b[0m  检查 🦀 shell commands")) {
		t.Fatalf("missing trimmed wide-character title:\n%s", joined)
	}
	if !bottomPaneContainsRow(rows, "[x] \x1b[2m 2\x1b[0m  Unnamed hook") {
		t.Fatalf("blank status message did not fall back:\n%s", joined)
	}
	if !strings.Contains(joined, "Review shell") || !strings.Contains(joined, "modified") {
		t.Fatalf("trust suffix lost:\n%s", joined)
	}
}

func hookBrowserTestHook(key string, event appserver.HookEventName, enabled bool, managed bool, order int64) appserver.HookMetadata {
	command := "/tmp/" + strings.ReplaceAll(key, ":", "-") + ".sh"
	sourcePath := "/tmp/hooks.json"
	return appserver.HookMetadata{
		Key:          key,
		EventName:    event,
		HandlerType:  appserver.HookHandlerCommand,
		Command:      &command,
		TimeoutSec:   5,
		SourcePath:   sourcePath,
		Source:       appserver.HookSourceUser,
		DisplayOrder: order,
		Enabled:      enabled,
		IsManaged:    managed,
		CurrentHash:  "sha256:" + key,
		TrustStatus:  appserver.HookTrustTrusted,
	}
}

func findHookEventRow(rows []HookEventRow, event appserver.HookEventName) HookEventRow {
	for _, row := range rows {
		if row.EventName == event {
			return row
		}
	}
	return HookEventRow{}
}

// TestHooksBrowserDetailLinksWrappedURLsLikeRust mirrors Rust #51473: a web URL
// in a wrapped hook command detail stays a complete terminal hyperlink, and a
// truncated detail never points at a truncated destination.
func TestHooksBrowserDetailLinksWrappedURLsLikeRust(t *testing.T) {
	url := "https://example.com/hooks/very/long/command?token=abc123"
	lines := wrapHookDetail("Command", "curl "+url, 30, 0)
	if len(lines) < 2 {
		t.Fatalf("expected the command to wrap: %#v", lines)
	}
	linked := 0
	visible := strings.Builder{}
	for _, line := range lines {
		if strings.Contains(line, tui.OSC8Hyperlink(url, url)) {
			linked++
		}
		visible.WriteString(tui.StripOSC8(line))
		visible.WriteString("\n")
	}
	if linked != 1 {
		t.Fatalf("expected one complete hyperlink, got %d: %#v", linked, lines)
	}
	if !strings.Contains(visible.String(), url) {
		t.Fatalf("visible text lost the URL:\n%s", visible.String())
	}

	// A truncated detail must not link a truncated destination, and the ellipsis
	// stays outside any link.
	truncated := wrapHookDetail("Command", url+" "+strings.Repeat("x", 40), 30, 1)
	if len(truncated) != 1 || !strings.HasSuffix(truncated[0], "…") {
		t.Fatalf("truncated detail = %#v", truncated)
	}
	if strings.Contains(truncated[0], "\x1b]8;;") {
		t.Fatalf("truncated detail should not be hyperlinked: %q", truncated[0])
	}
}
