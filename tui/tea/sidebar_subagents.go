package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/app/sidebar_subagents.rs and
// sidebar_agent_status.rs (#62149). The sidebar chooser lists the primary
// conversation's spawn tree with each agent's status so the user can watch its
// progress and switch to its conversation. The tea client observes the tree
// through the parent's item stream (collab agent tool calls and sub-agent
// activity) plus the agent reader it already uses for the agent picker.

// sidebarAgentPhase is Rust sidebar_agent_status::State.
type sidebarAgentPhase int

const (
	sidebarAgentStarting sidebarAgentPhase = iota
	sidebarAgentRunning
	sidebarAgentWaiting
	sidebarAgentIdle
	sidebarAgentCompleted
	sidebarAgentStopped
	sidebarAgentFailed
	sidebarAgentClosed
	sidebarAgentUnavailable
)

// active reports whether the agent counts as working for the row ordering
// (Rust State::active).
func (p sidebarAgentPhase) active() bool {
	return p == sidebarAgentStarting || p == sidebarAgentRunning || p == sidebarAgentWaiting
}

// done reports whether the phase is an explicit outcome (Rust State::done).
func (p sidebarAgentPhase) done() bool {
	return p == sidebarAgentCompleted || p == sidebarAgentStopped ||
		p == sidebarAgentFailed || p == sidebarAgentClosed
}

// supersedes is Rust State::supersedes: spawn activity can arrive after the
// child's own completion notification, and an idle or unloaded update never
// clears an explicit outcome. Only explicit work starts do.
func (p sidebarAgentPhase) supersedes(previous sidebarAgentPhase, hasPrevious bool, startHint bool) bool {
	if !hasPrevious || !previous.done() {
		return true
	}
	if p == sidebarAgentIdle || p == sidebarAgentUnavailable {
		return false
	}
	return !startHint || p.done()
}

// label is Rust State::label: the status glyph and its caption.
func (p sidebarAgentPhase) label() (string, string) {
	switch p {
	case sidebarAgentStarting:
		return "●", "Starting"
	case sidebarAgentRunning:
		return "●", "Running"
	case sidebarAgentWaiting:
		return "●", "Waiting"
	case sidebarAgentIdle:
		return "○", "Idle"
	case sidebarAgentCompleted:
		return "○", "Completed"
	case sidebarAgentStopped:
		return "○", "Stopped"
	case sidebarAgentFailed:
		return "!", "Failed"
	case sidebarAgentClosed:
		return "○", "Closed"
	default:
		return "○", "Unavailable"
	}
}

// sidebarAgent is one registered member of a spawn tree.
type sidebarAgent struct {
	threadID  string
	agentPath string
	nickname  string
	role      string
	phase     sidebarAgentPhase
	// restored marks a phase seeded from a discovery read rather than observed
	// live, so a later read may refresh it (Rust restored_outcomes).
	restored bool
}

// sidebarAgentTree is the spawn tree below one primary conversation.
type sidebarAgentTree struct {
	root   string
	order  []string
	agents map[string]*sidebarAgent
}

func (t *sidebarAgentTree) isMember(threadID string) bool {
	if t == nil || t.agents == nil {
		return false
	}
	_, ok := t.agents[strings.TrimSpace(threadID)]
	return ok
}

// sidebarAgentRow is one painted Subagents row (Rust sidebar_chooser::AgentRow).
type sidebarAgentRow struct {
	ThreadID string
	Name     string
	Symbol   string
	Status   string
}

// ensureSidebarAgentTrees lazily allocates the per-root spawn trees.
func (m *Model) ensureSidebarAgentTrees() map[string]*sidebarAgentTree {
	if m.sidebarAgentTrees == nil {
		m.sidebarAgentTrees = map[string]*sidebarAgentTree{}
	}
	return m.sidebarAgentTrees
}

// sidebarAgentTreeFor returns the tree rooted at root, creating it when asked.
func (m *Model) sidebarAgentTreeFor(root string, create bool) *sidebarAgentTree {
	root = strings.TrimSpace(root)
	if m == nil || root == "" {
		return nil
	}
	trees := m.ensureSidebarAgentTrees()
	tree := trees[root]
	if tree == nil && create {
		tree = &sidebarAgentTree{root: root, agents: map[string]*sidebarAgent{}}
		trees[root] = tree
	}
	return tree
}

