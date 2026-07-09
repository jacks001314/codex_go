package app

import (
	"encoding/json"
	"strings"

	"codex_go/internal/appserver"
)

// Rust parity subset: codex-rs/tui/src/app/thread_events.rs.

const (
	ThreadBufferedEventHistoryEntryResponse = "history_entry_response"
	ThreadBufferedEventFeedbackSubmission   = "feedback_submission"
)

type ThreadEvent struct {
	ThreadID string
	Kind     string
}

type ThreadEventAttachment string

const (
	ThreadEventAttachmentLive       ThreadEventAttachment = "live"
	ThreadEventAttachmentReplayOnly ThreadEventAttachment = "replay_only"
)

type ThreadEventChannel struct {
	Store      *ThreadEventStore
	attachment ThreadEventAttachment
}

func ThreadEventSurvivesSessionRefresh(event ThreadBufferedEvent) bool {
	if event.Type == ThreadBufferedEventRequest {
		return true
	}
	if event.Type == ThreadBufferedEventFeedbackSubmission {
		return true
	}
	if event.Type != ThreadBufferedEventNotification || event.Notification == nil {
		return false
	}
	switch event.Notification.Name {
	case ServerNotificationHookStarted,
		ServerNotificationHookCompleted,
		ServerNotificationMcpServerStatusUpdated:
		return true
	default:
		return false
	}
}

func NewThreadEventChannel(capacity int) *ThreadEventChannel {
	return &ThreadEventChannel{
		Store:      NewThreadEventStore(capacity),
		attachment: ThreadEventAttachmentLive,
	}
}

func NewThreadEventChannelWithSession(capacity int, session ThreadSessionState, turns []appserver.Turn) *ThreadEventChannel {
	return &ThreadEventChannel{
		Store:      NewThreadEventStoreWithSession(capacity, session, turns),
		attachment: ThreadEventAttachmentLive,
	}
}

func (c *ThreadEventChannel) MarkReplayOnly() {
	if c == nil {
		return
	}
	c.attachment = ThreadEventAttachmentReplayOnly
}

func (c *ThreadEventChannel) Attachment() ThreadEventAttachment {
	if c == nil || c.attachment == "" {
		return ThreadEventAttachmentLive
	}
	return c.attachment
}

func (s *ThreadEventStore) FileChangeChanges(turnID string, itemID string) ([]appserver.FileUpdateChange, bool) {
	if s == nil || strings.TrimSpace(itemID) == "" {
		return nil, false
	}
	for i := len(s.Buffer) - 1; i >= 0; i-- {
		event := s.Buffer[i]
		if event.Type != ThreadBufferedEventNotification || event.Notification == nil {
			continue
		}
		notification := event.Notification
		if notification.Name != ServerNotificationItemStarted && notification.Name != ServerNotificationItemCompleted {
			continue
		}
		target := EventTargetFromServerEvent(*notification)
		if !turnIDMatches(turnID, target.TurnID) {
			continue
		}
		if changes, ok := fileChangeItemChanges(notification.Item, itemID); ok {
			return changes, true
		}
	}
	for turnIndex := len(s.Turns) - 1; turnIndex >= 0; turnIndex-- {
		turn := s.Turns[turnIndex]
		if !turnIDMatches(turnID, turn.ID) {
			continue
		}
		for itemIndex := len(turn.Items) - 1; itemIndex >= 0; itemIndex-- {
			if changes, ok := fileChangeItemChanges(&turn.Items[itemIndex], itemID); ok {
				return changes, true
			}
		}
	}
	return nil, false
}

func turnIDMatches(requestTurnID string, candidateTurnID string) bool {
	requestTurnID = strings.TrimSpace(requestTurnID)
	return requestTurnID == "" || requestTurnID == strings.TrimSpace(candidateTurnID)
}

func fileChangeItemChanges(item *appserver.ThreadItem, itemID string) ([]appserver.FileUpdateChange, bool) {
	if item == nil || strings.TrimSpace(item.ID) != strings.TrimSpace(itemID) {
		return nil, false
	}
	changes := FileUpdateChangesFromAny(firstFileChangeDataValue(item.Data, "changes", "fileChanges", "file_changes"))
	if len(changes) == 0 && !threadItemLooksLikeFileChange(item) {
		return nil, false
	}
	return changes, true
}

