package app

import (
	"strings"

	codextui "codex_go/internal/tui"
)

// Rust parity: codex-rs/tui/src/app/agent_navigation.rs.

type AgentNavigationTarget struct {
	ThreadID string
	AgentID  string
}

type AgentNavigationDirection string

const (
	AgentNavigationPrevious AgentNavigationDirection = "previous"
	AgentNavigationNext     AgentNavigationDirection = "next"
)

type SubAgentActivityDisplay struct {
	ThreadID      string
	AgentPath     string
	IsRunningHint bool
}

type AgentNavigationPickerItem struct {
	ThreadID    string
	Name        string
	Description string
	IsCurrent   bool
	IsPrimary   bool
	IsClosed    bool
	SearchValue string
}

type AgentNavigationState struct {
	threads map[string]codextui.AgentThreadEntry
	order   []string
}

func NewAgentNavigationState() *AgentNavigationState {
	return &AgentNavigationState{
		threads: map[string]codextui.AgentThreadEntry{},
	}
}

func (s *AgentNavigationState) ensure() {
	if s.threads == nil {
		s.threads = map[string]codextui.AgentThreadEntry{}
	}
}

func (s *AgentNavigationState) Get(threadID string) (codextui.AgentThreadEntry, bool) {
	if s == nil || s.threads == nil {
		return codextui.AgentThreadEntry{}, false
	}
	entry, ok := s.threads[threadID]
	return entry, ok
}

func (s *AgentNavigationState) IsEmpty() bool {
	return s == nil || len(s.threads) == 0
}

func (s *AgentNavigationState) Upsert(threadID string, agentNickname string, agentRole string, isClosed bool) {
	if s == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	s.ensure()
	previous, exists := s.threads[threadID]
	if !exists {
		s.order = append(s.order, threadID)
	}
	s.threads[threadID] = codextui.AgentThreadEntry{
		ThreadID:      threadID,
		AgentNickname: agentNickname,
		AgentRole:     agentRole,
		AgentPath:     previous.AgentPath,
		IsRunning:     previous.IsRunning && !isClosed,
		IsClosed:      isClosed,
		IsPrimary:     previous.IsPrimary,
	}
}

func (s *AgentNavigationState) RecordSubAgentActivity(activity SubAgentActivityDisplay) {
	if s == nil || strings.TrimSpace(activity.ThreadID) == "" {
		return
	}
	s.ensure()
	entry, exists := s.threads[activity.ThreadID]
	if !exists {
		s.order = append(s.order, activity.ThreadID)
		entry.ThreadID = activity.ThreadID
	}
	entry.AgentPath = activity.AgentPath
	entry.IsRunning = activity.IsRunningHint
	entry.IsClosed = false
	s.threads[activity.ThreadID] = entry
}

func (s *AgentNavigationState) SetRunning(threadID string, isRunning bool) {
	if s == nil || s.threads == nil {
		return
	}
	entry, ok := s.threads[threadID]
	if !ok {
		return
	}
	entry.IsRunning = isRunning
	s.threads[threadID] = entry
}

func (s *AgentNavigationState) SetAgentPath(threadID string, agentPath string) {
	if s == nil || s.threads == nil {
		return
	}
	entry, ok := s.threads[threadID]
	if !ok {
		return
	}
	entry.AgentPath = agentPath
	s.threads[threadID] = entry
}

func (s *AgentNavigationState) MarkClosed(threadID string) {
	if s == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	s.ensure()
	entry, ok := s.threads[threadID]
	if !ok {
		s.Upsert(threadID, "", "", true)
		return
	}
	entry.IsClosed = true
	entry.IsRunning = false
	s.threads[threadID] = entry
}

func (s *AgentNavigationState) Clear() {
	if s == nil {
		return
	}
	s.threads = map[string]codextui.AgentThreadEntry{}
	s.order = nil
}

func (s *AgentNavigationState) Remove(threadID string) {
	if s == nil || s.threads == nil {
		return
	}
	delete(s.threads, threadID)
	out := s.order[:0]
	for _, candidate := range s.order {
		if candidate != threadID {
			out = append(out, candidate)
		}
	}
	s.order = out
}

