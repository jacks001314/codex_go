package appserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

// writeRestoredHistoryPrewarmConfig enables the Rust #61959 feature the same
// way a user would: a `[features]` table in the thread's config file.
func writeRestoredHistoryPrewarmConfig(t *testing.T, home string, enabled bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(config.ConfigPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "[features]\nrestored_history_prewarm = false\n"
	if enabled {
		body = "[features]\nrestored_history_prewarm = true\n"
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// coldResumePrewarmRouter builds a router over a resumed thread whose history
// carries one user message made of the given content parts, mirroring the
// rollout a cold resume reads back.
func coldResumePrewarmRouter(t *testing.T, enabled bool, modelID string, part session.ContentPart) (*RuntimeRouter, *historyPrewarmAgent) {
	t.Helper()
	home := t.TempDir()
	writeRestoredHistoryPrewarmConfig(t, home, enabled)
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	record := &session.Record{
		ID: "child", SessionID: "child", ParentThreadID: "root",
		CreatedAt: now, UpdatedAt: now,
		Metadata: session.Metadata{CWD: home, Model: modelID, LastResponseID: "persisted-response"},
		Items: []session.Item{
			{ID: "user-1", Type: "message", Role: "user", Text: "what is in this image", Content: []session.ContentPart{part}, CreatedAt: now},
			{ID: "assistant-1", Type: "message", Role: "assistant", Text: "a chart", CreatedAt: now},
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	agent := &historyPrewarmAgent{prewarmingRuntimeAgent: &prewarmingRuntimeAgent{
		recordingRuntimeAgent: newRecordingRuntimeAgent("ok"),
		prewarmedID:           "restored-warm-1",
	}}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		DefaultCWD:   home,
	})
	t.Cleanup(func() { _ = router.Close() })
	retainIdleLifecycleThread(t, router, "child")
	// Retaining an idle thread schedules Rust #48812's idle-history warmup. Let
	// it finish and forget its request so the cold-resume warmup is the only
	// one this test observes.
	deadline := time.Now().Add(3 * time.Second)
	for {
		state := router.startupPrewarmsSnapshot()["child"]
		if state == nil || state.finished {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle-history prewarm did not finish (state=%#v)", state)
		}
		time.Sleep(5 * time.Millisecond)
	}
	agent.mu.Lock()
	agent.requests = nil
	agent.mu.Unlock()
	return router, agent
}

func waitForResumePrewarmRequest(t *testing.T, agent *historyPrewarmAgent) *model.AgentRequest {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		agent.mu.Lock()
		count := len(agent.requests)
		agent.mu.Unlock()
		if count > 0 {
			return agent.lastRequest()
		}
		if time.Now().After(deadline) {
			t.Fatal("cold-resume prewarm did not run")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForResumePrewarmFinished(t *testing.T, router *RuntimeRouter) *startupPrewarmState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		state := router.startupPrewarmsSnapshot()["child"]
		if state != nil && state.finished {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatalf("cold-resume prewarm did not finish (state=%#v)", state)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestRestoredHistoryPrewarmCandidateLikeRust pins Rust's `has_file_image` and
// its `for_prompt` projection: only a stored file reference on a model that
// accepts images can benefit from the socket's normalized-image cache.
func TestRestoredHistoryPrewarmCandidateLikeRust(t *testing.T) {
	fileImageMessage := []any{map[string]any{
		"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_image", "file_id": "file-1"}},
	}}
	fileImageToolOutput := []any{map[string]any{
		"type": "function_call_output", "call_id": "call-1",
		"output": []any{map[string]any{"type": "input_image", "file_id": "file-2"}},
	}}
	inlineImage := []any{map[string]any{
		"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAAA"}},
	}}
	textOnly := []any{map[string]any{
		"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_text", "text": "hi"}},
	}}
	vision := &model.ModelInfo{InputModalities: []string{"text", "image"}}
	textOnlyModel := &model.ModelInfo{InputModalities: []string{"text"}}
	cases := []struct {
		name  string
		items []any
		info  *model.ModelInfo
		want  bool
	}{
		{name: "message file image", items: fileImageMessage, info: vision, want: true},
		{name: "tool output file image", items: fileImageToolOutput, info: vision, want: true},
		{name: "inline image only", items: inlineImage, info: vision, want: false},
		{name: "text-only history", items: textOnly, info: vision, want: false},
		{name: "text-only model", items: fileImageMessage, info: textOnlyModel, want: false},
		{name: "unknown model", items: fileImageMessage, info: nil, want: false},
		{name: "empty history", items: nil, info: vision, want: false},
	}
	for _, tc := range cases {
		if got := restoredHistoryPrewarmCandidate(tc.items, tc.info); got != tc.want {
			t.Errorf("%s: candidate = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestColdResumePrewarmsRestoredFileImagesLikeRust pins Rust #61959: the warmup
// carries the restored history when it has file images, and its response is
// never chained by the first turn.
func TestColdResumePrewarmsRestoredFileImagesLikeRust(t *testing.T) {
	router, agent := coldResumePrewarmRouter(t, true, "gpt-6.1-sol", session.ContentPart{Type: "input_image", FileID: "file-1"})

	router.scheduleResumePrewarm("child")
	request := waitForResumePrewarmRequest(t, agent)
	if request.PrewarmInputMode != startupPrewarmInputRestoredHistory {
		t.Fatalf("prewarm input mode = %q, want %q", request.PrewarmInputMode, startupPrewarmInputRestoredHistory)
	}
	if len(request.InputItems) != 2 {
		t.Fatalf("restored warmup input items = %d, want the full 2-item history", len(request.InputItems))
	}
	if request.Instructions == "" {
		t.Fatal("restored warmup did not carry the restored base instructions")
	}
	state := waitForResumePrewarmFinished(t, router)
	if state.inputMode != startupPrewarmInputRestoredHistory || state.responseID != "restored-warm-1" {
		t.Fatalf("restored prewarm state = %#v", state)
	}

	settingsKey := prewarmSettingsKey(request.Model, request.Instructions, "")
	if id, consumed, discard := router.consumeStartupPrewarmForTurn("child", request.InputItems, settingsKey, "persisted-response"); id != "" || consumed != 0 || !discard {
		t.Fatalf("restored consumption = (%q, %d, %v), want (\"\", 0, true)", id, consumed, discard)
	}
	// The warmup is consumed once; a later turn is an ordinary continuation of
	// the response generation produced.
	if _, _, discard := router.consumeStartupPrewarmForTurn("child", request.InputItems, settingsKey, "persisted-response"); discard {
		t.Fatal("restored warmup discarded the continuation twice")
	}
}

// TestColdResumeRestoredPrewarmWithoutPersistedContinuationLikeRust pins the
// same drop when the thread has no persisted response id: the warmup id is
// still never chained.
func TestColdResumeRestoredPrewarmWithoutPersistedContinuationLikeRust(t *testing.T) {
	router, agent := coldResumePrewarmRouter(t, true, "gpt-6.1-sol", session.ContentPart{Type: "input_image", FileID: "file-1"})

	router.scheduleResumePrewarm("child")
	request := waitForResumePrewarmRequest(t, agent)
	waitForResumePrewarmFinished(t, router)
	if id, consumed := router.takeStartupPrewarmResponseID("child", request.InputItems, prewarmSettingsKey(request.Model, request.Instructions, "")); id != "" || consumed != 0 {
		t.Fatalf("restored prewarm response = (%q, %d), want it never chained", id, consumed)
	}
}

// TestColdResumePrewarmFallsBackToBaseLikeRust pins the two fallbacks: text-only
// history and a disabled feature both keep the empty base warm-up.
func TestColdResumePrewarmFallsBackToBaseLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		part    session.ContentPart
	}{
		{name: "text-only history", enabled: true, part: session.ContentPart{Type: "input_text", Text: "hi"}},
		{name: "feature disabled", enabled: false, part: session.ContentPart{Type: "input_image", FileID: "file-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, agent := coldResumePrewarmRouter(t, tc.enabled, "gpt-6.1-sol", tc.part)
			router.scheduleResumePrewarm("child")
			request := waitForResumePrewarmRequest(t, agent)
			if request.PrewarmInputMode != startupPrewarmInputBase {
				t.Fatalf("prewarm input mode = %q, want %q", request.PrewarmInputMode, startupPrewarmInputBase)
			}
			if len(request.InputItems) != 0 {
				t.Fatalf("base warmup input items = %d, want 0", len(request.InputItems))
			}
			state := waitForResumePrewarmFinished(t, router)
			if state.inputMode != startupPrewarmInputBase {
				t.Fatalf("prewarm state mode = %q, want %q", state.inputMode, startupPrewarmInputBase)
			}
		})
	}
}