// sidebarAgentRootForThread resolves which spawn tree owns threadID: the tree
// that already lists it, else the displayed conversation itself, which makes
// the next spawn register under it.
func (m *Model) sidebarAgentRootForThread(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if m == nil || threadID == "" {
		return ""
	}
	for root, tree := range m.sidebarAgentTrees {
		if tree.isMember(threadID) {
			return root
		}
	}
	if m.State != nil && strings.TrimSpace(m.State.ThreadID) == threadID {
		return threadID
	}
	return ""
}

// registerSidebarAgent records a child of parent (Rust App::register_sidebar_agent).
// A parent outside the tree is ignored so unrelated threads never leak in.
func (m *Model) registerSidebarAgent(parent string, child string) bool {
	parent = strings.TrimSpace(parent)
	child = strings.TrimSpace(child)
	if m == nil || parent == "" || child == "" || !m.sideWorkspaceEnabled() {
		return false
	}
	root := m.sidebarAgentRootForThread(parent)
	if root == "" || root == child {
		return false
	}
	tree := m.sidebarAgentTreeFor(root, true)
	if tree == nil {
		return false
	}
	if parent != root && !tree.isMember(parent) {
		return false
	}
	if tree.isMember(child) {
		return true
	}
	tree.agents[child] = &sidebarAgent{threadID: child, phase: sidebarAgentIdle, restored: true}
	tree.order = append(tree.order, child)
	return true
}

// setSidebarAgentPhase applies one observed phase (Rust status::update).
func (m *Model) setSidebarAgentPhase(root string, threadID string, phase sidebarAgentPhase, startHint bool) bool {
	tree := m.sidebarAgentTreeFor(root, false)
	if tree == nil {
		return false
	}
	agent := tree.agents[strings.TrimSpace(threadID)]
	if agent == nil {
		return false
	}
	if !phase.supersedes(agent.phase, true, startHint) {
		return false
	}
	agent.phase = phase
	agent.restored = false
	return true
}

// setSidebarAgentIdentity fills in the name parts a live event supplied.
func (m *Model) setSidebarAgentIdentity(root string, threadID string, agentPath string, nickname string, role string) {
	tree := m.sidebarAgentTreeFor(root, false)
	if tree == nil {
		return
	}
	agent := tree.agents[strings.TrimSpace(threadID)]
	if agent == nil {
		return
	}
	if path := strings.TrimSpace(agentPath); path != "" {
		agent.agentPath = path
	}
	if name := strings.TrimSpace(nickname); name != "" {
		agent.nickname = name
	}
	if value := strings.TrimSpace(role); value != "" {
		agent.role = value
	}
}

// sidebarAgentPhaseForStatus maps a collab agent status to its phase.
func sidebarAgentPhaseForStatus(status string) (sidebarAgentPhase, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pendinginit":
		return sidebarAgentStarting, true
	case "running":
		return sidebarAgentRunning, true
	case "interrupted":
		return sidebarAgentStopped, true
	case "completed":
		return sidebarAgentCompleted, true
	case "errored":
		return sidebarAgentFailed, true
	case "shutdown":
		return sidebarAgentClosed, true
	case "notfound":
		return sidebarAgentUnavailable, true
	}
	return sidebarAgentIdle, false
}

// sidebarAgentPhaseForActivity maps a sub-agent activity kind to its phase.
// Ordinary mailbox delivery ("interacted") need not start a turn, so it is not
// an outcome (Rust visit_item).
func sidebarAgentPhaseForActivity(kind string) (sidebarAgentPhase, bool, bool) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "started":
		return sidebarAgentRunning, true, true
	case "completed":
		return sidebarAgentCompleted, false, true
	case "interrupted":
		return sidebarAgentStopped, false, true
	}
	return sidebarAgentIdle, false, false
}

