package turn

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"codex_go/model"
)

// maxResultBodyBytes bounds the Apps result bodies one turn retains, matching the
// executed-tool-call observation cap they accompany (Rust
// MAX_TURN_RESULT_BODY_BYTES, #62392).
const maxResultBodyBytes = 1024 * 1024

// ResultBodyTurn is the per-turn, bounded store of Apps connector result bodies
// (Rust ResultBodyTurn, #62392). Bodies are private to the turn that captured
// them: history keeps only the local result IDs the executed-tool rows carry, so
// a later turn, another account, or another process never sees the bytes.
type ResultBodyTurn struct {
	turnID string
	// owner names the credential instance that may read these bodies: the auth
	// mode plus the account the turn started with. A request served by a
	// different owner never attaches them.
	owner string

	mu    sync.Mutex
	byID  map[string]*model.ToolResultBody
	bytes int
}

// NewResultBodyTurn starts the bounded store for one turn.
func NewResultBodyTurn(turnID string, owner string) *ResultBodyTurn {
	return &ResultBodyTurn{turnID: turnID, owner: owner, byID: map[string]*model.ToolResultBody{}}
}

// Owner reports the credential instance the bodies belong to.
func (t *ResultBodyTurn) Owner() string {
	if t == nil {
		return ""
	}
	return t.owner
}

// TurnID reports the turn the bodies belong to.
func (t *ResultBodyTurn) TurnID() string {
	if t == nil {
		return ""
	}
	return t.turnID
}

// Record projects one completed MCP result and retains it under a fresh local
// ID. A result the receiver cannot replay, a full store, or an exhausted byte
// budget simply yields no ID, so the caller attaches no observation.
func (t *ResultBodyTurn) Record(source model.ToolResultBodySource) (string, bool) {
	return t.RecordNested(source, "")
}

// RecordNested retains one completed MCP result made inside a Code Mode cell and
// pairs it with that child call's ID, so a sampling request can send
// `tool_result` and `tool_result_call_id` together (Rust #62423).
func (t *ResultBodyTurn) RecordNested(source model.ToolResultBodySource, nestedCallID string) (string, bool) {
	if t == nil {
		return "", false
	}
	body := model.ProjectToolResultBody(source)
	if body == nil {
		return "", false
	}
	body = body.WithNestedCallID(nestedCallID)
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.byID) >= maxPendingExecutedToolCalls || body.CompactBytes() > maxResultBodyBytes-t.bytes {
		return "", false
	}
	id := newResultBodyID()
	t.bytes += body.CompactBytes()
	t.byID[id] = body
	return id, true
}

// Discard releases a body whose executed-tool row is no longer part of the
// turn's request input.
func (t *ResultBodyTurn) Discard(id string) {
	if t == nil || id == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if body, ok := t.byID[id]; ok {
		t.bytes -= body.CompactBytes()
		delete(t.byID, id)
	}
}

// Attachments selects the retained bodies for the executed-tool rows of this
// request input. Indexes address the same slice the caller will encode, exactly
// like Rust ResultBodyTurn::for_input.
func (t *ResultBodyTurn) Attachments(input []any) []model.ToolResultAttachment {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	empty := len(t.byID) == 0
	t.mu.Unlock()
	if empty {
		return nil
	}
	var attachments []model.ToolResultAttachment
	for itemIndex, item := range input {
		calls := model.RequestInputExecutedToolCalls(item)
		for callIndex, call := range calls {
			body := t.body(call.ToolResultID())
			if body == nil {
				continue
			}
			attachments = append(attachments, model.ToolResultAttachment{
				ItemIndex: itemIndex,
				CallIndex: callIndex,
				Body:      body,
			})
		}
	}
	return attachments
}

func (t *ResultBodyTurn) body(id string) *model.ToolResultBody {
	if id == "" {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.byID[id]
}

// newResultBodyID returns the opaque local handle a result ID carries. The value
// is a random token rather than a content hash so it cannot be correlated with
// the body it names.
func newResultBodyID() string {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(buffer[:])
}
