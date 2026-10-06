package appserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"codex_go/session"
)

// Thread attachment RPCs (Rust app-server
// request_processors/thread_attachments.rs). Adds are idempotent on
// (attachmentType, identityKey); removes of a missing identity succeed
// without a notification. The bare Router owns the persisted mutations so both
// the app-server runtime and direct router users share one implementation.

func isThreadAttachmentMethod(method Method) bool {
	switch method {
	case MethodThreadAttachmentAdd, MethodThreadAttachmentList, MethodThreadAttachmentOwnerList, MethodThreadAttachmentRemove:
		return true
	default:
		return false
	}
}

func (r *Router) handleThreadAttachmentAdd(request *Request) (*ThreadAttachmentAddResponse, error) {
	var params ThreadAttachmentAddParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.addThreadAttachment(&params)
}

func (r *Router) handleThreadAttachmentList(request *Request) (*ThreadAttachmentListResponse, error) {
	var params ThreadAttachmentListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.listThreadAttachments(&params)
}

func (r *Router) handleThreadAttachmentOwnerList(request *Request) (*ThreadAttachmentOwnerListResponse, error) {
	var params ThreadAttachmentOwnerListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.listThreadAttachmentOwners(&params)
}

func (r *Router) handleThreadAttachmentRemove(request *Request) (*ThreadAttachmentRemoveResponse, error) {
	var params ThreadAttachmentRemoveParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	response, _, err := r.removeThreadAttachment(&params)
	return response, err
}

func (r *Router) addThreadAttachment(params *ThreadAttachmentAddParams) (*ThreadAttachmentAddResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	payload, err := normalizeThreadAttachmentPayload(params.Payload)
	if err != nil {
		return nil, err
	}
	record, err := r.threadAttachmentRecord(params.ThreadID)
	if err != nil {
		return nil, err
	}
	attachments := threadAttachmentsFromExtra(record.Metadata.Extra)
	for _, existing := range attachments {
		if existing.AttachmentType == params.AttachmentType && existing.IdentityKey == params.IdentityKey {
			return &ThreadAttachmentAddResponse{
				Outcome:    ThreadAttachmentAddOutcomeExisting,
				Attachment: existing,
			}, nil
		}
	}
	if len(attachments) >= maxThreadAttachmentsPerThread {
		return nil, invalidParams(fmt.Sprintf(
			"invalid thread attachment request: thread attachment identity count exceeds %d",
			maxThreadAttachmentsPerThread,
		))
	}
	attachmentID, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("failed to create thread attachment id: %w", err)
	}
	attachment := ThreadAttachment{
		ID:             attachmentID.String(),
		AttachmentType: params.AttachmentType,
		IdentityKey:    params.IdentityKey,
		Payload:        payload,
		CreatedAt:      r.now().UTC().Unix(),
	}
	if err := r.persistThreadAttachments(record, append(attachments, attachment)); err != nil {
		return nil, err
	}
	return &ThreadAttachmentAddResponse{
		Outcome:    ThreadAttachmentAddOutcomeCreated,
		Attachment: attachment,
	}, nil
}

func (r *Router) listThreadAttachments(params *ThreadAttachmentListParams) (*ThreadAttachmentListResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	limit := defaultThreadAttachmentListLimit
	if params.Limit != nil {
		limit = int(*params.Limit)
	}
	if limit < 1 {
		limit = 1
	}
	if limit > maxThreadAttachmentListPageSize {
		limit = maxThreadAttachmentListPageSize
	}
	threadID := strings.TrimSpace(params.ThreadID)
	anchor, err := parseThreadAttachmentCursor(params.Cursor, threadID)
	if err != nil {
		return nil, err
	}
	record, err := r.threadAttachmentRecord(threadID)
	if err != nil {
		return nil, err
	}
	attachments := threadAttachmentsFromExtra(record.Metadata.Extra)
	sort.SliceStable(attachments, func(i, j int) bool {
		if attachments[i].CreatedAt != attachments[j].CreatedAt {
			return attachments[i].CreatedAt < attachments[j].CreatedAt
		}
		return attachments[i].ID < attachments[j].ID
	})
	page := make([]ThreadAttachment, 0, limit)
	var nextCursor *string
	for _, attachment := range attachments {
		if anchor != nil && !threadAttachmentAfterCursor(attachment, *anchor) {
			continue
		}
		if len(page) == limit {
			cursor := fmt.Sprintf("%s|%d|%s", threadID, page[len(page)-1].CreatedAt, page[len(page)-1].ID)
			nextCursor = &cursor
			break
		}
		page = append(page, attachment)
	}
	return &ThreadAttachmentListResponse{Data: page, NextCursor: nextCursor}, nil
}

