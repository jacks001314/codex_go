// Package agentsoverview implements the interactive agents-overview dashboard
// core. It mirrors Rust codex-rs/tui/src/app/agents_overview_view.rs (#39094 /
// #39112): a full-screen, list-based dashboard of root agent sessions with
// status/project grouping, search, new-task dispatch, rename, stop and
// details rendering. The package is deliberately free of terminal and
// app-server dependencies so the dashboard can be unit-tested like the Rust
// snapshot suite and reused by both the standalone `codex agents` command and
// a future in-session `/agents` view.
package agentsoverview

import (
	"path/filepath"
	"strings"
	"unicode"

	"codex_go/gitutil"
)

// Group classifies a root thread for the dashboard's status grouping.
type Group int

const (
	GroupNeedsYou Group = iota // waiting on approval or user input, or system error
	GroupWorking               // active (turn in progress)
	GroupReady                 // idle
	GroupFinished              // not loaded / closed
)

// GroupForStatus mirrors Rust AgentsOverviewGroup::for_status.
func GroupForStatus(statusType string, waitingOnApproval, waitingOnUserInput bool) Group {
	switch strings.ToLower(strings.TrimSpace(statusType)) {
	case "active":
		if waitingOnApproval || waitingOnUserInput {
			return GroupNeedsYou
		}
		return GroupWorking
	case "idle":
		return GroupReady
	case "systemerror", "system_error", "error":
		return GroupNeedsYou
	default: // "notLoaded" and unknown statuses
		return GroupFinished
	}
}

// PreviewMarkdown bounds an overview text preview to Rust's 512-character
// limit, preserving newlines and tabs for layout while stripping other control
// characters (Rust agents_overview_details::preview_markdown, #44752).
func PreviewMarkdown(text string) string {
	const previewChars = 512
	var builder strings.Builder
	kept := 0
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue
		}
		if kept >= previewChars {
			break
		}
		builder.WriteRune(r)
		kept++
	}
	return builder.String()
}

// Grouping is the dashboard's task grouping mode, cycled by the toggle
// shortcut (Rust #44957 AgentsOverviewGrouping).
type Grouping int

const (
	// GroupingProject groups by project directory (or repository when linked
	// worktrees are enabled).
	GroupingProject Grouping = iota
	// GroupingStatus groups by task status.
	GroupingStatus
	// GroupingModel groups by the task's model.
	GroupingModel
)

// Next returns the next grouping mode in the toggle cycle.
func (g Grouping) Next() Grouping {
	switch g {
	case GroupingStatus:
		return GroupingModel
	case GroupingModel:
		return GroupingProject
	default:
		return GroupingStatus
	}
}

// Label is the footer hint text for the active grouping mode (Rust
// agents_overview_render).
func (g Grouping) Label() string {
	switch g {
	case GroupingStatus:
		return "group: status"
	case GroupingModel:
		return "group: model"
	default:
		return "group: project"
	}
}

// ModelName is the group label for a row's model; missing or empty names group
// as "Unknown" (Rust agents_overview_grouping::model_name).
func ModelName(model string) string {
	if name := strings.TrimSpace(model); name != "" {
		return name
	}
	return "Unknown"
}

func (g Group) Label() string {
	switch g {
	case GroupNeedsYou:
		return "Needs input"
	case GroupWorking:
		return "Working"
	case GroupReady:
		return "Ready"
	default:
		return "Finished"
	}
}

// Dot returns the status marker glyph (Rust: red/green filled dots, cyan
// hollow dot, dim check mark). Styling is applied by the renderer.
func (g Group) Dot() string {
	switch g {
	case GroupNeedsYou:
		return "●"
	case GroupWorking:
		return "●"
	case GroupReady:
		return "○"
	default:
		return "✓"
	}
}

// Row is one root thread shown by the dashboard.
type Row struct {
	ThreadID string
	Name     string
	Preview  string
	CWD      string
	// Model is the task's model; an empty value groups as "Unknown"
	// (Rust #44957).
	Model        string
	GitBranch    string
	Group        Group
	IsCurrent    bool
	StatusActive bool // an active turn is running (enables ctrl+x stop)
}

// Title mirrors Rust: thread name, else preview, else "Untitled task".
func (r Row) Title() string {
	if strings.TrimSpace(r.Name) != "" {
		return firstLine(strings.TrimSpace(r.Name))
	}
	if strings.TrimSpace(r.Preview) != "" {
		return firstLine(strings.TrimSpace(r.Preview))
	}
	return "Untitled task"
}

