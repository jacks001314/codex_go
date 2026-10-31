package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #43447 asks the model-owned Guardian computer-use policy wherever review
// evidence or turn metadata decides whether computer use needs review. The
// turn's `x-codex-turn-metadata` must therefore carry the policy's answer, not
// the legacy node_repl_auto_review_required bit alone.
func TestTurnMetadataUsesComputerUsePolicyOverLegacyFlagLikeRust(t *testing.T) {
	adaptive := model.GuardianReviewModeAdaptive
	disabled := model.GuardianReviewModeDisabled
	catalog := model.ModelsResponse{Models: []model.ModelInfo{
		{
			Slug:                       "policy-disabled",
			Visibility:                 model.VisibilityList,
			SupportedInAPI:             true,
			NodeReplAutoReviewRequired: true,
			Guardian:                   &model.GuardianModelPolicy{ComputerUse: &disabled},
		},
		{
			Slug:           "policy-adaptive",
			Visibility:     model.VisibilityList,
			SupportedInAPI: true,
			Guardian:       &model.GuardianModelPolicy{ComputerUse: &adaptive},
		},
		{
			Slug:                       "legacy-only",
			Visibility:                 model.VisibilityList,
			SupportedInAPI:             true,
			NodeReplAutoReviewRequired: true,
		},
	}}
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	now := time.Now().UTC()
	const threadID = "thread-turn-metadata-policy"
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: t.TempDir()},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Models:       model.NewModelService(model.NewStaticModelsManager(catalog)),
	})

	for _, testCase := range []struct {
		model string
		want  bool
	}{
		{model: "policy-disabled", want: false},
		{model: "policy-adaptive", want: true},
		{model: "legacy-only", want: true},
	} {
		t.Run(testCase.model, func(t *testing.T) {
			params := &turn.TurnStartParams{ThreadID: threadID, Prompt: "hello", Model: testCase.model}
			runConfig, err := router.appTurnConfig(context.Background(), threadID, "turn-"+testCase.model, "", params, now.UnixMilli(), nil)
			if err != nil {
				t.Fatalf("appTurnConfig() error = %v", err)
			}
			if runConfig == nil {
				t.Fatal("appTurnConfig() returned nil")
			}
			raw := runConfig.ClientMetadata[codexapi.ClientCodexTurnMetadataHeader]
			if raw == "" {
				t.Fatalf("client metadata = %#v, want the turn metadata header", runConfig.ClientMetadata)
			}
			var document map[string]any
			if err := json.Unmarshal([]byte(raw), &document); err != nil {
				t.Fatalf("turn metadata is not JSON: %v", err)
			}
			required, ok := document[codexapi.NodeReplAutoReviewRequiredKey].(bool)
			if !ok {
				t.Fatalf("turn metadata = %#v, want %q", document, codexapi.NodeReplAutoReviewRequiredKey)
			}
			if required != testCase.want {
				t.Fatalf("node_repl_auto_review_required = %v, want %v", required, testCase.want)
			}
		})
	}
}
