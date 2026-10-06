package appserver

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Thread attachments (Rust #43949/#44330/#44564): bounded, independently
// persisted records owned by a thread. The Go port stores them through the
// thread record's `Extra` metadata map instead of a dedicated SQLite table,
// because the Rust state migrations that add `thread_attachments` are newer
// than the frozen migration inventory the parity suite pins. The wire contract
// matches Rust exactly.

const (
	maxThreadAttachmentPayloadBytes     = 64 * 1024
	maxThreadAttachmentTypeBytes        = 256
	maxThreadAttachmentIdentityKeyBytes = 256
	maxThreadAttachmentListPageSize     = 100
	maxThreadAttachmentsPerThread       = 100
	// defaultThreadAttachmentListLimit mirrors Rust THREAD_LIST_DEFAULT_LIMIT.
	defaultThreadAttachmentListLimit = 25

	threadAttachmentsExtraKey = "thread_attachments"
)

// ThreadAttachment is an independently persisted attachment associated with a
// thread.
type ThreadAttachment struct {
	ID             string          `json:"id"`
	AttachmentType string          `json:"attachmentType"`
	IdentityKey    string          `json:"identityKey"`
	Payload        json.RawMessage `json:"payload"`
	CreatedAt      int64           `json:"createdAt"`
}

type ThreadAttachmentAddOutcome string

const (
	ThreadAttachmentAddOutcomeCreated  ThreadAttachmentAddOutcome = "created"
	ThreadAttachmentAddOutcomeExisting ThreadAttachmentAddOutcome = "existing"
)

type ThreadAttachmentOperation string

const (
	ThreadAttachmentOperationCreated ThreadAttachmentOperation = "created"
	ThreadAttachmentOperationDeleted ThreadAttachmentOperation = "deleted"
)

type ThreadAttachmentAddParams struct {
	ThreadID       string          `json:"threadId"`
	AttachmentType string          `json:"attachmentType"`
	IdentityKey    string          `json:"identityKey"`
	Payload        json.RawMessage `json:"payload"`
}

func (p *ThreadAttachmentAddParams) Validate() error {
	if err := validateThreadAttachmentThreadID(p.threadID()); err != nil {
		return err
	}
	return validateThreadAttachmentIdentity(p.attachmentType(), p.identityKey())
}

type ThreadAttachmentAddResponse struct {
	Outcome    ThreadAttachmentAddOutcome `json:"outcome"`
	Attachment ThreadAttachment           `json:"attachment"`
}

type ThreadAttachmentListParams struct {
	ThreadID string  `json:"threadId"`
	Cursor   *string `json:"cursor,omitempty"`
	Limit    *uint32 `json:"limit,omitempty"`
}

func (p *ThreadAttachmentListParams) Validate() error {
	return validateThreadAttachmentThreadID(p.threadID())
}

type ThreadAttachmentListResponse struct {
	Data       []ThreadAttachment `json:"data"`
	NextCursor *string            `json:"nextCursor"`
}

// ThreadAttachmentOwnerListParams mirrors Rust ThreadAttachmentOwnerListParams:
// reverse lookup by exact attachment identity within this server's store.
type ThreadAttachmentOwnerListParams struct {
	AttachmentType string `json:"attachmentType"`
	IdentityKey    string `json:"identityKey"`
	// Archived omitted or null returns all matches; false returns non-archived
	// threads only, true returns archived threads only.
	Archived *bool   `json:"archived,omitempty"`
	Cursor   *string `json:"cursor,omitempty"`
	Limit    *uint32 `json:"limit,omitempty"`
}

func (p *ThreadAttachmentOwnerListParams) Validate() error {
	if p == nil {
		return validateThreadAttachmentIdentity("", "")
	}
	return validateThreadAttachmentIdentity(p.AttachmentType, p.IdentityKey)
}

// ThreadAttachmentOwner mirrors Rust ThreadAttachmentOwner.
type ThreadAttachmentOwner struct {
	ThreadID string `json:"threadId"`
	// Archived is the owning thread's archive state, not its run state.
	Archived bool `json:"archived"`
}

// ThreadAttachmentOwnerListResponse mirrors Rust ThreadAttachmentOwnerListResponse.
type ThreadAttachmentOwnerListResponse struct {
	Data       []ThreadAttachmentOwner `json:"data"`
	NextCursor *string                 `json:"nextCursor"`
}

// threadAttachmentOwnerCursor mirrors Rust AttachmentThreadsCursor. It is opaque
// to clients and round-tripped verbatim; the field names stay snake_case to
// match the Rust serialization.
type threadAttachmentOwnerCursor struct {
	AttachmentType string `json:"attachment_type"`
	IdentityKey    string `json:"identity_key"`
	Archived       *bool  `json:"archived"`
	ThreadID       string `json:"thread_id"`
}

type ThreadAttachmentRemoveParams struct {
	ThreadID       string `json:"threadId"`
	AttachmentType string `json:"attachmentType"`
	IdentityKey    string `json:"identityKey"`
}

func (p *ThreadAttachmentRemoveParams) Validate() error {
	if err := validateThreadAttachmentThreadID(p.threadID()); err != nil {
		return err
	}
	return validateThreadAttachmentIdentity(p.attachmentType(), p.identityKey())
}

type ThreadAttachmentRemoveResponse struct{}

type ThreadAttachmentUpdatedNotification struct {
	ThreadID       string                    `json:"threadId"`
	AttachmentType string                    `json:"attachmentType"`
	IdentityKey    string                    `json:"identityKey"`
	AttachmentID   string                    `json:"attachmentId"`
	Operation      ThreadAttachmentOperation `json:"operation"`
}

func (p *ThreadAttachmentAddParams) threadID() string {
	if p == nil {
		return ""
	}
	return p.ThreadID
}

func (p *ThreadAttachmentAddParams) attachmentType() string {
	if p == nil {
		return ""
	}
	return p.AttachmentType
}

func (p *ThreadAttachmentAddParams) identityKey() string {
	if p == nil {
		return ""
	}
	return p.IdentityKey
}

func (p *ThreadAttachmentListParams) threadID() string {
	if p == nil {
		return ""
	}
	return p.ThreadID
}

func (p *ThreadAttachmentRemoveParams) threadID() string {
	if p == nil {
		return ""
	}
	return p.ThreadID
}

func (p *ThreadAttachmentRemoveParams) attachmentType() string {
	if p == nil {
		return ""
	}
	return p.AttachmentType
}

func (p *ThreadAttachmentRemoveParams) identityKey() string {
	if p == nil {
		return ""
	}
	return p.IdentityKey
}

func validateThreadAttachmentThreadID(threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return invalidParams("threadId is required")
	}
	if !validUUIDString(threadID) {
		return invalidParams("invalid thread id: invalid UUID")
	}
	return nil
}