// firstLine mirrors Rust's display_title, which renders only the first line of
// a task name or preview.
func firstLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return strings.TrimSpace(value[:index])
	}
	return value
}

// Completion reports how the dashboard view ended.
type Completion int

const (
	CompletionNone Completion = iota
	CompletionAccepted
	CompletionCancelled
)

// Action is an effect the host should perform after a key event. View state
// mutation happens before the action is returned.
type Action int

const (
	ActionNone Action = iota
	// ActionDispatchTask starts a new background task; the prompt is the
	// trimmed input and CWD is the selected row's cwd when project grouping
	// is active (Rust dispatch_agents_overview_task).
	ActionDispatchTask
	// ActionOpenThread opens the selected root session (completion accepted).
	ActionOpenThread
	// ActionRenameThread renames the selected row to the trimmed input.
	ActionRenameThread
	// ActionStopThread interrupts the selected row's active turn.
	ActionStopThread
	// ActionHideThread hides the selected row locally without stopping it
	// (Rust #44424). Hidden tasks stay hidden through activity and metadata
	// refreshes until explicitly resumed.
	ActionHideThread
	// ActionArchiveThread archives the selected row and its child agents after
	// confirmation (Rust #44433).
	ActionArchiveThread
	// ActionDeleteThread permanently deletes the selected row and its child
	// agents after confirmation (Rust #44433).
	ActionDeleteThread
	// ActionExit closes the dashboard (exit_on_cancel standalone mode).
	ActionExit
)

// State is the mutable view state preserved across dashboard refreshes.
type State struct {
	Input     string
	Search    string
	Searching bool
	Grouping  Grouping
	Renaming  bool
	// HiddenThreads is local visibility only; activity and metadata refreshes
	// never reveal a hidden root (Rust #44424).
	HiddenThreads map[string]struct{}
}

// View is the dashboard model.
type View struct {
	Rows         []Row
	Selected     int
	State        State
	ExitOnCancel bool
	Completion   Completion
	hints        map[string]string

	// worktreesEnabled groups rows from linked checkouts of the same repository
	// under the primary checkout (Rust #43279), and projectGroups carries the
	// resolved group key/heading for every row.
	worktreesEnabled bool
	projectGroups    []projectGroup

	// UseThemeColors enables deterministic per-thread identity colors on row
	// and detail titles (Rust #44857). ThreadColor resolves a thread id to an
	// accent color ("#rrggbb"), returning "" to fall back to the default style.
	UseThemeColors bool
	ThreadColor    func(threadID string) string
}

// projectGroup is a row's project-grouping identity. With linked worktrees
// enabled the key is the repository's shared administrative directory plus the
// directory within the checkout, and the heading points at the primary
// checkout's corresponding directory (Rust AgentsOverviewProjectGroup).
type projectGroup struct {
	commonDir   string
	relativeCWD string
	heading     string
}

func projectGroupForRow(row Row, worktreesEnabled bool) projectGroup {
	if worktreesEnabled {
		if identity, ok := gitutil.RepositoryIdentityForCWD(row.CWD); ok {
			return projectGroup{
				commonDir:   identity.CommonDir,
				relativeCWD: identity.RelativeCWD,
				heading:     filepath.Join(identity.PrimaryRoot, identity.RelativeCWD),
			}
		}
	}
	return projectGroup{commonDir: row.CWD, heading: row.CWD}
}

func (v *View) recomputeProjectGroups() {
	if v == nil {
		return
	}
	v.projectGroups = make([]projectGroup, len(v.Rows))
	for i := range v.Rows {
		v.projectGroups[i] = projectGroupForRow(v.Rows[i], v.worktreesEnabled)
	}
}

// SetWorktreesEnabled toggles linked-checkout grouping (Rust #43279).
func (v *View) SetWorktreesEnabled(enabled bool) {
	if v == nil {
		return
	}
	v.worktreesEnabled = enabled
	v.recomputeProjectGroups()
}

func (g projectGroup) equal(other projectGroup) bool {
	return g.commonDir == other.commonDir && g.relativeCWD == other.relativeCWD
}

// projectGroupAt returns the resolved project group for a row index, computing
// groups defensively if rows were applied without a refresh.
func (v *View) projectGroupAt(index int) projectGroup {
	if v == nil || index < 0 || index >= len(v.Rows) {
		return projectGroup{}
	}
	if len(v.projectGroups) != len(v.Rows) {
		v.recomputeProjectGroups()
	}
	return v.projectGroups[index]
}

