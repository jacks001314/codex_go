package rollout

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// TestImageGenerationFileRefAndInputImagesSurviveRolloutLikeRust mirrors Rust
// #54398 (`aabf13307a`, "Preserve file references through legacy events,
// persisted rollouts, thread history, and task output summaries") and #54420
// (`592964c0cb`, "Preserve this metadata through completion events, persisted
// thread history, resume, and legacy rollout migration"): a completed
// image-generation item keeps its durable attachment reference (`fileId`) and
// the observed local `inputImages` across the paginated rollout write, the
// resume read, and the client-facing projection.
func TestImageGenerationFileRefAndInputImagesSurviveRolloutLikeRust(t *testing.T) {
	const (
		threadID = "thread-image-file-ref"
		turnID   = "turn-image-file-ref"
		itemID   = "ig_123"
		fileID   = "file_123"
	)
	inputSHA256 := strings.Repeat("ab", 32)
	home := t.TempDir()
	now := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home, ThreadID: threadID, SessionID: threadID,
		HistoryMode: "paginated", CWD: home, ModelProvider: "openai", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted(turnID, now); err != nil {
		t.Fatal(err)
	}
	item := session.Item{
		ID: itemID, Type: "imageGeneration", Status: "completed", CreatedAt: now.Add(time.Second),
		Data: map[string]any{
			"status":                "completed",
			"revisedPrompt":         "draw a square",
			"fileId":                fileID,
			"transparentBackground": true,
			"savedPath":             "/tmp/ig_123.png",
			"inputImages": []any{map[string]any{
				"path":   "/tmp/reference.png",
				"sha256": inputSHA256,
			}},
		},
		Metadata: map[string]any{"turnId": turnID},
	}
	if err := AppendSessionItems(recorder, []session.Item{item}, now); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnComplete(turnID, now.Add(2*time.Second), 2_000); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	coreRaw := completedCoreItemForID(t, path, itemID)
	core := map[string]any{}
	if err := json.Unmarshal(coreRaw, &core); err != nil {
		t.Fatal(err)
	}
	if got := firstNonEmptyString(anyString(core, "file_id", "fileId")); got != fileID {
		t.Fatalf("persisted core item file reference = %q, want %q (core item = %s)", got, fileID, coreRaw)
	}
	coreImages, _ := firstAny(core, "input_images", "inputImages").([]any)
	if len(coreImages) != 1 {
		t.Fatalf("persisted core item inputImages = %#v, want one entry (core item = %s)", firstAny(core, "input_images", "inputImages"), coreRaw)
	}

	// The resume path a reopened thread takes.
	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatal(err)
	}
	var resumed *session.Item
	for i := range record.Items {
		if record.Items[i].ID == itemID {
			resumed = &record.Items[i]
		}
	}
	if resumed == nil {
		t.Fatalf("resumed record has no %s item: %#v", itemID, record.Items)
	}
	if got := firstNonEmptyString(anyString(resumed.Data, "fileId", "file_id")); got != fileID {
		t.Fatalf("resumed item fileId = %q, want %q (data = %#v)", got, fileID, resumed.Data)
	}
	if resumedImages, _ := firstAny(resumed.Data, "inputImages", "input_images").([]any); len(resumedImages) != 1 {
		t.Fatalf("resumed item inputImages = %#v, want one entry", firstAny(resumed.Data, "inputImages", "input_images"))
	}

	// The client-facing projection of the persisted item.
	public, _, kind, err := PublicThreadItemJSONFromCore(coreRaw)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "imageGeneration" {
		t.Fatalf("public item type = %q, want imageGeneration", kind)
	}
	publicItem := map[string]any{}
	if err := json.Unmarshal(public, &publicItem); err != nil {
		t.Fatal(err)
	}
	if got := firstNonEmptyString(anyString(publicItem, "fileId", "file_id")); got != fileID {
		t.Fatalf("public item fileId = %q, want %q (item = %s)", got, fileID, public)
	}
	if publicImages, _ := firstAny(publicItem, "inputImages", "input_images").([]any); len(publicImages) != 1 {
		t.Fatalf("public item inputImages = %#v, want one entry (item = %s)", firstAny(publicItem, "inputImages", "input_images"), public)
	}
}

