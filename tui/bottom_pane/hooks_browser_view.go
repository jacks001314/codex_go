package bottompane

import (
	"sort"
	"strings"

	"codex_go/appserver"
	"codex_go/tui"
	tuistatus "codex_go/tui/status"
)

// Rust parity: codex-rs/tui/src/bottom_pane/hooks_browser_view.rs.

const maxHookCommandDetailLines = 3

type HooksBrowserRow struct {
	Name   string
	Status string
}

type HooksBrowserPage string

const (
	HooksBrowserPageEvents   HooksBrowserPage = "events"
	HooksBrowserPageHandlers HooksBrowserPage = "handlers"
)

type HooksBrowserEventKind string

const (
	HooksBrowserEventSetEnabled HooksBrowserEventKind = "set_hook_enabled"
	HooksBrowserEventTrustHook  HooksBrowserEventKind = "trust_hook"
	HooksBrowserEventTrustHooks HooksBrowserEventKind = "trust_hooks"
)

type HooksBrowserEvent struct {
	Kind        HooksBrowserEventKind
	Key         string
	Enabled     bool
	CurrentHash string
	Updates     []tui.HookTrustUpdate
}

type HookEventRow struct {
	EventName   appserver.HookEventName
	Installed   int
	Active      int
	NeedsReview int
}

type HooksBrowserView struct {
	Entry        appserver.HookListEntry
	Page         HooksBrowserPage
	HandlerEvent appserver.HookEventName
	State        ScrollState
	Complete     bool
	Events       []HooksBrowserEvent
}

func NewHooksBrowserView(entry appserver.HookListEntry) *HooksBrowserView {
	hooks := append([]appserver.HookMetadata(nil), entry.Hooks...)
	sort.SliceStable(hooks, func(i, j int) bool {
		return hooks[i].DisplayOrder < hooks[j].DisplayOrder
	})
	entry.Hooks = hooks
	view := &HooksBrowserView{
		Entry: entry,
		Page:  HooksBrowserPageEvents,
		State: NewScrollState(),
	}
	rows := view.EventRows()
	selected := 0
	for idx, row := range rows {
		if row.NeedsReview > 0 {
			selected = idx
			break
		}
	}
	if len(rows) > 0 {
		view.State.SelectedIdx = selected
		view.State.HasSelection = true
	}
	return view
}