// listThreadAttachmentOwners returns the threads that currently own an exact
// attachment identity, including archived threads by default. Results are
// ordered by thread id and use a keyset cursor, mirroring Rust's state-backed
// `list_thread_attachment_threads`.
func (r *Router) listThreadAttachmentOwners(params *ThreadAttachmentOwnerListParams) (*ThreadAttachmentOwnerListResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	limit := defaultThreadAttachmentListLimit
	if params.Limit != nil {
		limit = int(*params.Limit)
	}
	if limit < 1 {
		limit = 1
	}
	if limit > maxThreadAttachmentListPageSize {
		limit = maxThreadAttachmentListPageSize
	}
	if r == nil || r.store == nil {
		return nil, invalidParams("thread attachment owner lookup is unavailable")
	}
	anchor, err := parseThreadAttachmentOwnerCursor(params.Cursor, params.AttachmentType, params.IdentityKey, params.Archived)
	if err != nil {
		return nil, err
	}
	records, err := r.store.AllRecords()
	if err != nil {
		return nil, err
	}
	owners := make([]ThreadAttachmentOwner, 0, len(records))
	for i := range records {
		record := &records[i]
		if params.Archived != nil && record.Archived != *params.Archived {
			continue
		}
		if !threadRecordHasAttachmentIdentity(record, params.AttachmentType, params.IdentityKey) {
			continue
		}
		owners = append(owners, ThreadAttachmentOwner{ThreadID: string(record.ID), Archived: record.Archived})
	}
	sort.SliceStable(owners, func(i, j int) bool { return owners[i].ThreadID < owners[j].ThreadID })
	if anchor != nil {
		filtered := owners[:0]
		for _, owner := range owners {
			if owner.ThreadID > anchor.ThreadID {
				filtered = append(filtered, owner)
			}
		}
		owners = filtered
	}
	page := owners
	var nextCursor *string
	if len(owners) > limit {
		page = owners[:limit]
		encoded, encodeErr := encodeThreadAttachmentOwnerCursor(params, page[len(page)-1].ThreadID)
		if encodeErr != nil {
			return nil, encodeErr
		}
		nextCursor = &encoded
	}
	return &ThreadAttachmentOwnerListResponse{Data: page, NextCursor: nextCursor}, nil
}

func threadRecordHasAttachmentIdentity(record *session.Record, attachmentType string, identityKey string) bool {
	if record == nil {
		return false
	}
	for _, attachment := range threadAttachmentsFromExtra(record.Metadata.Extra) {
		if attachment.AttachmentType == attachmentType && attachment.IdentityKey == identityKey {
			return true
		}
	}
	return false
}

func parseThreadAttachmentOwnerCursor(cursor *string, attachmentType string, identityKey string, archived *bool) (*threadAttachmentOwnerCursor, error) {
	if cursor == nil || strings.TrimSpace(*cursor) == "" {
		return nil, nil
	}
	var parsed threadAttachmentOwnerCursor
	if err := json.Unmarshal([]byte(*cursor), &parsed); err != nil {
		return nil, invalidParams("invalid thread attachment request: invalid pagination cursor")
	}
	if parsed.AttachmentType != attachmentType ||
		parsed.IdentityKey != identityKey ||
		!equalThreadAttachmentBoolPtr(parsed.Archived, archived) ||
		!validUUIDString(parsed.ThreadID) {
		return nil, invalidParams("invalid thread attachment request: invalid pagination cursor")
	}
	return &parsed, nil
}

func encodeThreadAttachmentOwnerCursor(params *ThreadAttachmentOwnerListParams, threadID string) (string, error) {
	encoded, err := json.Marshal(threadAttachmentOwnerCursor{
		AttachmentType: params.AttachmentType,
		IdentityKey:    params.IdentityKey,
		Archived:       params.Archived,
		ThreadID:       threadID,
	})
	if err != nil {
		return "", invalidParams("invalid thread attachment request: invalid pagination cursor")
	}
	return string(encoded), nil
}