// validateThreadAttachmentIdentity mirrors Rust validate_attachment_identity.
func validateThreadAttachmentIdentity(attachmentType string, identityKey string) error {
	if strings.TrimSpace(attachmentType) == "" {
		return invalidParams("attachmentType must not be empty")
	}
	if len(attachmentType) > maxThreadAttachmentTypeBytes {
		return invalidParams(fmt.Sprintf("attachmentType must not exceed %d bytes", maxThreadAttachmentTypeBytes))
	}
	if strings.TrimSpace(identityKey) == "" {
		return invalidParams("identityKey must not be empty")
	}
	if len(identityKey) > maxThreadAttachmentIdentityKeyBytes {
		return invalidParams(fmt.Sprintf("identityKey must not exceed %d bytes", maxThreadAttachmentIdentityKeyBytes))
	}
	return nil
}

// threadAttachmentsFromExtra decodes the persisted attachment list, tolerating
// both the in-memory typed slice and the JSON-decoded `[]any` shape produced
// after a metadata round trip.
func threadAttachmentsFromExtra(extra map[string]any) []ThreadAttachment {
	if extra == nil {
		return nil
	}
	raw, ok := extra[threadAttachmentsExtraKey]
	if !ok || raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var attachments []ThreadAttachment
	if err := json.Unmarshal(data, &attachments); err != nil {
		return nil
	}
	return attachments
}

func cloneExtraMap(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	clone := make(map[string]any, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}