// observeSidebarAgentItem records spawn membership and outcomes from one item
// of the conversation threadID (Rust App::observe_sidebar_agent_event items).
func (m *Model) observeSidebarAgentItem(threadID string, item *protocol.ThreadItem) {
	if m == nil || item == nil || !m.sideWorkspaceEnabled() {
		return
	}
	sender := strings.TrimSpace(threadID)
	if sender == "" {
		return
	}
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(item.Type), "_", "")) {
	case "subagentactivity":
		child := strings.TrimSpace(item.AgentThreadID)
		phase, startHint, ok := sidebarAgentPhaseForActivity(item.ActivityKind)
		if child == "" || !ok || !m.registerSidebarAgent(sender, child) {
			return
		}
		root := m.sidebarAgentRootForThread(sender)
		m.setSidebarAgentIdentity(root, child, item.AgentPath, "", "")
		m.setSidebarAgentPhase(root, child, phase, startHint)
	case "collabtoolcall", "collabagenttoolcall":
		if item.AgentsStates == nil && item.ReceiverThreadIDs == nil {
			return
		}
		tool := strings.ToLower(strings.TrimSpace(item.Tool))
		spawn := tool == "spawn_agent"
		if spawn && item.ReceiverThreadIDs != nil {
			for _, receiver := range *item.ReceiverThreadIDs {
				m.registerSidebarAgent(sender, receiver)
			}
		}
		root := m.sidebarAgentRootForThread(sender)
		if item.AgentsStates == nil || root == "" {
			return
		}
		for id, state := range *item.AgentsStates {
			child := strings.TrimSpace(id)
			if child == "" {
				continue
			}
			phase, ok := sidebarAgentPhaseForStatus(state.Status)
			if !ok || phase == sidebarAgentUnavailable {
				continue
			}
			if spawn {
				m.registerSidebarAgent(sender, child)
			}
			if !m.sidebarAgentMember(root, child) {
				continue
			}
			m.setSidebarAgentPhase(root, child, phase, spawn)
		}
	}
}

// sidebarAgentMember reports whether child belongs to the tree rooted at root.
func (m *Model) sidebarAgentMember(root string, child string) bool {
	return m.sidebarAgentTreeFor(root, false).isMember(child)
}

// observeSidebarAgentTurn records a turn boundary for a member the user is
// watching (Rust status::observe ThreadStarted/TurnStarted/TurnCompleted arms).
func (m *Model) observeSidebarAgentTurn(threadID string, phase sidebarAgentPhase, startHint bool) {
	if m == nil || !m.sideWorkspaceEnabled() {
		return
	}
	root := m.sidebarAgentRootForThread(threadID)
	if root == "" || !m.sidebarAgentMember(root, threadID) {
		return
	}
	m.setSidebarAgentPhase(root, threadID, phase, startHint)
}

// mergeSidebarAgentEntries folds one agent-reader response into the tree of
// root: descendants are registered parent-first, and known agents keep live
// phases while a restored seed may be refreshed (Rust seed_discovered_sidebar_agent).
func (m *Model) mergeSidebarAgentEntries(root string, entries []codextui.AgentThreadEntry) {
	root = strings.TrimSpace(root)
	tree := m.sidebarAgentTreeFor(root, true)
	if tree == nil {
		return
	}
	pending := make([]codextui.AgentThreadEntry, 0, len(entries))
	for _, entry := range entries {
		entry.ThreadID = strings.TrimSpace(entry.ThreadID)
		if entry.ThreadID == "" || entry.ThreadID == root || entry.IsPrimary || entry.Archived {
			continue
		}
		pending = append(pending, entry)
	}
	// Descendants arrive in arbitrary order; register each one once its parent
	// is part of the tree.
	for len(pending) > 0 {
		registered := false
		kept := pending[:0]
		for _, entry := range pending {
			parent := strings.TrimSpace(entry.ParentThreadID)
			if parent == "" {
				continue
			}
			if parent == root || tree.isMember(parent) {
				if m.registerSidebarAgent(parent, entry.ThreadID) {
					m.setSidebarAgentIdentity(root, entry.ThreadID, entry.AgentPath, entry.AgentNickname, entry.AgentRole)
					m.seedSidebarAgentDiscovery(root, entry)
				}
				registered = true
				continue
			}
			kept = append(kept, entry)
		}
		pending = kept
		if !registered {
			break
		}
	}
}

// seedSidebarAgentDiscovery applies a discovery entry's liveness unless a live
// phase already owns the agent.
func (m *Model) seedSidebarAgentDiscovery(root string, entry codextui.AgentThreadEntry) {
	tree := m.sidebarAgentTreeFor(root, false)
	if tree == nil {
		return
	}
	agent := tree.agents[strings.TrimSpace(entry.ThreadID)]
	if agent == nil || !agent.restored {
		return
	}
	phase := sidebarAgentIdle
	switch {
	case entry.IsClosed:
		phase = sidebarAgentUnavailable
	case entry.IsRunning:
		phase = sidebarAgentRunning
	}
	agent.phase = phase
	agent.restored = false
}