func equalThreadAttachmentBoolPtr(a *bool, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (r *Router) removeThreadAttachment(params *ThreadAttachmentRemoveParams) (*ThreadAttachmentRemoveResponse, *ThreadAttachment, error) {
	if err := params.Validate(); err != nil {
		return nil, nil, err
	}
	record, err := r.threadAttachmentRecord(params.ThreadID)
	if err != nil {
		return nil, nil, err
	}
	attachments := threadAttachmentsFromExtra(record.Metadata.Extra)
	remaining := make([]ThreadAttachment, 0, len(attachments))
	var removed *ThreadAttachment
	for _, attachment := range attachments {
		if attachment.AttachmentType == params.AttachmentType && attachment.IdentityKey == params.IdentityKey {
			candidate := attachment
			removed = &candidate
			continue
		}
		remaining = append(remaining, attachment)
	}
	if removed == nil {
		return &ThreadAttachmentRemoveResponse{}, nil, nil
	}
	if err := r.persistThreadAttachments(record, remaining); err != nil {
		return nil, nil, err
	}
	return &ThreadAttachmentRemoveResponse{}, removed, nil
}

func (r *Router) threadAttachmentRecord(threadID string) (*session.Record, error) {
	threadID = strings.TrimSpace(threadID)
	record, err := r.readThreadRecord(session.ThreadID(threadID), true, false)
	if err != nil {
		if errors.Is(err, session.ErrThreadNotFound) {
			return nil, invalidParams(fmt.Sprintf("thread not found: %s", threadID))
		}
		return nil, err
	}
	return record, nil
}

func (r *Router) persistThreadAttachments(record *session.Record, attachments []ThreadAttachment) error {
	if record == nil {
		return invalidParams("thread not found")
	}
	extra := cloneExtraMap(record.Metadata.Extra)
	if len(attachments) == 0 {
		delete(extra, threadAttachmentsExtraKey)
	} else {
		extra[threadAttachmentsExtraKey] = attachments
	}
	_, err := r.updateThreadMetadata(record.ID, &session.MetadataPatch{Extra: extra}, true)
	return err
}

// setThreadAttachmentsOnRecord replaces a record's attachment list in its
// metadata map without persisting it, so a fork can publish the list atomically
// with the rest of the record.
func setThreadAttachmentsOnRecord(record *session.Record, attachments []ThreadAttachment) {
	if record == nil {
		return
	}
	extra := cloneExtraMap(record.Metadata.Extra)
	if len(attachments) == 0 {
		delete(extra, threadAttachmentsExtraKey)
	} else {
		extra[threadAttachmentsExtraKey] = attachments
	}
	record.Metadata.Extra = extra
}

// applyThreadForkAttachments mirrors Rust #45579: a non-ephemeral fork owns a
// copy of the source thread's current attachments - fresh attachment ids and
// creation timestamps, identical resource identities and payloads - so the two
// threads' membership can change independently. An ephemeral fork inherits
// nothing. Referenced resources are never copied, and a copy failure is logged
// and leaves the fork without attachments rather than failing the fork.
//
// The caller applies this before publishing the fork, so the attachment list is
// persisted atomically with the new thread record.
func applyThreadForkAttachments(source, fork *session.Record, ephemeral bool, now time.Time) {
	if fork == nil {
		return
	}
	if ephemeral {
		setThreadAttachmentsOnRecord(fork, nil)
		return
	}
	var sourceAttachments []ThreadAttachment
	if source != nil {
		sourceAttachments = threadAttachmentsFromExtra(source.Metadata.Extra)
	}
	copied := make([]ThreadAttachment, 0, len(sourceAttachments))
	for _, attachment := range sourceAttachments {
		id, err := uuid.NewV7()
		if err != nil {
			slog.Warn("failed to copy thread attachments into fork; continuing without attachments", "error", err)
			copied = nil
			break
		}
		copied = append(copied, ThreadAttachment{
			ID:             id.String(),
			AttachmentType: attachment.AttachmentType,
			IdentityKey:    attachment.IdentityKey,
			Payload:        append(json.RawMessage(nil), attachment.Payload...),
			CreatedAt:      now.UTC().Unix(),
		})
	}
	setThreadAttachmentsOnRecord(fork, copied)
}

func normalizeThreadAttachmentPayload(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, invalidParams("invalid attachment payload: missing payload")
	}
	if len(raw) > maxThreadAttachmentPayloadBytes {
		return nil, invalidParams(fmt.Sprintf("attachment payload must not exceed %d bytes", maxThreadAttachmentPayloadBytes))
	}
	return append([]byte(nil), raw...), nil
}

type threadAttachmentCursor struct {
	threadID  string
	createdAt int64
	id        string
}

