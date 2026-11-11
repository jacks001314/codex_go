package cloudbackend

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"codex_go/appserver"
	"codex_go/cloudclient"
)

// TestCloudArchiveDetachesAttachmentLikeRust mirrors Rust #60635: a successful
// archive detaches the live conversation, clears cached resume metadata and
// emits thread/archived; a failed archive preserves both.
func TestCloudArchiveDetachesAttachmentLikeRust(t *testing.T) {
	ctx := context.Background()
	archived := false
	fake := &fakeCloud{archive: func(ctx context.Context, threadID string) error {
		if threadID != "thread-1" {
			t.Fatalf("archive thread = %q", threadID)
		}
		if !archived {
			return errors.New("archive failed")
		}
		return nil
	}}
	client := newTestClient(fake)
	client.active = &attachment{threadID: "thread-1", cancel: func() {}}
	client.loaded = &appserver.ThreadResumeResponse{Thread: &appserver.Thread{ID: "thread-1"}}

	if _, rpcErr := client.handleRequest(ctx, appserver.MethodThreadArchive, json.RawMessage(`{"threadId":"thread-1"}`), cloudclient.SubmissionStartIfIdle, false); rpcErr == nil {
		t.Fatal("failed archive was reported as successful")
	}
	if client.active == nil || client.loaded == nil {
		t.Fatalf("failed archive detached state: active=%#v loaded=%#v", client.active, client.loaded)
	}

	archived = true
	if _, rpcErr := client.handleRequest(ctx, appserver.MethodThreadArchive, json.RawMessage(`{"threadId":"thread-1"}`), cloudclient.SubmissionStartIfIdle, false); rpcErr != nil {
		t.Fatalf("archive error = %v", rpcErr)
	}
	if client.active != nil || client.loaded != nil {
		t.Fatalf("archive kept state: active=%#v loaded=%#v", client.active, client.loaded)
	}
	notifications := client.testNotifications()
	if len(notifications) != 1 || notifications[0].Method != appserver.NotificationThreadArchived {
		t.Fatalf("archive notifications = %#v", notifications)
	}
}

// TestCloudUnarchiveReturnsSnapshotAndNotificationLikeRust checks the restore
// response shape and the thread/unarchived notification.
func TestCloudUnarchiveReturnsSnapshotAndNotificationLikeRust(t *testing.T) {
	fake := &fakeCloud{unarchive: func(ctx context.Context, threadID string) (cloudclient.ThreadSnapshot, error) {
		return cloudclient.ThreadSnapshot{ID: threadID, CWD: "/repo"}, nil
	}}
	client := newTestClient(fake)
	result, rpcErr := client.handleRequest(context.Background(), appserver.MethodThreadUnarchive, json.RawMessage(`{"threadId":"thread-1"}`), cloudclient.SubmissionStartIfIdle, false)
	if rpcErr != nil {
		t.Fatalf("unarchive error = %v", rpcErr)
	}
	var response appserver.ThreadUnarchiveResponse
	if err := json.Unmarshal(result, &response); err != nil || response.Thread == nil || response.Thread.ID != "thread-1" || response.Thread.HistoryMode != "paginated" {
		t.Fatalf("unarchive response = %s (%v)", result, err)
	}
	notifications := client.testNotifications()
	if len(notifications) != 1 || notifications[0].Method != appserver.NotificationThreadUnarchived {
		t.Fatalf("unarchive notifications = %#v", notifications)
	}
}

func TestCloudThreadListSupportsArchivedLikeRust(t *testing.T) {
	archived := true
	query, err := cloudThreadListQuery(appserver.ThreadListParams{Archived: &archived})
	if err != nil || !query.Archived {
		t.Fatalf("archived query = %#v, %v", query, err)
	}
	active := false
	query, err = cloudThreadListQuery(appserver.ThreadListParams{Archived: &active})
	if err != nil || query.Archived {
		t.Fatalf("active query = %#v, %v", query, err)
	}
}
