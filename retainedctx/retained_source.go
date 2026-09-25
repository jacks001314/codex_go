package retainedctx

import "github.com/google/uuid"

// ResponseItemID mirrors `codex_protocol::ResponseItemId`: an opaque
// `<prefix>_<uuid>` identifier serialized transparently as a string.
type ResponseItemID string

// NewResponseItemID mints a fresh prefixed identifier, matching
// `ResponseItemId::new`.
func NewResponseItemID(prefix string) ResponseItemID {
	id, err := uuid.NewV7()
	if err != nil {
		return ResponseItemID(prefix + "_" + uuid.NewString())
	}
	return ResponseItemID(prefix + "_" + id.String())
}

// String returns the underlying opaque identifier.
func (id ResponseItemID) String() string { return string(id) }

// RetainedSourceRole names the family an original message belongs to.
type RetainedSourceRole string

const (
	RetainedSourceRoleUser      RetainedSourceRole = "user"
	RetainedSourceRoleAssistant RetainedSourceRole = "assistant"
)

// RetainedSourceID identifies the original message, including its role and turn
// namespace.
type RetainedSourceID struct {
	MessageID string             `json:"message_id"`
	TurnID    string             `json:"turn_id"`
	Role      RetainedSourceRole `json:"role"`
}

// RetainedSource is a host-observed version of original evidence, never a
// fingerprint of rendered prompt text.
type RetainedSource struct {
	ID RetainedSourceID `json:"id"`
	// Revision is a fresh opaque version that prevents reuse across history
	// resets and forks.
	Revision ResponseItemID `json:"revision"`
	Complete bool           `json:"complete"`
}

// source reproduces `Ordered::source`: evidence without a message id or a
// recorded revision cannot establish delivery.
func (e *orderedUserMessage) source(role RetainedSourceRole) *RetainedSource {
	if e.Value.MessageID == nil || e.Revision == nil {
		return nil
	}
	return &RetainedSource{
		ID: RetainedSourceID{
			MessageID: *e.Value.MessageID,
			TurnID:    e.Value.TurnID,
			Role:      role,
		},
		Revision: *e.Revision,
		Complete: e.Value.Complete,
	}
}

// RestoreSourceRevision restores a host-captured version while replaying its
// original message. Live recording must mint a new revision for changed
// evidence instead.
func (c *RetainedContext) RestoreSourceRevision(source *RetainedSource) bool {
	if source == nil {
		return false
	}
	entries := &c.userMessages
	if source.ID.Role == RetainedSourceRoleAssistant {
		entries = &c.assistantMessages
	}
	for i := range *entries {
		entry := &(*entries)[i]
		if entry.Value.MessageID == nil || *entry.Value.MessageID != source.ID.MessageID {
			continue
		}
		if entry.Value.TurnID != source.ID.TurnID || entry.Value.Complete != source.Complete {
			continue
		}
		revision := source.Revision
		entry.Revision = &revision
		return true
	}
	return false
}

// Source returns the host-observed version of the given retained entry, or nil
// when a legacy revision or message id cannot establish delivery.
func (c *RetainedContext) Source(entry RetainedContextEntry) *RetainedSource {
	var message *RetainedUserMessage
	var role RetainedSourceRole
	var entries []orderedUserMessage
	switch {
	case entry.UserMessage != nil:
		message = entry.UserMessage
		role = RetainedSourceRoleUser
		entries = c.userMessages
	case entry.AssistantMessage != nil:
		message = entry.AssistantMessage
		role = RetainedSourceRoleAssistant
		entries = c.assistantMessages
	default:
		return nil
	}
	for i := range entries {
		if &entries[i].Value == message {
			return entries[i].source(role)
		}
	}
	return nil
}
