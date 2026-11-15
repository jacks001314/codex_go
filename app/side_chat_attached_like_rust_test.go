package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
	codextea "codex_go/tui/tea"
)

func newAttachedSideChatParent(t *testing.T, store *session.Store, title string) *session.Record {
	t.Helper()
	now := time.Now().UTC()
	home := filepath.Dir(store.Root())
	rolloutPath := rollout.PathForThread(home, "thread-parent", now)
	parent := &session.Record{
		ID:        "thread-parent",
		SessionID: "thread-parent",
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:         `D:\repo`,
			HistoryMode: "paginated",
			RolloutTurns: []session.TurnSnapshot{
				{ID: "turn-1", Status: "completed"},
			},
			Extra: map[string]any{
				"thread_name_explicit":    true,
				"runtime_workspace_roots": []any{"D:/repo"},
				"rollout_path":            rolloutPath,
			},
		},
		Items: []session.Item{{
			ID:        "parent-user",
			Type:      "message",
			Role:      "user",
			Text:      "Prepare a release",
			CreatedAt: now,
			Metadata:  map[string]any{"turnId": "turn-1"},
		}},
	}
	if err := store.Create(parent); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     home,
		ThreadID:      "thread-parent",
		Source:        "vscode",
		CWD:           `D:\repo`,
		ModelProvider: "openai",
		HistoryMode:   "paginated",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewRecorder(parent) error = %v", err)
	}
	if err := recorder.AppendTurnStarted("turn-1", now); err != nil {
		t.Fatalf("AppendTurnStarted(parent) error = %v", err)
	}
	if err := recorder.AppendItem(rollout.Item{
		ID:   "thread-parent-user",
		Type: "user_message",
		Text: "Prepare a release",
		Data: map[string]any{"turn_id": "turn-1"},
	}); err != nil {
		t.Fatalf("AppendItem(parent) error = %v", err)
	}
	if err := recorder.AppendTurnComplete("turn-1", now.Add(time.Second), 1000); err != nil {
		t.Fatalf("AppendTurnComplete(parent) error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close(parent recorder) error = %v", err)
	}
	return parent
}

