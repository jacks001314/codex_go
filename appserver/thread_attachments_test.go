package appserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"codex_go/session"
)

func TestRuntimeRouterThreadAttachmentLifecycle(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: "D:/repo"}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID

	add := router.Handle(requestWithParams(t, IntID(2), MethodThreadAttachmentAdd, ThreadAttachmentAddParams{
		ThreadID:       threadID,
		AttachmentType: "note",
		IdentityKey:    "identity-1",
		Payload:        json.RawMessage(`{"text":"hello"}`),
	}))
	if add.Error != nil {
		t.Fatalf("thread/attachment/add error: %+v", add.Error)
	}
	added := add.Result.(*ThreadAttachmentAddResponse)
	if added.Outcome != ThreadAttachmentAddOutcomeCreated {
		t.Fatalf("add outcome = %q, want created", added.Outcome)
	}
	if added.Attachment.ID == "" || added.Attachment.CreatedAt == 0 {
		t.Fatalf("created attachment = %#v", added.Attachment)
	}
	if string(added.Attachment.Payload) != `{"text":"hello"}` {
		t.Fatalf("payload = %s", added.Attachment.Payload)
	}
	if !sinkHasAttachmentUpdate(sink, ThreadAttachmentOperationCreated, added.Attachment.ID) {
		t.Fatalf("created notification missing: %+v", sink.List())
	}

	// Repeated add for the same (type, identity) returns the existing record
	// and must not emit another notification.
	existing := router.Handle(requestWithParams(t, IntID(3), MethodThreadAttachmentAdd, ThreadAttachmentAddParams{
		ThreadID:       threadID,
		AttachmentType: "note",
		IdentityKey:    "identity-1",
		Payload:        json.RawMessage(`{"text":"changed"}`),
	}))
	if existing.Error != nil {
		t.Fatalf("second thread/attachment/add error: %+v", existing.Error)
	}
	repeated := existing.Result.(*ThreadAttachmentAddResponse)
	if repeated.Outcome != ThreadAttachmentAddOutcomeExisting || repeated.Attachment.ID != added.Attachment.ID {
		t.Fatalf("repeated add = %#v", repeated)
	}
	if string(repeated.Attachment.Payload) != `{"text":"hello"}` {
		t.Fatalf("existing payload changed: %s", repeated.Attachment.Payload)
	}
	if count := attachmentUpdateCount(sink); count != 1 {
		t.Fatalf("attachment notifications after repeated add = %d, want 1", count)
	}

	list := router.Handle(requestWithParams(t, IntID(4), MethodThreadAttachmentList, ThreadAttachmentListParams{ThreadID: threadID}))
	if list.Error != nil {
		t.Fatalf("thread/attachment/list error: %+v", list.Error)
	}
	listResponse := list.Result.(*ThreadAttachmentListResponse)
	if len(listResponse.Data) != 1 || listResponse.Data[0].ID != added.Attachment.ID || listResponse.NextCursor != nil {
		t.Fatalf("list response = %#v", listResponse)
	}

	remove := router.Handle(requestWithParams(t, IntID(5), MethodThreadAttachmentRemove, ThreadAttachmentRemoveParams{
		ThreadID:       threadID,
		AttachmentType: "note",
		IdentityKey:    "identity-1",
	}))
	if remove.Error != nil {
		t.Fatalf("thread/attachment/remove error: %+v", remove.Error)
	}
	if !sinkHasAttachmentUpdate(sink, ThreadAttachmentOperationDeleted, added.Attachment.ID) {
		t.Fatalf("deleted notification missing: %+v", sink.List())
	}

	empty := router.Handle(requestWithParams(t, IntID(6), MethodThreadAttachmentList, ThreadAttachmentListParams{ThreadID: threadID}))
	if empty.Error != nil {
		t.Fatalf("thread/attachment/list after remove error: %+v", empty.Error)
	}
	if data := empty.Result.(*ThreadAttachmentListResponse).Data; len(data) != 0 {
		t.Fatalf("list after remove = %#v", data)
	}

	// Removing a missing identity succeeds without a notification.
	before := attachmentUpdateCount(sink)
	missing := router.Handle(requestWithParams(t, IntID(7), MethodThreadAttachmentRemove, ThreadAttachmentRemoveParams{
		ThreadID:       threadID,
		AttachmentType: "note",
		IdentityKey:    "identity-1",
	}))
	if missing.Error != nil {
		t.Fatalf("removing missing attachment error: %+v", missing.Error)
	}
	if after := attachmentUpdateCount(sink); after != before {
		t.Fatalf("missing removal emitted a notification: before=%d after=%d", before, after)
	}
}

