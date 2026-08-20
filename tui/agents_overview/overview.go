// Package agentsoverview implements the interactive agents-overview dashboard
// core. It mirrors Rust codex-rs/tui/src/app/agents_overview_view.rs (#39094 /
// #39112): a full-screen, list-based dashboard of root agent sessions with
// status/project grouping, search, new-task dispatch, rename, stop and
// details rendering. The package is deliberately free of terminal and
// app-server dependencies so the dashboard can be unit-tested like the Rust
// snapshot suite and reused by both the standalone `codex agents` command and
// a future in-session `/agents` view.
package agentsoverview

import "strings"

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
	ThreadID     string
	Name         string
	Preview      string
	CWD          string
	GitBranch    string
	Group        Group
	IsCurrent    bool
	StatusActive bool // an active turn is running (enables ctrl+x stop)
}

// Title mirrors Rust: thread name, else preview, else "Untitled task".
func (r Row) Title() string {
	if strings.TrimSpace(r.Name) != "" {
		return strings.TrimSpace(r.Name)
	}
	if strings.TrimSpace(r.Preview) != "" {
		return strings.TrimSpace(r.Preview)
	}
	return "Untitled task"
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
	// ActionExit closes the dashboard (exit_on_cancel standalone mode).
	ActionExit
)

// State is the mutable view state preserved across dashboard refreshes.
type State struct {
	Input          string
	Search         string
	Searching      bool
	StatusGrouping bool
	Renaming       bool
}

// View is the dashboard model.
type View struct {
	Rows         []Row
	Selected     int
	State        State
	ExitOnCancel bool
	Completion   Completion
	hints        map[string]string
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
		searchable := strings.ToLower(strings.Join([]string{v.Rows[i].Name, v.Rows[i].Preview, v.Rows[i].CWD}, " "))
		if search == "" || strings.Contains(searchable, search) {
			visible = append(visible, i)
		}
	}
	if !v.State.StatusGrouping {
		// Project grouping: sort by cwd then updated-at recency is handled by
		// the host (rows arrive sorted); here we keep a stable cwd ordering.
		stableSortByCWD(visible, v.Rows)
	}
	return visible
}

func stableSortByCWD(indices []int, rows []Row) {
	// insertion sort keeps host recency order within the same project.
	for i := 1; i < len(indices); i++ {
		key := indices[i]
		j := i - 1
		for j >= 0 && rows[indices[j]].CWD > rows[key].CWD {
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

// ToggleGrouping switches between project (cwd) and status grouping.
func (v *View) ToggleGrouping() {
	if v == nil {
		return
	}
	v.State.StatusGrouping = !v.State.StatusGrouping
	v.fitSelection()
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