func (s *AgentNavigationState) HasNonPrimaryThread(primaryThreadID string) bool {
	if s == nil || s.threads == nil {
		return false
	}
	for threadID := range s.threads {
		if primaryThreadID == "" || threadID != primaryThreadID {
			return true
		}
	}
	return false
}

func (s *AgentNavigationState) OrderedThreads() []codextui.AgentThreadEntry {
	if s == nil || s.threads == nil {
		return nil
	}
	out := make([]codextui.AgentThreadEntry, 0, len(s.order))
	for _, threadID := range s.order {
		if entry, ok := s.threads[threadID]; ok {
			out = append(out, entry)
		}
	}
	return out
}

func (s *AgentNavigationState) OrderedThreadIDs() []string {
	return s.TrackedThreadIDs()
}

func (s *AgentNavigationState) PickerItems(currentThreadID string, primaryThreadID string) ([]AgentNavigationPickerItem, int) {
	ordered := s.OrderedThreads()
	items := make([]AgentNavigationPickerItem, 0, len(ordered))
	selected := -1
	for index, entry := range ordered {
		isPrimary := primaryThreadID != "" && entry.ThreadID == primaryThreadID
		name := codextui.FormatAgentPickerItemName(entry.AgentNickname, entry.AgentRole, isPrimary)
		item := AgentNavigationPickerItem{
			ThreadID:    entry.ThreadID,
			Name:        name,
			Description: entry.ThreadID,
			IsCurrent:   currentThreadID != "" && entry.ThreadID == currentThreadID,
			IsPrimary:   isPrimary,
			IsClosed:    entry.IsClosed,
			SearchValue: strings.TrimSpace(name + " " + entry.ThreadID),
		}
		if item.IsCurrent {
			selected = index
		}
		items = append(items, item)
	}
	return items, selected
}

func (s *AgentNavigationState) OrderedPathBackedSubagentThreads(primaryThreadID string) []codextui.AgentThreadEntry {
	ordered := s.OrderedThreads()
	out := make([]codextui.AgentThreadEntry, 0, len(ordered))
	for _, entry := range ordered {
		if entry.ThreadID == primaryThreadID {
			continue
		}
		if strings.TrimSpace(entry.AgentPath) == "" {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (s *AgentNavigationState) TrackedThreadIDs() []string {
	ordered := s.OrderedThreads()
	out := make([]string, 0, len(ordered))
	for _, entry := range ordered {
		out = append(out, entry.ThreadID)
	}
	return out
}

func (s *AgentNavigationState) AdjacentThreadID(currentDisplayedThreadID string, direction AgentNavigationDirection) (string, bool) {
	ordered := s.TrackedThreadIDs()
	if len(ordered) < 2 || strings.TrimSpace(currentDisplayedThreadID) == "" {
		return "", false
	}
	currentIndex := -1
	for index, threadID := range ordered {
		if threadID == currentDisplayedThreadID {
			currentIndex = index
			break
		}
	}
	if currentIndex < 0 {
		return "", false
	}
	nextIndex := currentIndex
	switch direction {
	case AgentNavigationPrevious:
		nextIndex--
		if nextIndex < 0 {
			nextIndex = len(ordered) - 1
		}
	default:
		nextIndex = (nextIndex + 1) % len(ordered)
	}
	return ordered[nextIndex], true
}

func (s *AgentNavigationState) ActiveAgentLabel(currentDisplayedThreadID string, primaryThreadID string) (string, bool) {
	if s == nil || len(s.threads) <= 1 || strings.TrimSpace(currentDisplayedThreadID) == "" {
		return "", false
	}
	entry, ok := s.threads[currentDisplayedThreadID]
	isPrimary := primaryThreadID != "" && currentDisplayedThreadID == primaryThreadID
	if ok && !isPrimary {
		if agentPath := strings.TrimSpace(entry.AgentPath); agentPath != "" {
			return "`" + agentPath + "`", true
		}
	}
	if !ok {
		return codextui.FormatAgentPickerItemName("", "", isPrimary), true
	}
	return codextui.FormatAgentPickerItemName(entry.AgentNickname, entry.AgentRole, isPrimary), true
}

func AgentNavigationPickerSubtitle() string {
	return "Select an agent to watch. Alt+Left previous, Alt+Right next."
}
