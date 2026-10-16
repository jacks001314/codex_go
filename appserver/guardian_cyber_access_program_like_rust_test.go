package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/model"
	"codex_go/state"
	"codex_go/turn"
)

// TestGuardianReviewForwardsParentCyberAccessProgramLikeRust mirrors Rust
// #52689 (`core/tests/suite/guardian_cyber_access_program_tests.rs` and
// `ext/guardian-reviewer/src/settings.rs:32-40`): a Guardian review is issued
// with the reviewed turn's explicit Cyber program, but either Daybreak
// selection is reviewed as Blue while Standard is preserved and no selection
// stays absent. The parent turn keeps its own program; the reviewer reuses the
// same plan, so only the review request changes.
func TestGuardianReviewForwardsParentCyberAccessProgramLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		parent      string
		wantForward string
		wantWire    string
	}{
		{name: "daybreak blue", parent: "daybreak_blue", wantForward: "daybreak_blue", wantWire: "daybreak_blue"},
		{name: "daybreak red reviewed as blue", parent: "daybreak_red", wantForward: "daybreak_blue", wantWire: "daybreak_blue"},
		{name: "standard preserved", parent: "standard", wantForward: "standard", wantWire: "standard"},
		{name: "no selection stays absent", parent: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			const (
				threadID = "thread-cyber-program"
				turnID   = "turn-cyber-program"
				parent   = "gpt-parent"
			)
			var captured *model.AgentRequest
			agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
				copyRequest := *request
				captured = &copyRequest
				return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"reviewed"}`}, nil
			})
			router := NewRuntimeRouter(RuntimeServices{
				Agent:        agent,
				Models:       model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{Slug: parent}}})),
				ThreadStatus: NewThreadStatusManager(),
			})
			if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID, Model: parent}); err != nil {
				t.Fatalf("register active turn: %v", err)
			}
			router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{Model: parent, CyberAccessProgram: testCase.parent})
			reviewer := router.ensureGuardianReviewerWithPrewarm(agent, false)
			if _, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-1", state.Action{Type: "command", Command: "ls", CWD: "/repo"}); err != nil {
				t.Fatalf("Review() error = %v", err)
			}
			if captured == nil {
				t.Fatal("the Guardian review never reached the agent")
			}
			if captured.CyberAccessProgram != testCase.wantForward {
				t.Fatalf("guardian request CyberAccessProgram = %q, want %q", captured.CyberAccessProgram, testCase.wantForward)
			}
			// The forwarded program is the one the reviewer's request projects to
			// the wire (`access_programs.cyber`) for a ChatGPT account, matching
			// the Rust assertion on the review request body.
			programs, err := model.AccessProgramsForAuth(captured.CyberAccessProgram, &auth.AuthDotJSON{AuthMode: "chatgpt"}, model.ApiKeyCyberAccessProgramsUnsupportedProvider)
			if err != nil {
				t.Fatalf("AccessProgramsForAuth() error = %v", err)
			}
			if testCase.wantWire == "" {
				if programs != nil {
					t.Fatalf("guardian access_programs = %#v, want the key omitted", programs)
				}
				return
			}
			if programs == nil || programs.Cyber != testCase.wantWire {
				t.Fatalf("guardian access_programs = %#v, want cyber=%q", programs, testCase.wantWire)
			}
		})
	}
}