func (v *HooksBrowserView) EventRows() []HookEventRow {
	if v == nil {
		return nil
	}
	rows := make([]HookEventRow, 0, len(AllHookEventNames()))
	for _, event := range AllHookEventNames() {
		row := HookEventRow{EventName: event}
		for _, hook := range v.Entry.Hooks {
			if hook.EventName != event {
				continue
			}
			row.Installed++
			if HookIsActive(hook) {
				row.Active++
			}
			if HookNeedsReviewMetadata(hook) {
				row.NeedsReview++
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func (v *HooksBrowserView) HandlersForEvent(event appserver.HookEventName) []appserver.HookMetadata {
	if v == nil {
		return nil
	}
	out := []appserver.HookMetadata{}
	for _, hook := range v.Entry.Hooks {
		if hook.EventName == event {
			out = append(out, hook)
		}
	}
	return out
}

func (v *HooksBrowserView) SelectedEvent() (appserver.HookEventName, bool) {
	if v == nil || v.Page != HooksBrowserPageEvents || !v.State.HasSelection {
		return "", false
	}
	events := AllHookEventNames()
	if v.State.SelectedIdx < 0 || v.State.SelectedIdx >= len(events) {
		return "", false
	}
	return events[v.State.SelectedIdx], true
}

func (v *HooksBrowserView) SelectedHookIndex(event appserver.HookEventName) (int, bool) {
	if v == nil || !v.State.HasSelection {
		return 0, false
	}
	visibleIdx := 0
	for idx, hook := range v.Entry.Hooks {
		if hook.EventName != event {
			continue
		}
		if visibleIdx == v.State.SelectedIdx {
			return idx, true
		}
		visibleIdx++
	}
	return 0, false
}

func (v *HooksBrowserView) SelectedHook(event appserver.HookEventName) (appserver.HookMetadata, bool) {
	idx, ok := v.SelectedHookIndex(event)
	if !ok {
		return appserver.HookMetadata{}, false
	}
	return v.Entry.Hooks[idx], true
}

func (v *HooksBrowserView) PageLen() int {
	if v == nil {
		return 0
	}
	switch v.Page {
	case HooksBrowserPageHandlers:
		return len(v.HandlersForEvent(v.HandlerEvent))
	default:
		return len(AllHookEventNames())
	}
}

func (v *HooksBrowserView) maxVisibleRows() int {
	return min(MaxPopupRows, max(v.PageLen(), 1))
}

func (v *HooksBrowserView) MoveUp() {
	if v == nil {
		return
	}
	length := v.PageLen()
	v.State.MoveUpWrap(length)
	v.State.EnsureVisible(length, v.maxVisibleRows())
}

func (v *HooksBrowserView) MoveDown() {
	if v == nil {
		return
	}
	length := v.PageLen()
	v.State.MoveDownWrap(length)
	v.State.EnsureVisible(length, v.maxVisibleRows())
}

func (v *HooksBrowserView) PageUp() {
	if v == nil {
		return
	}
	v.State.PageUpClamped(v.PageLen(), v.maxVisibleRows())
}

func (v *HooksBrowserView) PageDown() {
	if v == nil {
		return
	}
	v.State.PageDownClamped(v.PageLen(), v.maxVisibleRows())
}

func (v *HooksBrowserView) JumpTop() {
	if v == nil {
		return
	}
	v.State.JumpTop(v.PageLen(), v.maxVisibleRows())
}

func (v *HooksBrowserView) JumpBottom() {
	if v == nil {
		return
	}
	v.State.JumpBottom(v.PageLen(), v.maxVisibleRows())
}

func (v *HooksBrowserView) OpenSelectedEvent() {
	event, ok := v.SelectedEvent()
	if !ok {
		return
	}
	v.Page = HooksBrowserPageHandlers
	v.HandlerEvent = event
	v.State = NewScrollState()
	v.State.ClampSelection(v.PageLen())
}

func (v *HooksBrowserView) ReturnToEvents() {
	if v == nil {
		return
	}
	previous := v.HandlerEvent
	v.Page = HooksBrowserPageEvents
	v.HandlerEvent = ""
	v.State = NewScrollState()
	events := AllHookEventNames()
	selected := 0
	for idx, event := range events {
		if event == previous {
			selected = idx
			break
		}
	}
	if len(events) > 0 {
		v.State.SelectedIdx = selected
		v.State.HasSelection = true
	}
}

func (v *HooksBrowserView) ToggleSelectedHook() {
	if v == nil || v.Page != HooksBrowserPageHandlers {
		return
	}
	idx, ok := v.SelectedHookIndex(v.HandlerEvent)
	if !ok {
		return
	}
	hook := &v.Entry.Hooks[idx]
	if hook.IsManaged || HookNeedsReviewMetadata(*hook) {
		return
	}
	hook.Enabled = !hook.Enabled
	v.Events = append(v.Events, HooksBrowserEvent{
		Kind:    HooksBrowserEventSetEnabled,
		Key:     hook.Key,
		Enabled: hook.Enabled,
	})
}

func (v *HooksBrowserView) TrustSelectedHook() {
	if v == nil || v.Page != HooksBrowserPageHandlers {
		return
	}
	idx, ok := v.SelectedHookIndex(v.HandlerEvent)
	if !ok {
		return
	}
	hook := &v.Entry.Hooks[idx]
	if !HookNeedsReviewMetadata(*hook) {
		return
	}
	hook.TrustStatus = appserver.HookTrustTrusted
	v.Events = append(v.Events, HooksBrowserEvent{
		Kind:        HooksBrowserEventTrustHook,
		Key:         hook.Key,
		CurrentHash: hook.CurrentHash,
	})
}

func (v *HooksBrowserView) TrustAllHooks() {
	if v == nil {
		return
	}
	updates := []tui.HookTrustUpdate{}
	for idx := range v.Entry.Hooks {
		hook := &v.Entry.Hooks[idx]
		if !HookNeedsReviewMetadata(*hook) {
			continue
		}
		hook.TrustStatus = appserver.HookTrustTrusted
		updates = append(updates, tui.HookTrustUpdate{Key: hook.Key, CurrentHash: hook.CurrentHash})
	}
	if len(updates) > 0 {
		v.Events = append(v.Events, HooksBrowserEvent{Kind: HooksBrowserEventTrustHooks, Updates: updates})
	}
}

func (v *HooksBrowserView) Close() {
	if v != nil {
		v.Complete = true
	}
}

func (v *HooksBrowserView) HandleKey(key string) {
	if v == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "up", "ctrl+p":
		v.MoveUp()
	case "down", "ctrl+n":
		v.MoveDown()
	case "pageup", "ctrl+u":
		v.PageUp()
	case "pagedown", "ctrl+d":
		v.PageDown()
	case "home", "ctrl+a":
		v.JumpTop()
	case "end", "ctrl+e":
		v.JumpBottom()
	case "enter":
		if v.Page == HooksBrowserPageEvents {
			v.OpenSelectedEvent()
		} else {
			v.ToggleSelectedHook()
		}
	case "space":
		if v.Page == HooksBrowserPageHandlers {
			v.ToggleSelectedHook()
		}
	case "t":
		if v.Page == HooksBrowserPageEvents {
			v.TrustAllHooks()
		} else {
			v.TrustSelectedHook()
		}
	case "esc", "ctrl+c":
		if v.Page == HooksBrowserPageHandlers {
			v.ReturnToEvents()
		} else {
			v.Close()
		}
	}
}

func (v *HooksBrowserView) Rows(width int) []string {
	if v == nil {
		return nil
	}
	if v.Page == HooksBrowserPageHandlers {
		return v.handlerRows(width)
	}
	return v.eventRows(width)
}

func (v *HooksBrowserView) eventRows(width int) []string {
	rows := []string{
		"Hooks",
		"Lifecycle hooks from config and enabled plugins.",
	}
	if message, ok := ReviewNeededMessage(v.ReviewNeededTotalCount()); ok {
		rows = append(rows, "! "+message)
	}
	if len(v.Entry.Warnings) > 0 || len(v.Entry.Errors) > 0 {
		rows = append(rows, "Issues")
		for _, warning := range v.Entry.Warnings {
			rows = append(rows, "! "+warning)
		}
		for _, issue := range v.Entry.Errors {
			rows = append(rows, "x "+issue.Path+": "+issue.Message)
		}
	}
	eventRows := v.EventRows()
	showReview := false
	for _, row := range eventRows {
		if row.NeedsReview > 0 {
			showReview = true
			break
		}
	}
	header := "Event                  Installed   Active      "
	if showReview {
		header += "Review      "
	}
	header += "Description"
	rows = append(rows, truncateHookRow(header, width))
	for idx, row := range eventRows {
		line := formatHookEventRow(row, showReview)
		line = truncateHookRow(line, width)
		if v.State.HasSelection && idx == v.State.SelectedIdx {
			line = tui.RenderSelectedRow(line)
		}
		rows = append(rows, line)
	}
	if v.ReviewNeededTotalCount() > 0 {
		rows = append(rows, "Press t to trust all; enter to review hooks; esc to close")
	} else {
		rows = append(rows, "Press enter to view hooks; esc to close")
	}
	return rows
}

func (v *HooksBrowserView) handlerRows(width int) []string {
	reviewCount := v.ReviewNeededCount(v.HandlerEvent)
	rows := []string{HookEventLabel(v.HandlerEvent) + " hooks"}
	if message, ok := ReviewNeededMessage(reviewCount); ok {
		rows = append(rows, message)
	} else {
		rows = append(rows, "Turn hooks on or off. Your changes are saved automatically.")
	}
	handlers := v.HandlersForEvent(v.HandlerEvent)
	if len(handlers) == 0 {
		rows = append(rows, "No hooks installed for this event.")
		rows = append(rows, "Press esc to go back")
		return rows
	}
	v.State.ClampSelection(len(handlers))
	v.State.EnsureVisible(len(handlers), v.maxVisibleRows())
	start := v.State.ScrollTop
	end := min(start+v.maxVisibleRows(), len(handlers))
	for idx := start; idx < end; idx++ {
		hook := handlers[idx]
		row := HookHandlerRow(hook, idx)
		row = truncateHookRow(row, width)
		if v.State.HasSelection && idx == v.State.SelectedIdx {
			row = tui.RenderSelectedRow(row)
		}
		rows = append(rows, row)
	}
	rows = append(rows, v.detailRows(width)...)
	rows = append(rows, v.footerHint())
	return rows
}

func (v *HooksBrowserView) detailRows(width int) []string {
	hook, ok := v.SelectedHook(v.HandlerEvent)
	if !ok {
		return []string{"No hooks installed for this event."}
	}
	rows := []string{
		hookDetailLine("Event", HookEventLabel(v.HandlerEvent)),
	}
	if hook.Matcher != nil {
		rows = append(rows, wrapHookDetail("Matcher", *hook.Matcher, width, 0)...)
	}
	rows = append(rows, wrapHookDetail("Source", HookSourceDetail(hook), width, 0)...)
	command := "-"
	if hook.Command != nil {
		command = *hook.Command
	}
	commandLines := wrapHookDetail("Command", command, width, maxHookCommandDetailLines)
	rows = append(rows, commandLines...)
	rows = append(rows, hookDetailLine("Timeout", formatInt(int(hook.TimeoutSec))+"s"))
	rows = append(rows, hookDetailLine("Trust", HookTrustLabel(hook.TrustStatus)))
	return rows
}

func (v *HooksBrowserView) footerHint() string {
	hook, ok := v.SelectedHook(v.HandlerEvent)
	if !ok {
		return "Press esc to go back"
	}
	if hook.IsManaged {
		return "Managed hooks are always on; press esc to go back"
	}
	if HookNeedsReviewMetadata(hook) {
		return "Press t to trust; esc to go back"
	}
	return "Press space or enter to toggle; esc to go back"
}

func (v *HooksBrowserView) ReviewNeededTotalCount() int {
	if v == nil {
		return 0
	}
	count := 0
	for _, hook := range v.Entry.Hooks {
		if HookNeedsReviewMetadata(hook) {
			count++
		}
	}
	return count
}

func (v *HooksBrowserView) ReviewNeededCount(event appserver.HookEventName) int {
	if v == nil {
		return 0
	}
	count := 0
	for _, hook := range v.Entry.Hooks {
		if hook.EventName == event && HookNeedsReviewMetadata(hook) {
			count++
		}
	}
	return count
}

func HookIsActive(hook appserver.HookMetadata) bool {
	return hook.Enabled && (hook.TrustStatus == appserver.HookTrustManaged || hook.TrustStatus == appserver.HookTrustTrusted)
}

func HookNeedsReviewMetadata(hook appserver.HookMetadata) bool {
	return hook.TrustStatus == appserver.HookTrustUntrusted || hook.TrustStatus == appserver.HookTrustModified
}

func ReviewNeededMessage(count int) (string, bool) {
	switch count {
	case 0:
		return "", false
	case 1:
		return "1 hook needs review before it can run.", true
	default:
		return formatInt(count) + " hooks need review before they can run.", true
	}
}

func HookTrustLabel(status appserver.HookTrustStatus) string {
	switch status {
	case appserver.HookTrustManaged:
		return "Managed"
	case appserver.HookTrustTrusted:
		return "Trusted"
	case appserver.HookTrustUntrusted:
		return "New hook - review required"
	case appserver.HookTrustModified:
		return "Modified since last trusted - review required"
	default:
		return string(status)
	}
}

func HookEventLabel(event appserver.HookEventName) string {
	switch event {
	case appserver.HookEventPreToolUse:
		return "PreToolUse"
	case appserver.HookEventPermissionRequest:
		return "PermissionRequest"
	case appserver.HookEventPostToolUse:
		return "PostToolUse"
	case appserver.HookEventPreCompact:
		return "PreCompact"
	case appserver.HookEventPostCompact:
		return "PostCompact"
	case appserver.HookEventSessionStart:
		return "SessionStart"
	case appserver.HookEventSessionEnd:
		return "SessionEnd"
	case appserver.HookEventUserPromptSubmit:
		return "UserPromptSubmit"
	case appserver.HookEventSubagentStart:
		return "SubagentStart"
	case appserver.HookEventSubagentStop:
		return "SubagentStop"
	case appserver.HookEventStop:
		return "Stop"
	default:
		return string(event)
	}
}

func HookEventDescription(event appserver.HookEventName) string {
	switch event {
	case appserver.HookEventPreToolUse:
		return "Before a tool executes"
	case appserver.HookEventPermissionRequest:
		return "When permission is requested"
	case appserver.HookEventPostToolUse:
		return "After a tool executes"
	case appserver.HookEventPreCompact:
		return "Before context compaction"
	case appserver.HookEventPostCompact:
		return "After context compaction"
	case appserver.HookEventSessionStart:
		return "When a new session starts"
	case appserver.HookEventSessionEnd:
		return "When a session ends"
	case appserver.HookEventUserPromptSubmit:
		return "When the user submits a prompt"
	case appserver.HookEventSubagentStart:
		return "When a subagent is created"
	case appserver.HookEventSubagentStop:
		return "Right before a subagent ends its turn"
	case appserver.HookEventStop:
		return "Right before Codex ends its turn"
	default:
		return ""
	}
}

func HookHandlerRow(hook appserver.HookMetadata, idx int) string {
	marker := " "
	if HookNeedsReviewMetadata(hook) {
		marker = "!"
	} else if HookIsActive(hook) {
		marker = "x"
	}
	row := "[" + marker + "] Hook " + formatInt(idx+1)
	switch hook.TrustStatus {
	case appserver.HookTrustModified:
		row += " · modified"
	case appserver.HookTrustUntrusted:
		row += " · new"
	}
	return row
}

func HookSourceDetail(hook appserver.HookMetadata) string {
	switch hook.Source {
	case appserver.HookSourcePlugin:
		if hook.PluginID != nil {
			return "Plugin - " + *hook.PluginID
		}
		return "Plugin"
	case appserver.HookSourceSystem, appserver.HookSourceMDM, appserver.HookSourceCloudRequirements,
		appserver.HookSourceCloudManagedConfig, appserver.HookSourceLegacyConfigFile, appserver.HookSourceLegacyConfigMDM:
		return HookSourceLabel(hook.Source)
	default:
		return HookSourceLabel(hook.Source) + " - " + tuistatus.FormatDirectoryDisplay(hook.SourcePath, -1)
	}
}

func HookSourceLabel(source appserver.HookSource) string {
	switch source {
	case appserver.HookSourceSystem, appserver.HookSourceMDM, appserver.HookSourceCloudRequirements,
		appserver.HookSourceLegacyConfigFile, appserver.HookSourceLegacyConfigMDM:
		return "Admin config"
	case appserver.HookSourceUser:
		return "User config"
	case appserver.HookSourceProject:
		return "Project config"
	case appserver.HookSourceSessionFlags:
		return "Session flags"
	case appserver.HookSourcePlugin:
		return "Plugin"
	case appserver.HookSourceCloudManagedConfig:
		return "Cloud-managed config"
	default:
		return "Unknown source"
	}
}

func AllHookEventNames() []appserver.HookEventName {
	return []appserver.HookEventName{
		appserver.HookEventPreToolUse,
		appserver.HookEventPermissionRequest,
		appserver.HookEventPostToolUse,
		appserver.HookEventPreCompact,
		appserver.HookEventPostCompact,
		appserver.HookEventSessionStart,
		appserver.HookEventSessionEnd,
		appserver.HookEventUserPromptSubmit,
		appserver.HookEventSubagentStart,
		appserver.HookEventSubagentStop,
		appserver.HookEventStop,
	}
}

func formatHookEventRow(row HookEventRow, showReview bool) string {
	line := padRight(HookEventLabel(row.EventName), 22) +
		padRight(formatInt(row.Installed), 12) +
		padRight(formatInt(row.Active), 12)
	if showReview {
		line += padRight(formatInt(row.NeedsReview), 12)
	}
	line += HookEventDescription(row.EventName)
	return line
}

func padRight(value string, width int) string {
	runes := []rune(value)
	if len(runes) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-len(runes))
}

func truncateHookRow(value string, width int) string {
	return tui.TruncateWithEllipsis(value, width)
}

func hookDetailLine(label string, value string) string {
	return hookDetailPrefix(label) + value
}

func hookDetailPrefix(label string) string {
	if len([]rune(label)) >= 10 {
		return label
	}
	return label + strings.Repeat(" ", 10-len([]rune(label)))
}

func wrapHookDetail(label string, value string, width int, maxLines int) []string {
	prefix := hookDetailPrefix(label)
	full := prefix + value
	if width <= 0 || len([]rune(full)) <= width {
		return []string{full}
	}
	words := strings.Fields(value)
	if len(words) == 0 {
		return []string{prefix}
	}
	lines := []string{}
	current := prefix
	for _, word := range words {
		candidate := current
		if strings.TrimSpace(current) != strings.TrimSpace(prefix) {
			candidate += " "
		}
		candidate += word
		if len([]rune(candidate)) > width && strings.TrimSpace(current) != strings.TrimSpace(prefix) {
			lines = append(lines, current)
			current = strings.Repeat(" ", len([]rune(prefix))) + word
			if maxLines > 0 && len(lines) >= maxLines {
				lines = lines[:maxLines]
				lines[len(lines)-1] = tui.TruncateWithEllipsis(lines[len(lines)-1], width)
				return lines
			}
			continue
		}
		current = candidate
	}
	if current != "" {
		lines = append(lines, current)
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[len(lines)-1] = tui.TruncateWithEllipsis(lines[len(lines)-1], width)
	}
	return lines
}
