package bottompane

import (
	"reflect"
	"strings"
	"testing"

	"codex_go/internal/appserver"
	"codex_go/internal/tui"
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
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow("[x] Hook 1")) {
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
