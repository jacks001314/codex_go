package appserver

import (
	"os"
	"testing"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
	"codex_go/turn"
)

func TestGuardianReviewEndpointSelectionLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
[features.guardianv2]
free_guardian = true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config:  config.NewConfigService(home),
		Account: auth.NewAccountManager(),
	})
	if err := router.threads.RegisterTurn("thread", "turn", nil, 0, &turn.TurnStartParams{
		ThreadID: "thread",
	}); err != nil {
		t.Fatal(err)
	}
	if !router.threads.UpdateTurn("thread", "turn", func(active *activeRuntimeTurn) {
		active.RunConfig = &appTurnRunConfig{ProviderID: model.OpenAIProviderID}
	}) {
		t.Fatal("active turn was not found")
	}

	router.requireAccount().ApplyAuthSnapshot(&auth.AuthDotJSON{AuthMode: "chatgpt"})
	if got := router.guardianReviewEndpointForTurn("thread", "turn"); got != model.ResponsesEndpointGuardian {
		t.Fatalf("ChatGPT endpoint = %d, want Guardian", got)
	}

	router.requireAccount().ApplyAuthSnapshot(&auth.AuthDotJSON{AuthMode: "api-key", OpenAIAPIKey: "sk-test"})
	if got := router.guardianReviewEndpointForTurn("thread", "turn"); got != model.ResponsesEndpointResponses {
		t.Fatalf("API-key endpoint = %d, want Responses", got)
	}
}

func TestGuardianReviewEndpointRequiresFreeGuardian(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		Config:  config.NewConfigService(home),
		Account: auth.NewAccountManager(),
	})
	if err := router.threads.RegisterTurn("thread", "turn", nil, 0, &turn.TurnStartParams{
		ThreadID: "thread",
	}); err != nil {
		t.Fatal(err)
	}
	router.requireAccount().ApplyAuthSnapshot(&auth.AuthDotJSON{AuthMode: "chatgpt"})
	if got := router.guardianReviewEndpointForTurn("thread", "turn"); got != model.ResponsesEndpointResponses {
		t.Fatalf("disabled endpoint = %d, want Responses", got)
	}
}
