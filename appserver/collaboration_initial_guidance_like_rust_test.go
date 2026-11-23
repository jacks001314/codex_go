package appserver

import (
	"testing"
	"time"

	"codex_go/model"
	"codex_go/session"
)

// TestInitialCollaborationGuidanceSurvivesResumeUntilPlanLikeRust mirrors Rust
// #62896 (e3a398d3e4,
// core/tests/suite/scenarios_model_instructions.rs::initial_collaboration_guidance_survives_resume_until_plan):
// a catalog's `collaboration_modes.initial` applies on a new thread without an
// explicit mode, survives a resume, and is replaced by the Plan guidance and
// then the Default guidance once the client switches modes.
func TestInitialCollaborationGuidanceSurvivesResumeUntilPlanLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-initial-collaboration-guidance")
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy)},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })

	initial := "Work directly on the user's request."
	planText := "Prepare a plan before editing."
	defaultText := "Resume implementation."
	info := &model.ModelInfo{Slug: "gpt-5.5", ModelMessages: &model.ModelMessages{
		InstructionsTemplate: "Help with the migration.",
		CollaborationModes: &model.CollaborationModeMessages{
			Initial: &initial,
			Plan:    &planText,
			Default: &defaultText,
		},
	}}

	// Request 1: new thread, the client has not selected a mode yet.
	item, err := router.collaborationModeWorldStateInputItem(string(threadID), collaborationModeParamsForTest("", "gpt-5.5", ""), info, nil)
	if err != nil {
		t.Fatalf("initial collaboration state error = %v", err)
	}
	assertCollaborationModeItemText(t, item, collaborationModeOpenTag+initial+collaborationModeCloseTag)

	record, err := store.Load(threadID)
	if err != nil {
		t.Fatal(err)
	}
	persisted, ok := collaborationModeWorldStateSessionItemForTurn("turn-1", item, now)
	if !ok {
		t.Fatalf("initial collaboration item was not persistable: %#v", item)
	}
	record.Items = append(record.Items, persisted)
	record.Metadata.LastResponseID = "response-1"
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}

	// Request 2: after a restart the client still sends no mode, so the
	// retained initial guidance stays in history without a new section.
	unchanged, err := router.collaborationModeWorldStateInputItem(string(threadID), collaborationModeParamsForTest("", "gpt-5.5", ""), info, nil)
	if err != nil || unchanged != nil {
		t.Fatalf("resumed initial guidance item = %#v, err = %v", unchanged, err)
	}

	// Requests 3 and 4: entering Plan replaces the guidance, and returning to
	// Default uses the catalog's Default text.
	item, err = router.collaborationModeWorldStateInputItem(string(threadID), collaborationModeParamsForTest("plan", "gpt-5.5", ""), info, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertCollaborationModeItemText(t, item, collaborationModeOpenTag+planText+collaborationModeCloseTag)

	item, err = router.collaborationModeWorldStateInputItem(string(threadID), collaborationModeParamsForTest("default", "gpt-5.5", ""), info, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertCollaborationModeItemText(t, item, collaborationModeOpenTag+defaultText+collaborationModeCloseTag)

	snapshot, ok := collaborationModeSnapshot(collaborationModeParamsForTest("", "gpt-5.5", ""), info, true, false)
	if !ok || snapshot.Mode != string(ModeKindDefault) {
		t.Fatalf("initial collaboration snapshot = %#v, ok = %v; want the Rust Default snapshot", snapshot, ok)
	}
}

// TestInitialCollaborationGuidanceCallerPrecedenceLikeRust covers Rust
// CollaborationModeState::from_collaboration_mode: caller text wins over
// `initial` only when it would win over the catalog Default guidance, and the
// bundled Default preset never masks `initial`.
func TestInitialCollaborationGuidanceCallerPrecedenceLikeRust(t *testing.T) {
	initial := "Work directly on the user's request."
	catalogDefault := "Resume implementation."
	strPtr := func(value string) *string { return &value }

	cases := []struct {
		name           string
		catalog        *model.CollaborationModeMessages
		supplied       string
		want           string
		wantHasSection bool
	}{
		{
			name:           "initial applies without caller text",
			catalog:        &model.CollaborationModeMessages{Initial: &initial, Default: &catalogDefault},
			want:           initial,
			wantHasSection: true,
		},
		{
			name:           "caller text without catalog default wins",
			catalog:        &model.CollaborationModeMessages{Initial: &initial},
			supplied:       "Team-specific instructions.",
			want:           "Team-specific instructions.",
			wantHasSection: true,
		},
		{
			name:           "catalog default replaces caller text",
			catalog:        &model.CollaborationModeMessages{Initial: &initial, Default: &catalogDefault},
			supplied:       "Team-specific instructions.",
			want:           initial,
			wantHasSection: true,
		},
		{
			name:           "bundled default preset does not mask initial",
			catalog:        &model.CollaborationModeMessages{Initial: &initial},
			supplied:       collaborationModeDefaultInstructions,
			want:           initial,
			wantHasSection: true,
		},
		{
			name:           "bundled default preset alone emits nothing",
			catalog:        &model.CollaborationModeMessages{},
			supplied:       collaborationModeDefaultInstructions,
			wantHasSection: false,
		},
		{
			name:           "empty initial emits nothing",
			catalog:        &model.CollaborationModeMessages{Initial: strPtr("")},
			wantHasSection: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := &model.ModelInfo{Slug: "gpt-5.5", ModelMessages: &model.ModelMessages{CollaborationModes: tc.catalog}}
			got, ok := collaborationModeInstructions(collaborationModeParamsForTest("", "gpt-5.5", tc.supplied), info, true, false)
			if ok != tc.wantHasSection {
				t.Fatalf("has section = %v, want %v (got %q)", ok, tc.wantHasSection, got)
			}
			if ok && got != tc.want {
				t.Fatalf("instructions = %q, want %q", got, tc.want)
			}
		})
	}
}
