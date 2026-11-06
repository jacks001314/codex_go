package cloudbackend

import (
	"testing"

	"codex_go/cloudclient"
)

// Rust #59828: Git metadata survives the snapshot-to-protocol mapping.
func TestCloudThreadSnapshotPreservesGitInfoLikeRust(t *testing.T) {
	branch := "main"
	origin := "https://example.com/repo.git"
	sha := "abc123"
	thread, err := cloudThreadSnapshot(cloudclient.ThreadSnapshot{
		ID:            "thread-1",
		SessionID:     "session-1",
		ModelProvider: "openai",
		CWD:           "/repo",
		Status:        map[string]any{"type": "idle"},
		GitInfo:       &cloudclient.GitInfo{Branch: &branch, OriginURL: &origin, SHA: &sha},
	})
	if err != nil {
		t.Fatalf("cloudThreadSnapshot() error = %v", err)
	}
	if thread.GitInfo == nil || thread.GitInfo.Branch == nil || *thread.GitInfo.Branch != "main" ||
		thread.GitInfo.OriginURL == nil || *thread.GitInfo.OriginURL != origin ||
		thread.GitInfo.SHA == nil || *thread.GitInfo.SHA != sha {
		t.Fatalf("thread git info = %#v", thread.GitInfo)
	}
}