func parseThreadAttachmentCursor(cursor *string, threadID string) (*threadAttachmentCursor, error) {
	if cursor == nil {
		return nil, nil
	}
	segments := strings.Split(strings.TrimSpace(*cursor), "|")
	if len(segments) != 3 || segments[0] != threadID || !validUUIDString(segments[0]) || !validUUIDString(segments[2]) {
		return nil, invalidParams("invalid thread attachment request: invalid pagination cursor")
	}
	createdAt, err := strconv.ParseInt(segments[1], 10, 64)
	if err != nil {
		return nil, invalidParams("invalid thread attachment request: invalid pagination cursor")
	}
	return &threadAttachmentCursor{threadID: segments[0], createdAt: createdAt, id: segments[2]}, nil
}

func threadAttachmentAfterCursor(attachment ThreadAttachment, cursor threadAttachmentCursor) bool {
	if attachment.CreatedAt != cursor.createdAt {
		return attachment.CreatedAt > cursor.createdAt
	}
	return attachment.ID > cursor.id
}

// RuntimeRouter wrappers add notification delivery and reject ephemeral
// threads, matching Rust's state-backed store which has no ephemeral threads.

func (r *RuntimeRouter) handleThreadAttachmentRuntime(request *Request) (any, error) {
	if r == nil || r.services.ThreadRouter == nil {
		return nil, fmt.Errorf("%w: thread router is not configured", ErrInvalidRequest)
	}
	switch request.Method {
	case MethodThreadAttachmentAdd:
		return r.threadAttachmentAddRuntime(request)
	case MethodThreadAttachmentList:
		return r.threadAttachmentListRuntime(request)
	case MethodThreadAttachmentOwnerList:
		return r.threadAttachmentOwnerListRuntime(request)
	case MethodThreadAttachmentRemove:
		return r.threadAttachmentRemoveRuntime(request)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownMethod, request.Method)
	}
}

func (r *RuntimeRouter) threadAttachmentAddRuntime(request *Request) (*ThreadAttachmentAddResponse, error) {
	var params ThreadAttachmentAddParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := r.rejectEphemeralThreadAttachment(params.ThreadID); err != nil {
		return nil, err
	}
	response, err := r.services.ThreadRouter.addThreadAttachment(&params)
	if err != nil {
		return nil, err
	}
	if response.Outcome == ThreadAttachmentAddOutcomeCreated {
		r.notify(NotificationThreadAttachmentUpdated, &ThreadAttachmentUpdatedNotification{
			ThreadID:       strings.TrimSpace(params.ThreadID),
			AttachmentType: response.Attachment.AttachmentType,
			IdentityKey:    response.Attachment.IdentityKey,
			AttachmentID:   response.Attachment.ID,
			Operation:      ThreadAttachmentOperationCreated,
		})
	}
	return response, nil
}

func (r *RuntimeRouter) threadAttachmentListRuntime(request *Request) (*ThreadAttachmentListResponse, error) {
	var params ThreadAttachmentListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := r.rejectEphemeralThreadAttachment(params.ThreadID); err != nil {
		return nil, err
	}
	return r.services.ThreadRouter.listThreadAttachments(&params)
}

func (r *RuntimeRouter) threadAttachmentOwnerListRuntime(request *Request) (*ThreadAttachmentOwnerListResponse, error) {
	var params ThreadAttachmentOwnerListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	return r.services.ThreadRouter.listThreadAttachmentOwners(&params)
}

func (r *RuntimeRouter) threadAttachmentRemoveRuntime(request *Request) (*ThreadAttachmentRemoveResponse, error) {
	var params ThreadAttachmentRemoveParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := r.rejectEphemeralThreadAttachment(params.ThreadID); err != nil {
		return nil, err
	}
	response, removed, err := r.services.ThreadRouter.removeThreadAttachment(&params)
	if err != nil {
		return nil, err
	}
	if removed != nil {
		r.notify(NotificationThreadAttachmentUpdated, &ThreadAttachmentUpdatedNotification{
			ThreadID:       strings.TrimSpace(params.ThreadID),
			AttachmentType: removed.AttachmentType,
			IdentityKey:    removed.IdentityKey,
			AttachmentID:   removed.ID,
			Operation:      ThreadAttachmentOperationDeleted,
		})
	}
	return response, nil
}

func (r *RuntimeRouter) rejectEphemeralThreadAttachment(threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	if _, ok := r.ephemeralThreadRecord(session.ThreadID(threadID), false); ok {
		return invalidParams(fmt.Sprintf("thread not found: %s", threadID))
	}
	return nil
}