// sidebarAgentRows lists the chooser's Subagents section: the members of the
// pane's spawn tree, active first, then explicit outcomes, then the rest
// (Rust Overview::chooser_rows).
func (m *Model) sidebarAgentRows() []sidebarAgentRow {
	if m == nil || !m.sideWorkspaceEnabled() {
		return nil
	}
	root := strings.TrimSpace(m.sidebarChooserParent)
	if root == "" {
		root = m.sidebarParentThreadID()
	}
	tree := m.sidebarAgentTreeFor(root, false)
	if tree == nil || len(tree.order) == 0 {
		return nil
	}
	members := make([]*sidebarAgent, 0, len(tree.order))
	for _, threadID := range tree.order {
		if agent := tree.agents[threadID]; agent != nil {
			members = append(members, agent)
		}
	}
	sortSidebarAgentsByPhase(members)
	rows := make([]sidebarAgentRow, 0, len(members))
	for _, agent := range members {
		symbol, status := agent.phase.label()
		rows = append(rows, sidebarAgentRow{
			ThreadID: agent.threadID,
			Name:     sidebarAgentDisplayName(agent),
			Symbol:   symbol,
			Status:   status,
		})
	}
	return rows
}

// sortSidebarAgentsByPhase is Rust's stable sort by (!active, done).
func sortSidebarAgentsByPhase(agents []*sidebarAgent) {
	rank := map[sidebarAgentPhase]int{
		sidebarAgentStarting:    0,
		sidebarAgentRunning:     0,
		sidebarAgentWaiting:     0,
		sidebarAgentIdle:        1,
		sidebarAgentUnavailable: 1,
		sidebarAgentCompleted:   2,
		sidebarAgentStopped:     2,
		sidebarAgentFailed:      2,
		sidebarAgentClosed:      2,
	}
	// Insertion sort keeps first-seen order inside a group and avoids pulling
	// sort into this hot path.
	for i := 1; i < len(agents); i++ {
		current := agents[i]
		key := rank[current.phase]
		j := i - 1
		for j >= 0 && rank[agents[j].phase] > key {
			agents[j+1] = agents[j]
			j--
		}
		agents[j+1] = current
	}
}

// sidebarAgentDisplayName prefers the spawn path, then nickname and role, then
// the thread id (Rust chooser_rows).
func sidebarAgentDisplayName(agent *sidebarAgent) string {
	if agent == nil {
		return ""
	}
	if path := strings.TrimSpace(agent.agentPath); path != "" {
		return strings.TrimPrefix(path, "/root/")
	}
	if agent.nickname != "" || agent.role != "" {
		return codextui.FormatAgentPickerItemName(agent.nickname, agent.role, false)
	}
	return agent.threadID
}

// SidebarAgentsLoadedMsg delivers one agent-reader response for the sidebar's
// spawn tree (Rust AppEvent::DiscoverSidebarAgents).
type SidebarAgentsLoadedMsg struct {
	ParentThreadID string
	RequestID      uint64
	Entries        []codextui.AgentThreadEntry
	Err            error
}

// refreshSidebarAgentsCmd reads the pane's spawn tree in the background so the
// Subagents section fills in when the chooser opens.
func (m *Model) refreshSidebarAgentsCmd() bubbletea.Cmd {
	if m == nil || m.onReadAgents == nil {
		return nil
	}
	parent := strings.TrimSpace(m.sidebarChooserParent)
	if parent == "" {
		parent = m.sidebarParentThreadID()
	}
	if parent == "" {
		return nil
	}
	reader := m.onReadAgents
	m.sidebarAgentRequest++
	requestID := m.sidebarAgentRequest
	return func() bubbletea.Msg {
		entries, err := reader(parent)
		return SidebarAgentsLoadedMsg{ParentThreadID: parent, RequestID: requestID, Entries: entries, Err: err}
	}
}

// applySidebarAgentsLoaded merges a discovery response, dropping stale reads.
func (m *Model) applySidebarAgentsLoaded(message SidebarAgentsLoadedMsg) bubbletea.Cmd {
	if m == nil || message.RequestID != m.sidebarAgentRequest || message.Err != nil {
		return nil
	}
	m.mergeSidebarAgentEntries(message.ParentThreadID, message.Entries)
	if m.sidebarChooserOpen {
		m.clampSidebarChoice()
		m.refreshTranscript()
	}
	return nil
}

// switchToSidebarAgent opens a Subagents row's conversation, reusing the agent
// switch path the picker uses (Rust App::select_agent_thread).
func (m *Model) switchToSidebarAgent(threadID string) bubbletea.Cmd {
	if m == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	return m.applyAgentModalOption(threadID)
}
