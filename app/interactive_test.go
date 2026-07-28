package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/appserver"
	"codex_go/auth"
	"codex_go/config"
	"codex_go/session"
	codextui "codex_go/tui"
)

func TestInteractivePluginEnabledWriterPreservesPluginMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	configPath := filepath.Join(home, "config.toml")
	contents := "[plugins.\"docs@team\"]\npath = \"D:/plugins/docs\"\ntrusted_hash = \"abc123\"\nenabled = true\n"
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := interactivePluginEnabledWriter(nil)("docs@team", false); err != nil {
		t.Fatalf("plugin enabled write: %v", err)
	}
	loaded, err := config.LoadEffective(home, nil, nil, nil)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	plugins, _ := loaded.Values["plugins"].(map[string]any)
	entry, _ := plugins["docs@team"].(map[string]any)
	if entry["enabled"] != false || entry["path"] != "D:/plugins/docs" || entry["trusted_hash"] != "abc123" {
		t.Fatalf("plugin config after toggle = %#v", entry)
	}
}

func TestInteractiveSessionMessagesHideContextInstructionItems(t *testing.T) {
	record := &session.Record{Items: []session.Item{{
		ID:        "skill-instructions-turn-1-1",
		Type:      "message",
		Role:      "developer",
		Text:      "<skill>\n<name>imagegen</name>\nHidden instructions.\n</skill>",
		CreatedAt: time.Unix(1700000000, 0).UTC(),
		Data:      map[string]any{"kind": "skill_instructions"},
	}, {
		ID:        "image-generation-instructions-ig_123",
		Type:      "message",
		Role:      "developer",
		Text:      "Generated images are saved to C:/tmp as C:/tmp/<image_id>.png by default.",
		CreatedAt: time.Unix(1700000001, 0).UTC(),
		Metadata:  map[string]any{"kind": "image_generation_instructions"},
	}, {
		ID:        "user-1",
		Type:      "message",
		Role:      "user",
		Text:      "visible",
		CreatedAt: time.Unix(1700000002, 0).UTC(),
	}}}

	messages := interactiveSessionMessagesFromRecord(record)
	if len(messages) != 1 || messages[0].Text != "visible" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestInteractiveSessionMessagesReplayReviewLifecycleLikeRust(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	record := &session.Record{Items: []session.Item{
		{ID: "review-turn", Type: "enteredReviewMode", Text: "changes against 'main'", CreatedAt: now},
		{ID: "review-user", Type: "message", Role: "user", Text: "hidden review action", CreatedAt: now.Add(time.Second), Metadata: map[string]any{"kind": "review_rollout_user"}},
		{ID: "review-turn", Type: "exitedReviewMode", Text: "review output", CreatedAt: now.Add(2 * time.Second)},
		{ID: "review-assistant", Type: "agent_message", Role: "assistant", Text: "Found one issue", CreatedAt: now.Add(3 * time.Second)},
	}}

	messages := interactiveSessionMessagesFromRecord(record)
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", messages)
	}
	want := []string{">> Code review started: changes against 'main' <<", "<< Code review finished >>", "Found one issue"}
	for i := range want {
		if messages[i].Text != want[i] {
			t.Fatalf("messages[%d] = %#v, want %q", i, messages[i], want[i])
		}
	}
}

func TestRemoteThreadMessagesReplayReviewLifecycleLikeRust(t *testing.T) {
	thread := &appserver.Thread{Turns: []appserver.Turn{{Items: []appserver.ThreadItem{
		{ID: "review-turn", Type: "enteredReviewMode", Text: "changes against 'main'"},
		{ID: "review-user", Type: "userMessage", Role: "user", Text: "hidden review action", Data: map[string]any{"kind": "review_rollout_user"}},
		{ID: "review-turn", Type: "exitedReviewMode", Text: "review output"},
		{ID: "review-assistant", Type: "agentMessage", Role: "assistant", Text: "Found one issue"},
	}}}}

	messages := remoteTUIThreadMessagesFromThread(thread)
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", messages)
	}
	want := []string{">> Code review started: changes against 'main' <<", "<< Code review finished >>", "Found one issue"}
	for i := range want {
		if messages[i].Text != want[i] {
			t.Fatalf("messages[%d] = %#v, want %q", i, messages[i], want[i])
		}
	}
}

