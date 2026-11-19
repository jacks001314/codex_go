package rollout

import (
	"strings"

	"codex_go/session"
)

// RecordFromThreadItem projects a rollout listing item into the lightweight
// session record used by picker and history listings. Rust's thread store
// serves those lists from indexed metadata (`ThreadItem`) and never parses the
// whole rollout, so Go's list paths must not call RecordFromPath either.
func RecordFromThreadItem(item ThreadItem) *session.Record {
	threadID := strings.TrimSpace(item.ThreadID)
	if threadID == "" {
		if fromName, ok := ThreadIDFromFilename(item.Path); ok {
			threadID = fromName
		}
	}
	if threadID == "" {
		return nil
	}
	createdAt := item.CreatedAt
	if createdAt.IsZero() {
		createdAt = item.UpdatedAt
	}
	updatedAt := item.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	recencyAt := item.RecencyAt
	if recencyAt.IsZero() {
		recencyAt = updatedAt
	}
	record := &session.Record{
		ID:           session.ThreadID(threadID),
		SessionID:    threadID,
		ForkedFromID: session.ThreadID(strings.TrimSpace(item.ForkedFromID)),
		Preview:      item.Preview,
		Archived:     item.Archived,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
		RecencyAt:    recencyAt,
		FromRollout:  true,
		Metadata: session.Metadata{
			CWD:           item.CWD,
			Model:         item.Model,
			ModelProvider: item.ModelProvider,
			Source:        item.Source,
			HistoryMode:   item.HistoryMode,
			CLIVersion:    item.CLIVersion,
			AgentNickname: item.AgentNickname,
			AgentRole:     item.AgentRole,
			Extra: map[string]any{
				"rollout_path":       item.Path,
				"first_user_message": item.FirstUserMessage,
			},
		},
	}
	record.ParentThreadID = session.ThreadID(strings.TrimSpace(item.ParentThreadID))
	if item.AttachedThread && record.ForkedFromID != "" {
		record.AttachedThreadParentID = record.ForkedFromID
	}
	return record
}
