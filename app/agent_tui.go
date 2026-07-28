package app

import (
	"errors"
	"sort"
	"strings"

	"codex_go/session"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

type interactiveAgentStoreFactory func() *session.Store

func interactiveLocalAgentCallbacks(factory interactiveAgentStoreFactory) (codextea.AgentThreadReaderFunc, codextea.AgentThreadSwitchFunc) {
	store := func() *session.Store {
		if factory != nil {
			return factory()
		}
		return newSessionStore()
	}
	read := func(currentThreadID string) ([]codextui.AgentThreadEntry, error) {
		return interactiveLocalAgentThreadEntries(store(), currentThreadID)
	}
	switchThread := func(threadID string) (codextea.AgentThreadSwitchResponse, error) {
		return interactiveLocalSwitchAgentThread(store(), threadID)
	}
	return read, switchThread
}

func interactiveLocalAgentThreadEntries(store *session.Store, currentThreadID string) ([]codextui.AgentThreadEntry, error) {
	currentThreadID = strings.TrimSpace(currentThreadID)
	if currentThreadID == "" {
		return nil, nil
	}
	if store == nil {
		return nil, errors.New("agent thread store is unavailable")
	}
	records, err := store.AllRecords()
	if err != nil {
		return nil, err
	}
	recordsByID := localAgentRecordsByID(records)
	primaryThreadID := localAgentPrimaryThreadID(recordsByID, currentThreadID)
	primary, hasPrimary := recordsByID[session.ThreadID(primaryThreadID)]
	entries := make([]codextui.AgentThreadEntry, 0, len(records))
	if hasPrimary {
		entries = append(entries, localAgentEntryFromRecord(&primary, primaryThreadID))
	} else {
		entries = append(entries, codextui.AgentThreadEntry{ThreadID: primaryThreadID, IsPrimary: true})
	}

	descendants := make([]session.Record, 0, len(records))
	for i := range records {
		record := records[i]
		if string(record.ID) == primaryThreadID || !localAgentRecordIsSubagent(&record) {
			continue
		}
		if localAgentRecordDescendsFrom(&record, session.ThreadID(primaryThreadID), recordsByID) {
			descendants = append(descendants, record)
		}
	}
	sort.SliceStable(descendants, func(i int, j int) bool {
		left := descendants[i].CreatedAt
		right := descendants[j].CreatedAt
		if left.Equal(right) {
			return descendants[i].ID < descendants[j].ID
		}
		return left.Before(right)
	})
	for i := range descendants {
		entries = append(entries, localAgentEntryFromRecord(&descendants[i], primaryThreadID))
	}
	return entries, nil
}

func interactiveLocalSwitchAgentThread(store *session.Store, threadID string) (codextea.AgentThreadSwitchResponse, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return codextea.AgentThreadSwitchResponse{}, errors.New("agent switch requires a thread id")
	}
	if store == nil {
		return codextea.AgentThreadSwitchResponse{}, errors.New("agent thread store is unavailable")
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		return codextea.AgentThreadSwitchResponse{}, err
	}
	records, err := store.AllRecords()
	if err != nil {
		return codextea.AgentThreadSwitchResponse{}, err
	}
	primaryThreadID := localAgentPrimaryThreadID(localAgentRecordsByID(records), threadID)
	status := "idle"
	if localAgentRecordIsRunning(record) {
		status = "running"
	}
	return codextea.AgentThreadSwitchResponse{
		Entry:    localAgentEntryFromRecord(record, primaryThreadID),
		Messages: interactiveSessionMessagesFromRecord(record),
		Status:   status,
	}, nil
}

func localAgentRecordsByID(records []session.Record) map[session.ThreadID]session.Record {
	byID := make(map[session.ThreadID]session.Record, len(records))
	for i := range records {
		if records[i].ID != "" {
			byID[records[i].ID] = records[i]
		}
	}
	return byID
}

func localAgentPrimaryThreadID(records map[session.ThreadID]session.Record, currentThreadID string) string {
	current := session.ThreadID(strings.TrimSpace(currentThreadID))
	primary := current
	seen := map[session.ThreadID]bool{}
	for current != "" && !seen[current] {
		seen[current] = true
		record, ok := records[current]
		if !ok || !localAgentRecordIsSubagent(&record) || record.ParentThreadID == "" {
			break
		}
		primary = record.ParentThreadID
		current = record.ParentThreadID
	}
	return string(primary)
}

func localAgentRecordIsSubagent(record *session.Record) bool {
	if record == nil || record.ParentThreadID == "" {
		return false
	}
	source := strings.ToLower(strings.TrimSpace(record.Metadata.Source))
	threadSource := strings.ToLower(strings.TrimSpace(record.Metadata.ThreadSource))
	sideConversation, _ := record.Metadata.Extra["tui_side_conversation"].(bool)
	return strings.Contains(source, "subagent") ||
		strings.Contains(threadSource, "subagent") ||
		sideConversation ||
		strings.TrimSpace(record.Metadata.AgentPath) != "" ||
		strings.TrimSpace(record.Metadata.AgentNickname) != "" ||
		strings.TrimSpace(record.Metadata.AgentRole) != ""
}

func localAgentRecordDescendsFrom(record *session.Record, ancestor session.ThreadID, records map[session.ThreadID]session.Record) bool {
	if record == nil || ancestor == "" {
		return false
	}
	seen := map[session.ThreadID]bool{}
	for parent := record.ParentThreadID; parent != "" && !seen[parent]; {
		if parent == ancestor {
			return true
		}
		seen[parent] = true
		next, ok := records[parent]
		if !ok {
			return false
		}
		parent = next.ParentThreadID
	}
	return false
}

func localAgentEntryFromRecord(record *session.Record, primaryThreadID string) codextui.AgentThreadEntry {
	if record == nil {
		return codextui.AgentThreadEntry{}
	}
	closed := record.Archived
	return codextui.AgentThreadEntry{
		ThreadID:      strings.TrimSpace(string(record.ID)),
		AgentNickname: strings.TrimSpace(record.Metadata.AgentNickname),
		AgentRole:     strings.TrimSpace(record.Metadata.AgentRole),
		AgentPath:     strings.TrimSpace(record.Metadata.AgentPath),
		IsPrimary:     strings.TrimSpace(string(record.ID)) == strings.TrimSpace(primaryThreadID),
		IsRunning:     !closed && localAgentRecordIsRunning(record),
		IsClosed:      closed,
	}
}

func localAgentRecordIsRunning(record *session.Record) bool {
	if record == nil || len(record.Metadata.RolloutTurns) == 0 {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(record.Metadata.RolloutTurns[len(record.Metadata.RolloutTurns)-1].Status))
	return status == "inprogress" || status == "in_progress" || status == "running"
}
