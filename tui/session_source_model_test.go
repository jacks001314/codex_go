package tui

import (
	"testing"
	"time"

	"codex_go/appserver"
)

// TestSessionSummaryCarriesThreadModelFromAppServer covers Rust #43360: session
// selections preserve the server-reported model so resume can restore it.
func TestSessionSummaryCarriesThreadModelFromAppServer(t *testing.T) {
	model := "server-model"
	provider := "server-provider"
	thread := &appserver.Thread{
		ID:            "thread-1",
		ModelProvider: provider,
		Model:         &model,
		CWD:           "D:/repo",
		CreatedAt:     time.Now().Unix(),
		UpdatedAt:     time.Now().Unix(),
	}
	summary := sessionSummaryFromAppServerThread(thread, false)
	if summary.Model != model {
		t.Fatalf("summary model = %q, want %q", summary.Model, model)
	}
	if summary.Provider != provider {
		t.Fatalf("summary provider = %q, want %q", summary.Provider, provider)
	}

	thread.Model = nil
	if summary := sessionSummaryFromAppServerThread(thread, false); summary.Model != "" {
		t.Fatalf("summary model = %q, want empty when the server omits it", summary.Model)
	}
}