func TestRuntimeRouterThreadAttachmentListPagination(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadStatus: NewThreadStatusManager(),
	})
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: "D:/repo"}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID

	created := map[string]bool{}
	for index := 0; index < 3; index++ {
		add := router.Handle(requestWithParams(t, IntID(int64(10+index)), MethodThreadAttachmentAdd, ThreadAttachmentAddParams{
			ThreadID:       threadID,
			AttachmentType: "note",
			IdentityKey:    fmt.Sprintf("identity-%d", index),
			Payload:        json.RawMessage(`{}`),
		}))
		if add.Error != nil {
			t.Fatalf("add %d error: %+v", index, add.Error)
		}
		created[add.Result.(*ThreadAttachmentAddResponse).Attachment.ID] = true
	}

	limit := uint32(1)
	seen := map[string]bool{}
	var cursor *string
	for pageIndex := 0; pageIndex < 10; pageIndex++ {
		list := router.Handle(requestWithParams(t, IntID(int64(100+pageIndex)), MethodThreadAttachmentList, ThreadAttachmentListParams{
			ThreadID: threadID,
			Limit:    &limit,
			Cursor:   cursor,
		}))
		if list.Error != nil {
			t.Fatalf("list page %d error: %+v", pageIndex, list.Error)
		}
		page := list.Result.(*ThreadAttachmentListResponse)
		if len(page.Data) != 1 {
			t.Fatalf("page %d size = %d", pageIndex, len(page.Data))
		}
		if seen[page.Data[0].ID] {
			t.Fatalf("page %d repeated attachment %s", pageIndex, page.Data[0].ID)
		}
		seen[page.Data[0].ID] = true
		if page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != len(created) {
		t.Fatalf("paginated ids = %v, want %v", seen, created)
	}

	// An invalid cursor is an invalid-params (-32602) failure.
	badCursor := threadID + "|not-a-number|" + firstKey(created)
	invalid := router.Handle(requestWithParams(t, IntID(200), MethodThreadAttachmentList, ThreadAttachmentListParams{
		ThreadID: threadID,
		Cursor:   &badCursor,
	}))
	if invalid.Error == nil || invalid.Error.Code != JSONRPCInvalidParamsErrorCode ||
		invalid.Error.Message != "invalid thread attachment request: invalid pagination cursor" {
		t.Fatalf("invalid cursor error = %+v", invalid.Error)
	}
}