// titleSpan styles a task title. Identity colors win over the fallback style
// when theme colors are enabled and the resolver returns an accent.
func (v *View) titleSpan(threadID string, title string, fallback spanStyle) span {
	if v != nil && v.UseThemeColors && v.ThreadColor != nil {
		if color := v.ThreadColor(threadID); color != "" {
			return threadTitleSpan(title, color, fallback)
		}
	}
	return span{text: title, style: fallback}
}

// ShortcutHintKey identifies a dashboard footer shortcut. The tea layer
// resolves the key from the user's keymap and passes it through
// SetShortcutHint so the rendered footer reflects custom bindings (Rust
// AgentsKeymap::primary_hint / #39142).
const (
	ShortcutHintSearch         = "search"
	ShortcutHintToggleGrouping = "toggle_grouping"
	ShortcutHintRename         = "rename"
	ShortcutHintStop           = "stop"
	ShortcutHintHide           = "hide"
	ShortcutHintArchive        = "archive"
	ShortcutHintDelete         = "delete"
)

// SetShortcutHint overrides the displayed key for one dashboard action. An
// empty binding hides the hint entirely (Rust unbind); nil/absent falls back
// to the default binding.
func (v *View) SetShortcutHint(action string, binding string) {
	if v == nil {
		return
	}
	if v.hints == nil {
		v.hints = map[string]string{}
	}
	v.hints[action] = binding
}

func (v *View) shortcutHint(action string, fallback string) (string, bool) {
	if v == nil {
		return fallback, true
	}
	binding, ok := v.hints[action]
	if !ok {
		return fallback, true
	}
	return binding, strings.TrimSpace(binding) != ""
}

// New creates a dashboard view. selectedThreadID restores the previous
// selection across refreshes (Rust AgentsOverviewView::new).
func New(rows []Row, selectedThreadID string, exitOnCancel bool) *View {
	selected := 0
	for i := range rows {
		if strings.TrimSpace(rows[i].ThreadID) == strings.TrimSpace(selectedThreadID) {
			selected = i
			break
		}
	}
	view := &View{
		Rows:         append([]Row(nil), rows...),
		Selected:     selected,
		ExitOnCancel: exitOnCancel,
	}
	view.recomputeProjectGroups()
	view.fitSelection()
	return view
}

// ThreadIDs returns the visible row thread ids in view order.
func (v *View) ThreadIDs() []string {
	if v == nil {
		return nil
	}
	ids := make([]string, 0, len(v.Rows))
	for i := range v.Rows {
		ids = append(ids, strings.TrimSpace(v.Rows[i].ThreadID))
	}
	return ids
}

// Counts returns the total (needs-you, working, ready) counts across all rows,
// mirroring the Rust header summary fold.
func (v *View) Counts() (needsYou, working, ready int) {
	if v == nil {
		return 0, 0, 0
	}
	for i := range v.Rows {
		switch v.Rows[i].Group {
		case GroupNeedsYou:
			needsYou++
		case GroupWorking:
			working++
		case GroupReady:
			ready++
		}
	}
	return needsYou, working, ready
}

// VisibleIndices returns row indices passing the search filter, sorted by
// project (cwd) or status group depending on the grouping preference. It
// mirrors Rust AgentsOverviewView::visible_indices.
func (v *View) VisibleIndices() []int {
	if v == nil {
		return nil
	}
	search := strings.ToLower(strings.TrimSpace(v.State.Search))
	visible := make([]int, 0, len(v.Rows))
	for i := range v.Rows {
		if v.isHidden(v.Rows[i].ThreadID) {
			continue
		}
		searchable := strings.ToLower(strings.Join([]string{v.Rows[i].Name, v.Rows[i].Preview, v.Rows[i].CWD}, " "))
		if search == "" || strings.Contains(searchable, search) {
			visible = append(visible, i)
		}
	}
	switch v.State.Grouping {
	case GroupingModel:
		// Model grouping: sort by model name while preserving host recency
		// order within a group (Rust #44957).
		v.stableSortByGroupKey(visible, func(index int) string {
			return ModelName(v.Rows[index].Model)
		})
	case GroupingStatus:
		// Status grouping keeps the host's recency order.
	default:
		// Project grouping: sort by the project-group key (linked checkouts of
		// one repository share a key, Rust #43279) while preserving host
		// recency order within a group.
		v.stableSortByGroupKey(visible, func(index int) string {
			group := v.projectGroupAt(index)
			return group.commonDir + "\x00" + group.relativeCWD
		})
	}
	return visible
}