// TestImageGenerationFileRefSurvivesLegacyRolloutMigrationLikeRust mirrors the
// legacy-migration half of Rust #54398/#54420: the same metadata must survive
// the legacy rollout -> paginated canonicalization that re-encodes every item
// through the core translation.
func TestImageGenerationFileRefSurvivesLegacyRolloutMigrationLikeRust(t *testing.T) {
	const (
		threadID = "thread-image-file-ref-legacy"
		turnID   = "turn-image-file-ref-legacy"
		itemID   = "ig_legacy_123"
		fileID   = "file_legacy_123"
	)
	inputSHA256 := strings.Repeat("cd", 32)
	home := t.TempDir()
	now := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home, ThreadID: threadID, SessionID: threadID,
		HistoryMode: "legacy", CWD: home, ModelProvider: "openai", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted(turnID, now); err != nil {
		t.Fatal(err)
	}
	item := session.Item{
		ID: itemID, Type: "imageGeneration", Status: "completed", CreatedAt: now.Add(time.Second),
		Data: map[string]any{
			"status":                "completed",
			"revisedPrompt":         "draw a circle",
			"fileId":                fileID,
			"transparentBackground": false,
			"savedPath":             "/tmp/ig_legacy_123.png",
			"inputImages": []any{map[string]any{
				"path":   "/tmp/legacy-reference.png",
				"sha256": inputSHA256,
			}},
		},
		Metadata: map[string]any{"turnId": turnID},
	}
	if err := AppendSessionItems(recorder, []session.Item{item}, now); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnComplete(turnID, now.Add(2*time.Second), 2_000); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := CanonicalizeRollout(home, path); err != nil {
		t.Fatalf("CanonicalizeRollout() error = %v", err)
	}
	coreRaw := completedCoreItemForID(t, path, itemID)
	core := map[string]any{}
	if err := json.Unmarshal(coreRaw, &core); err != nil {
		t.Fatal(err)
	}
	if got := firstNonEmptyString(anyString(core, "file_id", "fileId")); got != fileID {
		t.Fatalf("migrated core item file reference = %q, want %q (core item = %s)", got, fileID, coreRaw)
	}
	if images, _ := firstAny(core, "input_images", "inputImages").([]any); len(images) != 1 {
		t.Fatalf("migrated core item inputImages = %#v, want one entry (core item = %s)", firstAny(core, "input_images", "inputImages"), coreRaw)
	}
	record, err := RecordFromPath(path, false)
	if err != nil {
		t.Fatal(err)
	}
	var resumed *session.Item
	for i := range record.Items {
		if record.Items[i].ID == itemID {
			resumed = &record.Items[i]
		}
	}
	if resumed == nil {
		t.Fatalf("migrated record has no %s item: %#v", itemID, record.Items)
	}
	if got := firstNonEmptyString(anyString(resumed.Data, "fileId", "file_id")); got != fileID {
		t.Fatalf("migrated item fileId = %q, want %q (data = %#v)", got, fileID, resumed.Data)
	}
	if resumedImages, _ := firstAny(resumed.Data, "inputImages", "input_images").([]any); len(resumedImages) != 1 {
		t.Fatalf("migrated item inputImages = %#v, want one entry", firstAny(resumed.Data, "inputImages", "input_images"))
	}
}

// completedCoreItemForID returns the `item_completed` payload stored for id.
func completedCoreItemForID(t *testing.T, path string, id string) json.RawMessage {
	t.Helper()
	lines, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range lines {
		if lines[i].Type != "event_msg" || len(lines[i].Payload) == 0 {
			continue
		}
		var payload rolloutEventPayload
		if err := json.Unmarshal(lines[i].Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Type != "item_completed" || len(payload.Item) == 0 {
			continue
		}
		probe := map[string]any{}
		if err := json.Unmarshal(payload.Item, &probe); err != nil {
			t.Fatal(err)
		}
		if anyString(probe, "id") == id {
			return append(json.RawMessage(nil), payload.Item...)
		}
	}
	t.Fatalf("rollout %s has no completed item %s", path, id)
	return nil
}