func FileUpdateChangesFromAny(value any) []appserver.FileUpdateChange {
	switch typed := value.(type) {
	case nil:
		return nil
	case []appserver.FileUpdateChange:
		return append([]appserver.FileUpdateChange(nil), typed...)
	case []*appserver.FileUpdateChange:
		out := make([]appserver.FileUpdateChange, 0, len(typed))
		for _, change := range typed {
			if change != nil {
				out = append(out, *change)
			}
		}
		return out
	case []map[string]any:
		out := make([]appserver.FileUpdateChange, 0, len(typed))
		for _, change := range typed {
			out = append(out, fileUpdateChangeFromMap(change))
		}
		return out
	case []any:
		out := make([]appserver.FileUpdateChange, 0, len(typed))
		for _, change := range typed {
			if parsed, ok := fileUpdateChangeFromAny(change); ok {
				out = append(out, parsed)
			}
		}
		return out
	default:
		if parsed, ok := fileUpdateChangeFromAny(value); ok {
			return []appserver.FileUpdateChange{parsed}
		}
		return nil
	}
}

func fileUpdateChangeFromAny(value any) (appserver.FileUpdateChange, bool) {
	switch typed := value.(type) {
	case appserver.FileUpdateChange:
		return typed, true
	case *appserver.FileUpdateChange:
		if typed == nil {
			return appserver.FileUpdateChange{}, false
		}
		return *typed, true
	case map[string]any:
		return fileUpdateChangeFromMap(typed), true
	case json.RawMessage:
		var decoded map[string]any
		if err := json.Unmarshal(typed, &decoded); err == nil {
			return fileUpdateChangeFromMap(decoded), true
		}
	case []byte:
		var decoded map[string]any
		if err := json.Unmarshal(typed, &decoded); err == nil {
			return fileUpdateChangeFromMap(decoded), true
		}
	default:
		data, err := json.Marshal(value)
		if err == nil {
			var decoded map[string]any
			if err := json.Unmarshal(data, &decoded); err == nil && len(decoded) > 0 {
				return fileUpdateChangeFromMap(decoded), true
			}
		}
	}
	return appserver.FileUpdateChange{}, false
}

func fileUpdateChangeFromMap(change map[string]any) appserver.FileUpdateChange {
	return appserver.FileUpdateChange{
		Path: fileChangeStringFromAny(firstMapValue(change, "path")),
		Kind: fileUpdateChangeKindFromAny(firstMapValue(change, "kind", "type")),
		Diff: fileChangeStringFromAny(firstMapValue(change, "diff")),
	}
}

func fileUpdateChangeKindFromAny(value any) appserver.PatchChangeKind {
	switch typed := value.(type) {
	case appserver.PatchChangeKind:
		return typed
	case *appserver.PatchChangeKind:
		if typed == nil {
			return appserver.PatchChangeKind{}
		}
		return *typed
	case string:
		return appserver.PatchChangeKind{Type: strings.TrimSpace(typed)}
	case map[string]any:
		kind := appserver.PatchChangeKind{Type: fileChangeStringFromAny(firstMapValue(typed, "type"))}
		if movePath := fileChangeStringFromAny(firstMapValue(typed, "move_path", "movePath")); strings.TrimSpace(movePath) != "" {
			kind.MovePath = &movePath
		}
		return kind
	default:
		data, err := json.Marshal(value)
		if err == nil {
			var decoded map[string]any
			if err := json.Unmarshal(data, &decoded); err == nil {
				return fileUpdateChangeKindFromAny(decoded)
			}
		}
	}
	return appserver.PatchChangeKind{}
}

func threadItemLooksLikeFileChange(item *appserver.ThreadItem) bool {
	if item == nil {
		return false
	}
	itemType := strings.TrimSpace(item.Type)
	if itemType == "fileChange" || itemType == "file_change" {
		return true
	}
	if marker, ok := item.Data["fileChange"].(bool); ok && marker {
		return true
	}
	if marker, ok := item.Data["file_change"].(bool); ok && marker {
		return true
	}
	return strings.TrimSpace(item.Name) == "apply_patch" && itemType == "custom_tool_call"
}

func firstFileChangeDataValue(data map[string]any, keys ...string) any {
	if data == nil {
		return nil
	}
	return firstMapValue(data, keys...)
}

func firstMapValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func fileChangeStringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	case json.RawMessage:
		var decoded string
		if err := json.Unmarshal(typed, &decoded); err == nil {
			return strings.TrimSpace(decoded)
		}
	}
	return ""
}
