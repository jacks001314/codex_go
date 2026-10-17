package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/state"
)

// Rust #52535 (`core/src/agent/control/spawn.rs:1129-1139`,
// `guardian-context/src/transcript.rs:42-51`): a manual action approval copied
// from the parent carries `inherited_user_message` provenance, and
// `is_inherited_manual_approval` keeps that copied approval out of a review
// while the child's own approval stays visible.
func copiedManualApprovalItem(id string, action string, createdAt time.Time) session.Item {
	text := state.DeniedActionApprovalPrefix + "\n\nApproved action:\n" + action
	return session.Item{
		ID: id, Type: "message", Role: "developer", Text: text,
		Content:   []session.ContentPart{{Type: "input_text", Text: text}},
		CreatedAt: createdAt,
	}
}

func TestGuardianApprovedActionItemCarriesContentKindLikeRust(t *testing.T) {
	event := state.Event{ID: "review-1", TurnID: "turn-1", Action: state.Action{Type: "command", Command: "pwd", CWD: "/repo"}}
	item := guardianApprovedActionSessionItem(event, "approved", fixedTime())
	kinds := sessionItemContentItemKinds(&item)
	if len(kinds) != 1 || kinds[0] != "guardian.approved_action" {
		t.Fatalf("approved action content kinds = %#v", kinds)
	}
}

func inheritedMetadataItem(t *testing.T, item session.Item, raw string) session.Item {
	t.Helper()
	if !json.Valid([]byte(raw)) {
		t.Fatalf("invalid harness metadata %s", raw)
	}
	if item.Data == nil {
		item.Data = map[string]any{}
	}
	item.Data[harnessMetadataKey] = json.RawMessage(raw)
	return item
}

// TestMarkInheritedUserMessagesMarksDeveloperApprovalLikeRust pins requirement
// (1) of the lane-C port: the copied developer approval message gains the
// inherited provenance `is_inherited_manual_approval` needs, while ordinary
// developer commentary is left alone so it keeps reaching the child's retained
// context.
func TestMarkInheritedUserMessagesMarksDeveloperApprovalLikeRust(t *testing.T) {
	order := uint64(3)
	items := []session.Item{
		inheritedMetadataItem(t, copiedManualApprovalItem("approval-1", `{"action":"rm -rf"}`, fixedTime()), `{"user_input_order":3}`),
		{ID: "commentary-1", Type: "message", Role: "developer", Text: "inherited commentary"},
		{ID: "user-1", Type: "message", Role: "user", Text: "Keep files private."},
		{ID: "assistant-1", Type: "message", Role: "assistant", Text: "Understood."},
	}
	markInheritedUserMessages(items)

	approval := harnessMetadataFromSessionItem(&items[0])
	if approval == nil || !approval.InheritedUserMessage {
		t.Fatalf("copied developer approval metadata = %#v, want inherited_user_message", approval)
	}
	// Rust spawn.rs: only a message with the `user` role keeps the parent's
	// acceptance order, so a copied approval loses it like every other position.
	if approval.UserInputOrder != nil {
		t.Fatalf("copied developer approval kept the parent's counter: %#v", approval)
	}
	if metadata := harnessMetadataFromSessionItem(&items[1]); metadata != nil && metadata.InheritedUserMessage {
		t.Fatalf("plain developer commentary was marked inherited: %#v", metadata)
	}
	if metadata := harnessMetadataFromSessionItem(&items[2]); metadata == nil || !metadata.InheritedUserMessage {
		t.Fatalf("copied user message metadata = %#v, want inherited_user_message", metadata)
	}
	if metadata := harnessMetadataFromSessionItem(&items[3]); metadata == nil || !metadata.InheritedUserMessage {
		t.Fatalf("copied assistant message metadata = %#v, want inherited_user_message", metadata)
	}
	if want := uint64(3); order != want {
		t.Fatalf("fixture order = %d, want %d", order, want)
	}
}

// TestGuardianTranscriptExcludesInheritedDeveloperApprovalLikeRust pins
// requirement (2): the copied approval never reaches the reviewer, the child's
// own approval still does, and the ordinary conversation keeps its order.
func TestGuardianTranscriptExcludesInheritedDeveloperApprovalLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := "123e4567-e89b-42d3-a456-426614174200"
	now := fixedTime()
	items := []session.Item{
		{ID: "user-1", Type: "message", Role: "user", Text: "Keep files private.", CreatedAt: now},
		inheritedMetadataItem(t, copiedManualApprovalItem("approval-inherited", `{"action":"inherited-action"}`, now), `{"inherited_user_message":true}`),
		{ID: "assistant-1", Type: "message", Role: "assistant", Text: "Understood.", CreatedAt: now},
		copiedManualApprovalItem("approval-own", `{"action":"own-action"}`, now),
		{ID: "user-2", Type: "message", Role: "user", Text: "Also keep backups private.", CreatedAt: now},
	}
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli", ModelProvider: "openai"},
		Items:     items,
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	lines := router.guardianReviewTranscript(threadID)
	joined := strings.Join(lines, "\n")

	if strings.Contains(joined, "inherited-action") {
		t.Fatalf("inherited manual approval reached the review transcript: %q", joined)
	}
	if !strings.Contains(joined, "own-action") {
		t.Fatalf("the child's own manual approval is missing from the transcript: %q", joined)
	}
	if !strings.HasPrefix(strings.TrimSpace(lastLine(lines, "own-action")), "developer: "+state.DeniedActionApprovalPrefix) {
		t.Fatalf("the child's own approval lost its developer attribution: %q", joined)
	}
	userFirst := lineIndex(lines, "user: Keep files private.")
	assistant := lineIndex(lines, "assistant: Understood.")
	userSecond := lineIndex(lines, "user: Also keep backups private.")
	if userFirst < 0 || assistant < 0 || userSecond < 0 {
		t.Fatalf("ordinary conversation entries are missing: %q", joined)
	}
	if !(userFirst < assistant && assistant < userSecond) {
		t.Fatalf("conversation order = (%d, %d, %d), want the original order: %q", userFirst, assistant, userSecond, joined)
	}
}

func lineIndex(lines []string, needle string) int {
	for index, line := range lines {
		if strings.Contains(line, needle) {
			return index
		}
	}
	return -1
}

func lastLine(lines []string, needle string) string {
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.Contains(lines[index], needle) {
			return lines[index]
		}
	}
	return ""
}