// TestAttachedSideChatIsSavedListedAndArchivedLikeRust covers Rust
// sidebar_fork (#61343): the sidebar forks a saved attached child, keeps the
// inherited name only until the first direct title, and archives instead of
// deleting when the side chat closes.
func TestAttachedSideChatIsSavedListedAndArchivedLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	newAttachedSideChatParent(t, store, "Release plan")

	coordinator := newInteractiveLocalSideCoordinator(store)
	response, err := coordinator.Start(codextea.SideStartParams{
		ParentThreadID: "thread-parent",
		Storage:        codextea.SideChatStorageAttached,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !response.Storage.Attached() {
		t.Fatalf("Start() storage = %v, want attached", response.Storage)
	}
	if response.InheritedName != "Release plan" {
		t.Fatalf("Start() inherited name = %q, want the parent name", response.InheritedName)
	}
	side, err := store.Read(session.ThreadID(response.SideThreadID), true, true)
	if err != nil {
		t.Fatalf("Read(side) error = %v", err)
	}
	if side.AttachedThreadParentID != "thread-parent" {
		t.Fatalf("side attached parent = %q", side.AttachedThreadParentID)
	}
	if side.Metadata.ThreadSource != codextea.AttachedSideChatThreadSource {
		t.Fatalf("side thread source = %q", side.Metadata.ThreadSource)
	}
	if ephemeral, _ := side.Metadata.Extra["ephemeral"].(bool); ephemeral {
		t.Fatalf("saved side chat is marked ephemeral: %#v", side.Metadata.Extra)
	}
	if strings.Contains(side.Metadata.Instructions, sideTemporaryInstructionSuffixText()) {
		t.Fatalf("saved side chat kept the temporary worktree policy: %q", side.Metadata.Instructions)
	}
	if _, ok := sideChatContextCheckpoint(side.Items); !ok {
		t.Fatalf("saved side chat is missing its policy/boundary checkpoint: %#v", side.Items)
	}
	t.Logf("side preview=%q items=%d", side.Preview, len(side.Items))
	if path, err := rollout.FindThreadPath(home, response.SideThreadID, false); err == nil {
		if own, err := rollout.RecordFromPath(path, false); err == nil {
			t.Logf("own path=%q attached=%q items=%d", path, own.AttachedThreadParentID, len(own.Items))
		} else {
			t.Logf("RecordFromPath error = %v", err)
		}
	} else {
		t.Logf("FindThreadPath error = %v", err)
	}
	page, err := store.List(session.ListOptions{
		PageSize:      100,
		SortDirection: session.SortAsc,
		Relation:      &session.RelationFilter{AttachedThreadsOf: "thread-parent"},
	})
	if err != nil {
		t.Logf("store.List error = %v", err)
	} else {
		t.Logf("attached summaries = %d", len(page.Records))
		if len(page.Records) > 0 {
			local := embeddedSideChatLocalRecord(store, page.Records[0])
			if local == nil {
				t.Logf("embeddedSideChatLocalRecord returned nil")
			} else {
				_, ok := sideChatContextCheckpoint(local.Items)
				for i := range local.Items {
					text := local.Items[i].Text
					if len(text) > 80 {
						text = text[:80]
					}
					t.Logf("local item %d role=%q type=%q text=%q", i, local.Items[i].Role, local.Items[i].Type, text)
				}
				t.Logf("local attached=%q items=%d checkpoint=%v", local.AttachedThreadParentID, len(local.Items), ok)
			}
		}
	}

	list := interactiveEmbeddedListSideChats(store)
	choices, err := list("thread-parent")
	if err != nil {
		t.Fatalf("ListSideChats() error = %v", err)
	}
	if len(choices) != 1 {
		t.Fatalf("saved side chats = %#v, want one entry", choices)
	}
	if choices[0].Name != "" || choices[0].Preview != "" {
		t.Fatalf("inherited label was not suppressed: %#v", choices[0])
	}

	// A later name write is an explicit rename even when it repeats the
	// inherited text (Rust side_chat_history).
	if err := rollout.AppendThreadName(home, response.SideThreadID, "Release plan"); err != nil {
		t.Fatalf("AppendThreadName() error = %v", err)
	}
	if _, err := store.UpdateMetadata(side.ID, &session.MetadataPatch{Title: stringPtr("Release plan")}, true); err != nil {
		t.Fatalf("UpdateMetadata(title) error = %v", err)
	}
	choices, err = list("thread-parent")
	if err != nil {
		t.Fatalf("ListSideChats() after rename error = %v", err)
	}
	if len(choices) != 1 || choices[0].Name != "Release plan" {
		t.Fatalf("explicit rename was suppressed: %#v", choices)
	}

	if _, err := coordinator.Close(codextea.SideCloseParams{
		ParentThreadID: "thread-parent",
		SideThreadID:   response.SideThreadID,
		Storage:        codextea.SideChatStorageAttached,
	}); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	archived, err := store.Read(session.ThreadID(response.SideThreadID), true, true)
	if err != nil || archived == nil {
		t.Fatalf("saved side chat was deleted on close: %v", err)
	}
	if !archived.Archived {
		t.Fatalf("saved side chat was not archived on close: %#v", archived)
	}
	choices, err = list("thread-parent")
	if err != nil {
		t.Fatalf("ListSideChats() after close error = %v", err)
	}
	if len(choices) != 0 {
		t.Fatalf("archived side chat is still listed: %#v", choices)
	}
	if _, err := rollout.FindThreadPath(home, response.SideThreadID, true); err != nil {
		t.Fatalf("archived side chat lost its history: %v", err)
	}
}

// TestAttachedSideChatFallsBackToTemporaryLikeRust covers Rust sidebar_fork:
// a parent that cannot store attachments yields a temporary side conversation
// rather than failing the request.
func TestAttachedSideChatFallsBackToTemporaryLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID:        "thread-legacy",
		SessionID: "thread-legacy",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{HistoryMode: string(session.ForkAll)},
		Items: []session.Item{{
			ID: "legacy-user", Type: "message", Role: "user", Text: "Parent question", CreatedAt: now,
		}},
	}); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}

	response, err := newInteractiveLocalSideCoordinator(store).Start(codextea.SideStartParams{
		ParentThreadID: "thread-legacy",
		Storage:        codextea.SideChatStorageAttached,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if response.Storage.Attached() {
		t.Fatalf("legacy parent kept attached storage: %#v", response)
	}
	side, err := store.Read(session.ThreadID(response.SideThreadID), true, true)
	if err != nil {
		t.Fatalf("Read(side) error = %v", err)
	}
	if ephemeral, _ := side.Metadata.Extra["ephemeral"].(bool); !ephemeral {
		t.Fatalf("temporary fallback is not ephemeral: %#v", side.Metadata.Extra)
	}
	if !strings.Contains(side.Metadata.Instructions, sideTemporaryInstructionSuffixText()) {
		t.Fatalf("temporary fallback lost the worktree policy: %q", side.Metadata.Instructions)
	}
}

func sideTemporaryInstructionSuffixText() string {
	return "This temporary side chat cannot retain a worktree."
}