func TestRouterThreadAttachmentValidation(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: "D:/repo"}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID

	longValue := strings.Repeat("x", maxThreadAttachmentTypeBytes+1)
	longIdentity := strings.Repeat("y", maxThreadAttachmentIdentityKeyBytes+1)
	oversizedPayload := json.RawMessage(`"` + strings.Repeat("z", maxThreadAttachmentPayloadBytes) + `"`)

	cases := []struct {
		name    string
		method  Method
		params  any
		message string
	}{
		{
			name:    "empty attachment type",
			method:  MethodThreadAttachmentAdd,
			params:  ThreadAttachmentAddParams{ThreadID: threadID, AttachmentType: " ", IdentityKey: "k", Payload: json.RawMessage(`{}`)},
			message: "attachmentType must not be empty",
		},
		{
			name:    "long attachment type",
			method:  MethodThreadAttachmentAdd,
			params:  ThreadAttachmentAddParams{ThreadID: threadID, AttachmentType: longValue, IdentityKey: "k", Payload: json.RawMessage(`{}`)},
			message: fmt.Sprintf("attachmentType must not exceed %d bytes", maxThreadAttachmentTypeBytes),
		},
		{
			name:    "empty identity key",
			method:  MethodThreadAttachmentAdd,
			params:  ThreadAttachmentAddParams{ThreadID: threadID, AttachmentType: "note", IdentityKey: "", Payload: json.RawMessage(`{}`)},
			message: "identityKey must not be empty",
		},
		{
			name:    "long identity key",
			method:  MethodThreadAttachmentAdd,
			params:  ThreadAttachmentAddParams{ThreadID: threadID, AttachmentType: "note", IdentityKey: longIdentity, Payload: json.RawMessage(`{}`)},
			message: fmt.Sprintf("identityKey must not exceed %d bytes", maxThreadAttachmentIdentityKeyBytes),
		},
		{
			name:    "oversized payload",
			method:  MethodThreadAttachmentAdd,
			params:  ThreadAttachmentAddParams{ThreadID: threadID, AttachmentType: "note", IdentityKey: "k", Payload: oversizedPayload},
			message: fmt.Sprintf("attachment payload must not exceed %d bytes", maxThreadAttachmentPayloadBytes),
		},
		{
			name:    "invalid thread id",
			method:  MethodThreadAttachmentList,
			params:  ThreadAttachmentListParams{ThreadID: "not-a-uuid"},
			message: "invalid thread id: invalid UUID",
		},
	}
	for index, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := router.Handle(requestWithParams(t, IntID(int64(index+10)), test.method, test.params))
			if response.Error == nil {
				t.Fatalf("expected error for %s", test.name)
			}
			if response.Error.Code != JSONRPCInvalidParamsErrorCode || response.Error.Message != test.message {
				t.Fatalf("error = %+v, want code %d message %q", response.Error, JSONRPCInvalidParamsErrorCode, test.message)
			}
		})
	}

	unknown := router.Handle(requestWithParams(t, IntID(90), MethodThreadAttachmentList, ThreadAttachmentListParams{
		ThreadID: "00000000-0000-7000-8000-000000000000",
	}))
	if unknown.Error == nil || unknown.Error.Code != JSONRPCInvalidParamsErrorCode ||
		unknown.Error.Message != "thread not found: 00000000-0000-7000-8000-000000000000" {
		t.Fatalf("unknown thread error = %+v", unknown.Error)
	}

	// A payload field that is entirely absent is rejected; an explicit JSON
	// null payload is a valid JsonValue.
	raw := &Request{
		JSONRPC: "2.0",
		ID:      IntID(91),
		Method:  MethodThreadAttachmentAdd,
		Params:  json.RawMessage(`{"threadId":"` + threadID + `","attachmentType":"note","identityKey":"k"}`),
	}
	missing := router.Handle(raw)
	if missing.Error == nil || missing.Error.Code != JSONRPCInvalidParamsErrorCode ||
		missing.Error.Message != "invalid attachment payload: missing payload" {
		t.Fatalf("missing payload error = %+v", missing.Error)
	}
	nullPayload := router.Handle(requestWithParams(t, IntID(92), MethodThreadAttachmentAdd, ThreadAttachmentAddParams{
		ThreadID:       threadID,
		AttachmentType: "note",
		IdentityKey:    "null-payload",
		Payload:        json.RawMessage(`null`),
	}))
	if nullPayload.Error != nil {
		t.Fatalf("null payload error = %+v", nullPayload.Error)
	}
}

func TestRouterThreadAttachmentLimitPerThread(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: "D:/repo"}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID

	for index := 0; index < maxThreadAttachmentsPerThread; index++ {
		add := router.Handle(requestWithParams(t, IntID(int64(index+2)), MethodThreadAttachmentAdd, ThreadAttachmentAddParams{
			ThreadID:       threadID,
			AttachmentType: "note",
			IdentityKey:    fmt.Sprintf("identity-%d", index),
			Payload:        json.RawMessage(`{}`),
		}))
		if add.Error != nil {
			t.Fatalf("add %d error: %+v", index, add.Error)
		}
	}
	overflow := router.Handle(requestWithParams(t, IntID(1000), MethodThreadAttachmentAdd, ThreadAttachmentAddParams{
		ThreadID:       threadID,
		AttachmentType: "note",
		IdentityKey:    "one-too-many",
		Payload:        json.RawMessage(`{}`),
	}))
	want := fmt.Sprintf("invalid thread attachment request: thread attachment identity count exceeds %d", maxThreadAttachmentsPerThread)
	if overflow.Error == nil || overflow.Error.Code != JSONRPCInvalidParamsErrorCode || overflow.Error.Message != want {
		t.Fatalf("overflow error = %+v, want %q", overflow.Error, want)
	}
}

func sinkHasAttachmentUpdate(sink *NotificationBuffer, operation ThreadAttachmentOperation, attachmentID string) bool {
	for _, notification := range sink.List() {
		if notification.Method != NotificationThreadAttachmentUpdated {
			continue
		}
		update, ok := notification.Params.(*ThreadAttachmentUpdatedNotification)
		if ok && update.Operation == operation && update.AttachmentID == attachmentID {
			return true
		}
	}
	return false
}

func attachmentUpdateCount(sink *NotificationBuffer) int {
	count := 0
	for _, notification := range sink.List() {
		if notification.Method == NotificationThreadAttachmentUpdated {
			count++
		}
	}
	return count
}

func firstKey(values map[string]bool) string {
	for key := range values {
		return key
	}
	return ""
}
