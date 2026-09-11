package appserver

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

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
	case MethodThreadAttachmentAdd, MethodThreadAttachmentList, MethodThreadAttachmentRemove:
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