func TestInteractiveAndRemoteSessionMessagesReplayContextCompactionLikeRust(t *testing.T) {
	local := interactiveSessionMessagesFromRecord(&session.Record{Items: []session.Item{{ID: "compact-1", Type: "contextCompaction"}}})
	if len(local) != 1 || local[0].Role != codextui.RoleHistory || local[0].Text != "Context compacted" {
		t.Fatalf("local compaction replay = %#v", local)
	}
	remote := remoteTUIThreadMessagesFromThread(&appserver.Thread{Turns: []appserver.Turn{{Items: []appserver.ThreadItem{{ID: "compact-1", Type: "contextCompaction"}}}}})
	if len(remote) != 1 || remote[0].Role != codextui.RoleHistory || remote[0].Text != "Context compacted" {
		t.Fatalf("remote compaction replay = %#v", remote)
	}
}

func TestInteractiveRepairImageGenerationItemsSavesLegacyRawCall(t *testing.T) {
	home := t.TempDir()
	record := &session.Record{
		ID: "thread-legacy",
		Items: []session.Item{{
			ID:        "ig_legacy",
			Type:      "image_generation_call",
			Role:      "assistant",
			Text:      "Zm9v",
			CreatedAt: time.Unix(1700000000, 0).UTC(),
			Data: map[string]any{
				"result":         "Zm9v",
				"revised_prompt": "legacy prompt",
			},
		}},
	}

	if !interactiveRepairImageGenerationItems(record, home) {
		t.Fatalf("expected repair to change record")
	}
	item := record.Items[0]
	if item.Type != "imageGeneration" || item.Role != "" || item.Text != "legacy prompt" {
		t.Fatalf("repaired item = %#v", item)
	}
	savedPath := interactiveSessionItemDataString(item, "savedPath", "saved_path")
	if savedPath == "" || filepath.Base(savedPath) != "ig_legacy.png" {
		t.Fatalf("saved path = %q", savedPath)
	}
	bytes, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", savedPath, err)
	}
	if string(bytes) != "foo" {
		t.Fatalf("saved bytes = %q", string(bytes))
	}
	message, ok := interactiveSessionMessageFromItem(item)
	if !ok || strings.Contains(message.Text, "Zm9v") || !strings.Contains(message.Text, "Saved to: "+savedPath) {
		t.Fatalf("message = %#v ok=%v", message, ok)
	}
}

func TestInteractiveRenameThreadHandlerPersistsExplicitName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	store := newSessionStore()
	if err := store.Create(&session.Record{
		ID:    "thread-rename",
		Title: "Generated title",
		Metadata: session.Metadata{
			Extra: map[string]any{"preserved": true},
		},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := interactiveRenameThreadHandler()("thread-rename", "Release triage"); err != nil {
		t.Fatalf("rename error = %v", err)
	}
	record, err := store.Read("thread-rename", false, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if record.Title != "Release triage" {
		t.Fatalf("Title = %q, want Release triage", record.Title)
	}
	if record.Metadata.Extra["thread_name_explicit"] != true || record.Metadata.Extra["preserved"] != true {
		t.Fatalf("Metadata.Extra = %#v", record.Metadata.Extra)
	}
}

func TestInteractiveAccountDisplayMatchesStatusContract(t *testing.T) {
	email := "dev@example.com"
	tests := []struct {
		name        string
		account     *auth.Account
		want        string
		wantChatGPT bool
	}{
		{name: "signed out"},
		{name: "api key", account: &auth.Account{Type: auth.AccountAPIKey}, want: "API key configured (run codex login to use ChatGPT)"},
		{name: "chatgpt", account: &auth.Account{Type: auth.AccountChatGPT}, want: "ChatGPT", wantChatGPT: true},
		{name: "chatgpt email and plan", account: &auth.Account{Type: auth.AccountChatGPT, Email: &email, PlanType: auth.PlanPlus}, want: "dev@example.com (plus)", wantChatGPT: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, gotChatGPT := interactiveAccountDisplay(test.account)
			if got != test.want || gotChatGPT != test.wantChatGPT {
				t.Fatalf("display = %q chatgpt=%v", got, gotChatGPT)
			}
		})
	}
}

func TestInteractiveFeedbackSubmitHandlerPreparesLocalUpload(t *testing.T) {
	reason := "hung after approval"
	threadID := "thread-feedback"
	response, err := interactiveFeedbackSubmitHandler()(appserver.FeedbackUploadParams{
		Classification: "bug",
		Reason:         &reason,
		ThreadID:       &threadID,
		IncludeLogs:    true,
	})
	if err != nil {
		t.Fatalf("feedback submit error = %v", err)
	}
	if response.ThreadID != threadID {
		t.Fatalf("feedback response = %#v", response)
	}
	if _, err := interactiveFeedbackSubmitHandler()(appserver.FeedbackUploadParams{}); err == nil {
		t.Fatal("feedback submit accepted missing classification")
	}
}