// stableSortByGroupKey sorts rows by a group key with an insertion sort, which
// keeps the host's recency order within each group.
func (v *View) stableSortByGroupKey(indices []int, key func(index int) string) {
	less := func(left int, right int) bool {
		return key(left) < key(right)
	}
	for i := 1; i < len(indices); i++ {
		key := indices[i]
		j := i - 1
		for j >= 0 && less(key, indices[j]) {
			indices[j+1] = indices[j]
			j--
		}
		indices[j+1] = key
	}
}

// SelectedRow returns the currently selected row when it is visible.
func (v *View) SelectedRow() *Row {
	if v == nil {
		return nil
	}
	visible := v.VisibleIndices()
	for _, index := range visible {
		if index == v.Selected {
			return &v.Rows[index]
		}
	}
	return nil
}

// fitSelection ensures the selection is on a visible index.
func (v *View) fitSelection() {
	if v == nil {
		return
	}
	visible := v.VisibleIndices()
	if len(visible) == 0 {
		return
	}
	for _, index := range visible {
		if index == v.Selected {
			return
		}
	}
	v.Selected = visible[0]
}

// MoveSelection mirrors Rust move_selection.
func (v *View) MoveSelection(forward bool) {
	if v == nil || v.State.Renaming {
		return
	}
	visible := v.VisibleIndices()
	if len(visible) == 0 {
		return
	}
	current := 0
	for i, index := range visible {
		if index == v.Selected {
			current = i
			break
		}
	}
	if forward {
		v.Selected = visible[(current+1)%len(visible)]
	} else {
		if current == 0 {
			v.Selected = visible[len(visible)-1]
		} else {
			v.Selected = visible[current-1]
		}
	}
}

// JumpTop / JumpBottom mirror the Rust ListAction jump bindings.
func (v *View) JumpTop() {
	if v == nil || v.State.Renaming {
		return
	}
	visible := v.VisibleIndices()
	if len(visible) == 0 {
		return
	}
	v.Selected = visible[0]
}

func (v *View) JumpBottom() {
	if v == nil || v.State.Renaming {
		return
	}
	visible := v.VisibleIndices()
	if len(visible) == 0 {
		return
	}
	v.Selected = visible[len(visible)-1]
}

// PageDown / PageUp move five rows (Rust ListAction::PageUp | PageDown).
func (v *View) PageDown() {
	for i := 0; i < 5; i++ {
		v.MoveSelection(true)
	}
}

func (v *View) PageUp() {
	for i := 0; i < 5; i++ {
		v.MoveSelection(false)
	}
}

// ToggleGrouping cycles project -> status -> model grouping (Rust #44957).
func (v *View) ToggleGrouping() {
	if v == nil {
		return
	}
	v.State.Grouping = v.State.Grouping.Next()
	v.fitSelection()
}

// sameGroup reports whether two rows share the active grouping's group (Rust
// AgentsOverviewView::same_group).
func (v *View) sameGroup(grouping Grouping, left int, right int) bool {
	if v == nil || left < 0 || right < 0 || left >= len(v.Rows) || right >= len(v.Rows) {
		return false
	}
	switch grouping {
	case GroupingStatus:
		return v.Rows[left].Group == v.Rows[right].Group
	case GroupingModel:
		return ModelName(v.Rows[left].Model) == ModelName(v.Rows[right].Model)
	default:
		return v.projectGroupAt(left).equal(v.projectGroupAt(right))
	}
}

// groupHeading is the rendered header for a row's group (Rust #44957).
func (v *View) groupHeading(grouping Grouping, index int) string {
	if v == nil || index < 0 || index >= len(v.Rows) {
		return ""
	}
	switch grouping {
	case GroupingStatus:
		return v.Rows[index].Group.Label()
	case GroupingModel:
		return ModelName(v.Rows[index].Model)
	default:
		return v.projectGroupAt(index).heading
	}
}

// ToggleSearch enters or leaves search mode (Rust ctrl+f).
func (v *View) ToggleSearch() {
	if v == nil || v.State.Renaming {
		return
	}
	v.State.Searching = !v.State.Searching
	if !v.State.Searching {
		v.State.Search = ""
	}
}

