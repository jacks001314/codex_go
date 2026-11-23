package appserver

import (
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
)

// Mirrors Rust #62588: the host-selected Guardian provider supplies Guardian's
// preferred reviewer, so an API-key provider prefers Luna and an OpenAI-auth
// provider prefers the auto-review model.
func TestGuardianProviderPreferredReviewModelLikeRust(t *testing.T) {
	cases := []struct {
		name string
		info model.ProviderInfo
		want string
	}{
		{
			name: "api key provider",
			info: model.ProviderInfo{EnvKey: "GUARDIAN_API_KEY"},
			want: model.APIKeyApprovalReviewPreferredModel,
		},
		{
			name: "openai auth provider",
			info: model.ProviderInfo{RequiresOpenAIAuth: true},
			want: model.DefaultApprovalReviewPreferredModel,
		},
	}
	for _, testCase := range cases {
		provider := GuardianInferenceProvider{ID: "guardian", Info: testCase.info}
		if got := provider.preferredReviewModel(); got != testCase.want {
			t.Fatalf("%s preferred reviewer = %q, want %q", testCase.name, got, testCase.want)
		}
	}

	router := NewRuntimeRouter(RuntimeServices{
		GuardianModelProvider: &GuardianInferenceProvider{ID: "guardian", Info: model.ProviderInfo{EnvKey: "GUARDIAN_API_KEY"}},
	})
	if got := router.guardianReviewPreferredModel(nil, nil); got != model.APIKeyApprovalReviewPreferredModel {
		t.Fatalf("router preferred reviewer = %q, want the Guardian provider's", got)
	}
	if got := router.guardianReviewProviderID("actor-provider"); got != "guardian" {
		t.Fatalf("Guardian review provider = %q, want guardian", got)
	}

	actorOnly := NewRuntimeRouter(RuntimeServices{})
	if got := actorOnly.guardianReviewProviderID("actor-provider"); got != "actor-provider" {
		t.Fatalf("actor review provider = %q, want the actor's provider", got)
	}
}

// Mirrors Rust #62588 Config::guardian_policy_config: with a host-selected
// Guardian provider only the managed policy requirement applies, so policy text
// from the actor's configuration cannot steer Guardian.
func TestGuardianProviderUsesManagedPolicyOnlyLikeRust(t *testing.T) {
	configPolicy := "config policy"
	cfg := &config.Config{Values: map[string]any{
		"auto_review": map[string]any{"policy": configPolicy},
	}}
	if instructions := guardianReviewInstructions(cfg, nil, GuardianConversationHistoryRetrieval, false); !strings.Contains(instructions, configPolicy) {
		t.Fatal("without a Guardian provider the configuration policy must reach the reviewer")
	}
	if instructions := guardianReviewInstructions(cfg, nil, GuardianConversationHistoryRetrieval, true); strings.Contains(instructions, configPolicy) {
		t.Fatal("a host-selected Guardian provider must ignore the actor's configuration policy")
	}

	managed := "managed policy"
	managedCfg := &config.Config{
		Values:       map[string]any{"auto_review": map[string]any{"policy": configPolicy}},
		Requirements: &config.ConfigRequirements{GuardianPolicyConfig: &managed},
	}
	instructions := guardianReviewInstructions(managedCfg, nil, GuardianConversationHistoryRetrieval, true)
	if !strings.Contains(instructions, managed) || strings.Contains(instructions, configPolicy) {
		t.Fatalf("managed-policy instructions = %q, want only the managed policy", instructions)
	}
}

// Mirrors Rust #62588 GuardianInferenceContext sharing: review sessions run on
// the Guardian provider's runner and start fresh chains, so a provider switch
// never continues the actor's reviewer conversation.
func TestGuardianReviewerUsesGuardianAgentLikeRust(t *testing.T) {
	actorAgent := &guardianRecordingAgent{}
	guardianAgent := &guardianRecordingAgent{}
	reviewer, ok := newModelGuardianReviewer(actorAgent).(*modelGuardianReviewer)
	if !ok {
		t.Fatal("newModelGuardianReviewer() did not return a model reviewer")
	}
	actorSession := reviewer.sessionFor("thread-1")
	if actorSession == nil || actorSession.agent != actorAgent {
		t.Fatal("reviewer did not start on the actor's runner")
	}

	reviewer.useGuardianAgent(guardianAgent)
	session := reviewer.sessionFor("thread-1")
	if session == nil || session.agent != guardianAgent {
		t.Fatal("reviewer session did not switch to the Guardian provider's runner")
	}
	if session == actorSession {
		t.Fatal("provider switch continued the actor's reviewer chain")
	}
}
