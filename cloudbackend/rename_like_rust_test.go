package cloudbackend

import (
	"codex_go/appserver"
	"codex_go/cloudclient"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type renameCloud struct {
	fakeCloud
	rename func(context.Context, string, string) error
}

func (f *renameCloud) SetThreadName(ctx context.Context, id, name string) error {
	return f.rename(ctx, id, name)
}
func TestCloudRenameAcknowledgmentLikeRust(t *testing.T) {
	for _, fails := range []bool{true, false} {
		calls := 0
		fake := &renameCloud{rename: func(_ context.Context, id, name string) error {
			calls++
			if id != "thread-1" || name != "New name" {
				t.Fatalf("rename = %q/%q", id, name)
			}
			if fails {
				return errors.New("rename failed")
			}
			return nil
		}}
		client := newTestClient(fake)
		old := "Old"
		client.loaded = &appserver.ThreadResumeResponse{Thread: &appserver.Thread{ID: "thread-1", Name: &old}}
		_, err := client.handleRequest(context.Background(), appserver.MethodThreadSetName, json.RawMessage(`{"threadId":"thread-1","name":"  New name  "}`), cloudclient.SubmissionStartIfIdle, false)
		if calls != 1 || (err != nil) != fails {
			t.Fatalf("calls=%d error=%v fails=%v", calls, err, fails)
		}
		notifications := client.testNotifications()
		if fails {
			if *client.loaded.Thread.Name != "Old" || len(notifications) != 0 {
				t.Fatal("failed rename mutated state")
			}
		} else {
			if *client.loaded.Thread.Name != "New name" || len(notifications) != 1 || notifications[0].Method != appserver.NotificationThreadNameUpdated {
				t.Fatalf("rename state=%#v notifications=%#v", client.loaded, notifications)
			}
		}
	}
}