// ClearNew resets the new-task/search/rename state (Rust ctrl+n).
func (v *View) ClearNew() {
	if v == nil {
		return
	}
	v.State.Search = ""
	v.State.Searching = false
	v.State.Renaming = false
	v.State.Input = ""
}

// BeginRename starts renaming the selected row (Rust ctrl+r). It returns
// false when the current input is not empty.
func (v *View) BeginRename() bool {
	row := v.SelectedRow()
	if v == nil || v.State.Input != "" || row == nil {
		return false
	}
	v.State.Input = row.Title()
	v.State.Search = ""
	v.State.Searching = false
	v.State.Renaming = true
	return true
}

// CanOpenWithRight reports whether Right should open the selected task from an
// empty, focused composer (Rust #44344). Editing metadata (rename) and a
// non-empty draft keep Right for the editor.
func (v *View) CanOpenWithRight() bool {
	if v == nil || v.State.Renaming {
		return false
	}
	if strings.TrimSpace(v.State.Input) != "" {
		return false
	}
	return v.SelectedRow() != nil
}

// Activate mirrors Rust AgentsOverviewView::activate: dispatch when the
// input is non-empty, apply the rename when renaming, otherwise open the
// selected thread.
func (v *View) Activate() Action {
	if v == nil {
		return ActionNone
	}
	trimmed := strings.TrimSpace(v.State.Input)
	if !v.State.Searching && v.State.Input != "" && trimmed == "" {
		return ActionNone
	}
	if !v.State.Searching && trimmed != "" {
		if v.State.Renaming {
			if row := v.SelectedRow(); row != nil {
				v.State.Renaming = false
				v.State.Input = ""
				return ActionRenameThread
			}
			v.State.Renaming = false
			v.State.Input = ""
			return ActionNone
		}
		v.State.Input = ""
		return ActionDispatchTask
	}
	if row := v.SelectedRow(); row != nil && !v.State.Renaming {
		if v.State.Searching {
			v.State.Search = ""
			v.State.Searching = false
		}
		v.Completion = CompletionAccepted
		return ActionOpenThread
	}
	return ActionNone
}

// Cancel mirrors Rust ListAction::Cancel: clear search, then input/rename,
// then exit when exit_on_cancel.
func (v *View) Cancel() Action {
	if v == nil {
		return ActionNone
	}
	if v.State.Searching {
		v.State.Search = ""
		v.State.Searching = false
		v.Selected = 0
		return ActionNone
	}
	if v.State.Input != "" || v.State.Renaming {
		v.State.Input = ""
		v.State.Renaming = false
		return ActionNone
	}
	if v.ExitOnCancel {
		v.Completion = CompletionCancelled
		return ActionExit
	}
	v.Completion = CompletionCancelled
	return ActionNone
}

// StopSelected returns ActionStopThread for an active selected row, else none.
func (v *View) StopSelected() Action {
	row := v.SelectedRow()
	if v == nil || row == nil || !row.StatusActive {
		return ActionNone
	}
	return ActionStopThread
}

// ArchiveSelected returns ActionArchiveThread for a selected row, else none
// (Rust #44433). The confirmation and server lifecycle work live in the host.
func (v *View) ArchiveSelected() Action {
	if v == nil || v.SelectedRow() == nil {
		return ActionNone
	}
	return ActionArchiveThread
}

// DeleteSelected returns ActionDeleteThread for a selected row, else none
// (Rust #44433).
func (v *View) DeleteSelected() Action {
	if v == nil || v.SelectedRow() == nil {
		return ActionNone
	}
	return ActionDeleteThread
}

// isHidden reports whether threadID is hidden locally (Rust #44424).
func (v *View) isHidden(threadID string) bool {
	if v == nil || v.State.HiddenThreads == nil {
		return false
	}
	_, ok := v.State.HiddenThreads[strings.TrimSpace(threadID)]
	return ok
}

// HideSelected hides the selected row locally without stopping its task and
// moves the selection to the next visible row (Rust #44424).
func (v *View) HideSelected() Action {
	row := v.SelectedRow()
	if v == nil || row == nil {
		return ActionNone
	}
	threadID := strings.TrimSpace(row.ThreadID)
	if threadID == "" {
		return ActionNone
	}
	if v.State.HiddenThreads == nil {
		v.State.HiddenThreads = map[string]struct{}{}
	}
	hiddenIndex := v.Selected
	v.State.HiddenThreads[threadID] = struct{}{}
	visible := v.VisibleIndices()
	if len(visible) == 0 {
		v.Selected = 0
		return ActionHideThread
	}
	// Move to the next still-visible row after the hidden one, else the last.
	v.Selected = visible[len(visible)-1]
	for _, index := range visible {
		if index > hiddenIndex {
			v.Selected = index
			break
		}
	}
	return ActionHideThread
}

// UnhideThread makes a hidden task visible again; explicit resume clears the
// local hide (Rust #44424).
func (v *View) UnhideThread(threadID string) {
	if v == nil || v.State.HiddenThreads == nil {
		return
	}
	delete(v.State.HiddenThreads, strings.TrimSpace(threadID))
	v.fitSelection()
}

// HiddenThreads returns a copy of the locally hidden thread ids so callers can
// preserve them across dashboard close/reopen (Rust #44424).
func (v *View) HiddenThreads() map[string]struct{} {
	if v == nil || len(v.State.HiddenThreads) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(v.State.HiddenThreads))
	for id := range v.State.HiddenThreads {
		out[id] = struct{}{}
	}
	return out
}

// SetHiddenThreads restores locally hidden thread ids.
func (v *View) SetHiddenThreads(hidden map[string]struct{}) {
	if v == nil || len(hidden) == 0 {
		return
	}
	cloned := make(map[string]struct{}, len(hidden))
	for id := range hidden {
		cloned[id] = struct{}{}
	}
	v.State.HiddenThreads = cloned
	v.fitSelection()
}

// TypeChar appends a rune to the search or task input (Rust KeyCode::Char).
func (v *View) TypeChar(character rune) {
	if v == nil {
		return
	}
	if v.State.Searching {
		v.State.Search += string(character)
		v.fitSelection()
	} else {
		v.State.Input += string(character)
	}
}

// Backspace pops the last rune (Rust KeyCode::Backspace).
func (v *View) Backspace() {
	if v == nil {
		return
	}
	if v.State.Searching {
		runes := []rune(v.State.Search)
		if len(runes) > 0 {
			v.State.Search = string(runes[:len(runes)-1])
			v.fitSelection()
		}
		return
	}
	runes := []rune(v.State.Input)
	if len(runes) > 0 {
		v.State.Input = string(runes[:len(runes)-1])
	}
}

// Paste appends sanitized pasted text to the active input (Rust handle_paste).
func (v *View) Paste(text string) {
	if v == nil {
		return
	}
	text = strings.ReplaceAll(text, "\r", "")
	text = strings.ReplaceAll(text, "\n", " ")
	if v.State.Searching {
		v.State.Search += text
		v.fitSelection()
	} else {
		v.State.Input += text
	}
}

// ApplyRefresh replaces the rows while preserving the selection (Rust
// apply_agents_overview_thread_refresh). Renaming cancels if the selected
// thread disappeared.
func (v *View) ApplyRefresh(rows []Row, selectedThreadID string) {
	if v == nil {
		return
	}
	selected := strings.TrimSpace(selectedThreadID)
	if selected == "" {
		selected = v.SelectedThreadID()
	}
	view := New(rows, selected, v.ExitOnCancel)
	view.State = v.State
	view.worktreesEnabled = v.worktreesEnabled
	view.recomputeProjectGroups()
	// Hidden roots stay hidden across refreshes; if the restored selection
	// points at one, move it to a visible row.
	view.fitSelection()
	if selected != "" && !containsThreadID(view.Rows, selected) && view.State.Renaming {
		view.State.Renaming = false
		view.State.Input = ""
	}
	*v = *view
}

func (v *View) SelectedThreadID() string {
	if v == nil || v.Selected < 0 || v.Selected >= len(v.Rows) {
		return ""
	}
	return strings.TrimSpace(v.Rows[v.Selected].ThreadID)
}

func containsThreadID(rows []Row, threadID string) bool {
	for i := range rows {
		if strings.TrimSpace(rows[i].ThreadID) == threadID {
			return true
		}
	}
	return false
}

// Prompt returns the active prompt label, input and placeholder text,
// mirroring Rust AgentsOverviewView::render prompt area.
func (v *View) Prompt() (label, input, placeholder string) {
	if v == nil {
		return "New task › ", "", ""
	}
	switch {
	case v.State.Searching:
		return "Search › ", v.State.Search, ""
	case v.State.Renaming:
		return "Rename › ", v.State.Input, ""
	default:
		if v.State.Input == "" {
			return "New task › ", "", "Describe a task and press enter to dispatch it"
		}
		return "New task › ", v.State.Input, ""
	}
}
