package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/model"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

type guardianAgentFunc func(context.Context, *model.AgentRequest) (*model.AgentResponse, error)

// Mirror of Rust's core/src/guardian/permissions.rs: the review prompt carries
// the reviewed turn's denied read paths and globs, so the reviewer cannot approve
// an escalation whose purpose is to read them.
func TestModelGuardianReviewerIncludesTurnPermissionEvidenceLikeRust(t *testing.T) {
	var captured *model.AgentRequest
	cwd := t.TempDir()
	profile := sandbox.WorkspaceWritePermissionProfile()
	profile.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "path", Path: filepath.Join(cwd, "secret")},
		Access: sandbox.FileSystemAccessDeny,
	}}
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			captured = request
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store: state.NewReviewStore(),
		turnPermissionProfile: func(threadID, turnID string) (*sandbox.PermissionProfile, string, *string) {
			environmentID := "env-1"
			return &profile, cwd, &environmentID
		},
	}
	if _, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "command", Command: "ls", CWD: cwd}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if captured == nil || !strings.Contains(captured.Prompt, ">>> PARENT TURN PERMISSION CONTEXT START") {
		t.Fatalf("prompt missing the permission context:\n%#v", captured)
	}
	if !strings.Contains(captured.Prompt, "- path `"+filepath.Join(cwd, "secret")+"`") {
		t.Fatalf("prompt missing the denied read path:\n%s", captured.Prompt)
	}
	if !strings.Contains(captured.Prompt, "do not approve escalation whose purpose is to read them") {
		t.Fatalf("prompt missing the restriction guidance:\n%s", captured.Prompt)
	}
	// The resolved environment's selection id names the scope of the evidence
	// (Rust's GuardianPermissionContext::environment_id).
	if !strings.Contains(captured.Prompt, `The active permission profile for environment "env-1"`) {
		t.Fatalf("prompt missing the environment scope:\n%s", captured.Prompt)
	}

	// A profile without deny entries contributes no section.
	open := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			captured = request
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store: state.NewReviewStore(),
		turnPermissionProfile: func(threadID, turnID string) (*sandbox.PermissionProfile, string, *string) {
			readOnly := sandbox.ReadOnlyPermissionProfile()
			return &readOnly, cwd, nil
		},
	}
	if _, _, err := open.Review(context.Background(), "thread-1", "turn-1", "call-2", state.Action{Type: "command", Command: "ls", CWD: cwd}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if strings.Contains(captured.Prompt, "PARENT TURN PERMISSION CONTEXT") {
		t.Fatalf("a profile without deny entries rendered a section:\n%s", captured.Prompt)
	}
}

func TestModelGuardianReviewerSetsPermissionProfile(t *testing.T) {
	var captured *model.AgentRequest
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			captured = request
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store: state.NewReviewStore(),
		permissionProfile: func(threadID, turnID string) *sandbox.PermissionProfile {
			return &sandbox.PermissionProfile{SandboxPolicy: sandbox.NewReadOnlyPolicy()}
		},
	}
	if _, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "command", Command: "ls", CWD: "/repo"}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if captured == nil || captured.PermissionProfile == nil || captured.PermissionProfile.SandboxPolicy == nil || captured.PermissionProfile.SandboxPolicy.Kind != sandbox.SandboxReadOnly {
		t.Fatalf("captured permission profile = %#v", captured)
	}
}

// TestModelGuardianReviewerSkipsStaleScoresOverMaxToolCallLag mirrors Rust
// extension_tests.rs approval review at, above, and after recovering from the
// configured lag limit (#39001): the reviewer skips the model sample when the
// latest tool call lags the latest scored tool call by more than
// max_tool_call_lag, and recovers once the scored call catches up.
func TestModelGuardianReviewerSkipsStaleScoresOverMaxToolCallLag(t *testing.T) {
	calls := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store:          state.NewReviewStore(),
		breaker:        state.NewCircuitBreaker(),
		scoreProgress:  map[string]*guardianScoreProgress{},
		maxToolCallLag: 2,
	}
	review := func() (state.ReviewDecision, error) {
		decision, _, err := reviewer.Review(context.Background(), "thread-lag", "turn-lag", "call-lag", state.Action{Type: "command", Command: "ls", CWD: "/repo"})
		return decision, err
	}
	decision, err := review()
	if err != nil || decision != state.DecisionApproved || calls != 1 {
		t.Fatalf("first review = %v %v calls=%d, want approved with 1 sample", decision, err, calls)
	}
	progress := reviewer.scoreProgress["thread-lag"]
	// Two tool calls run without a stored score (lag = 3-1 = 2): review proceeds.
	progress.latestToolCall = 3
	decision, err = review()
	if err != nil || decision != state.DecisionApproved || calls != 2 {
		t.Fatalf("review at lag limit = %v %v calls=%d, want approved with sample", decision, err, calls)
	}
	// Many tool calls run without a stored score (lag = 20-4 = 16): review skips.
	progress.latestToolCall = 20
	decision, err = review()
	if err != nil || decision != state.DecisionDenied || calls != 2 {
		t.Fatalf("review above lag = %v %v calls=%d, want denied without sampling", decision, err, calls)
	}
	// The scored call catches up (lag = 20-18 = 2): review proceeds again.
	progress.latestScoredToolCall = 18
	decision, err = review()
	if err != nil || decision != state.DecisionApproved || calls != 3 {
		t.Fatalf("review after recovery = %v %v calls=%d, want approved with sample", decision, err, calls)
	}
}

func (f guardianAgentFunc) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	return f(ctx, request)
}

func TestGuardianTurnStartSelectsWindowsProxyPreserveMode(t *testing.T) {
	if !guardianTurnStart(&turn.TurnStartParams{Originator: "guardian"}) {
		t.Fatal("guardian originator was not detected")
	}
	if !guardianTurnStart(&turn.TurnStartParams{ResponsesAPIMetadata: map[string]string{"x-openai-subagent": "guardian"}}) {
		t.Fatal("guardian subagent metadata was not detected")
	}
	if guardianTurnStart(&turn.TurnStartParams{Originator: "review"}) {
		t.Fatal("ordinary review turn selected Guardian preserve mode")
	}
}

type guardianPrewarmAgent struct {
	mu            sync.Mutex
	requests      []*model.AgentRequest
	prewarms      int
	prewarmErr    error
	websocketRuns int
	websocketErr  error
}

func (a *guardianPrewarmAgent) Prewarm(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prewarms++
	if a.prewarmErr != nil {
		return nil, a.prewarmErr
	}
	return &model.AgentResponse{ResponseID: "warm-1"}, nil
}

func (a *guardianPrewarmAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	copyRequest := *request
	a.requests = append(a.requests, &copyRequest)
	return &model.AgentResponse{ResponseID: fmt.Sprintf("review-%d", len(a.requests)), Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
}

func (a *guardianPrewarmAgent) RunWebSocket(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.mu.Lock()
	a.websocketRuns++
	err := a.websocketErr
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return a.Run(ctx, request)
}

func (a *guardianPrewarmAgent) snapshot() (int, int, []*model.AgentRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prewarms, a.websocketRuns, append([]*model.AgentRequest(nil), a.requests...)
}

func TestEnsureGuardianReviewerPrewarmSkip(t *testing.T) {
	agent := &guardianPrewarmAgent{}
	router := &RuntimeRouter{}
	router.ensureGuardianReviewerWithPrewarm(agent, false)
	time.Sleep(50 * time.Millisecond)
	if prewarms, _, _ := agent.snapshot(); prewarms != 0 {
		t.Fatalf("prewarm should be skipped, got %d", prewarms)
	}

	router.services.GuardianReviewer = nil
	router.ensureGuardianReviewerWithPrewarm(agent, true)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if prewarms, _, _ := agent.snapshot(); prewarms > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("prewarm should run")
}

func TestTurnIsFullAccess(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"approval_policy": "never", "sandbox_mode": "danger-full-access"}}
	cwd := t.TempDir()
	params := &turn.TurnStartParams{CWD: cwd}
	if !turnIsFullAccess(cfg, cwd, params) {
		t.Fatal("approval never with danger-full-access should be Full Access")
	}
	cfg.Values["approval_policy"] = "on-request"
	if turnIsFullAccess(cfg, cwd, params) {
		t.Fatal("on-request approval should not be Full Access")
	}
}

func TestTurnIsFullAccessRequiresEverySelectedEnvironmentFullAccess(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"approval_policy": "never", "sandbox_mode": "danger-full-access"}}
	cwd := t.TempDir()
	params := &turn.TurnStartParams{CWD: cwd}
	fullAccess := sandbox.FullAccessPermissionProfile()
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(fullAccess)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	readyEnvironment := map[string]any{
		"environmentId": "env-full",
		"config": map[string]any{
			"state": "ready",
			"config": map[string]any{
				"permission_profile": profileJSON,
			},
		},
	}
	params.Environments = []map[string]any{readyEnvironment}
	if !turnIsFullAccess(cfg, cwd, params) {
		t.Fatal("ready full-access environment should preserve Full Access")
	}
	params.Environments = append(params.Environments, map[string]any{
		"environmentId": "env-pending",
		"config":        map[string]any{"state": "pending"},
	})
	if turnIsFullAccess(cfg, cwd, params) {
		t.Fatal("pending selected environment should not be Full Access")
	}
	params.Environments = []map[string]any{{
		"environmentId": "env-failed",
		"config":        map[string]any{"state": "failed", "error": "unavailable"},
	}}
	if turnIsFullAccess(cfg, cwd, params) {
		t.Fatal("failed selected environment should not be Full Access")
	}
}

func TestModelGuardianReviewerShortCircuitsInFullAccess(t *testing.T) {
	reviewer := newModelGuardianReviewer(guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		t.Fatal("reviewer should not sample in Full Access")
		return nil, nil
	})).(*modelGuardianReviewer)
	reviewer.fullAccess = func(threadID, turnID string) bool { return true }
	decision, _, err := reviewer.Review(context.Background(), "thread-full", "turn-full", "", state.Action{Type: "command", Command: "echo hi", CWD: "/tmp"})
	if err != nil {
		t.Fatalf("Review error = %v", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("decision = %v, want approved", decision)
	}
}

func TestModelGuardianReviewerAutoAcceptsUserModeNodeReplJS(t *testing.T) {
	reviewer := newModelGuardianReviewer(guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		t.Fatal("reviewer should not sample in user approval mode")
		return nil, nil
	})).(*modelGuardianReviewer)
	reviewer.approvalsReviewer = func(threadID, turnID string) string { return string(config.ApprovalsReviewerUser) }
	decision, _, err := reviewer.Review(context.Background(), "thread-user", "turn-user", "", state.Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "js"})
	if err != nil {
		t.Fatalf("Review error = %v", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("decision = %v, want approved", decision)
	}
}

func TestModelGuardianReviewerMapsAssessmentDecision(t *testing.T) {
	for _, tc := range []struct {
		name     string
		outcome  string
		decision state.ReviewDecision
	}{
		{name: "allow", outcome: "allow", decision: state.DecisionApproved},
		{name: "deny", outcome: "deny", decision: state.DecisionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reviewer := &modelGuardianReviewer{agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
				if request.TaskKind != model.AgentTaskReview || request.Originator != "guardian" || request.ClientMetadata["x-openai-subagent"] != "guardian" || request.ClientMetadata["parent_turn_id"] != "turn-1" || request.OutputSchema == nil {
					t.Fatalf("request = %#v", request)
				}
				return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"` + tc.outcome + `","rationale":"reviewed"}`}, nil
			})}
			decision, reason, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"})
			// Rust's denied completion always renders the rejection feedback
			// wrapper, using the bundled instructions when the catalog omits them.
			wantReason := "reviewed"
			if tc.outcome == "deny" {
				wantReason = state.RenderGuardianRejection("reviewed", state.GuardianRejectionInstructions())
			}
			if err != nil || decision != tc.decision || reason != wantReason {
				t.Fatalf("decision=%s reason=%q err=%v", decision, reason, err)
			}
		})
	}
}

func TestModelGuardianReviewerEmitsTurnTriggerMetadata(t *testing.T) {
	reviewer := &modelGuardianReviewer{agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		metadataJSON := strings.TrimSpace(request.ClientMetadata[codexapi.ClientCodexTurnMetadataHeader])
		if metadataJSON == "" {
			t.Fatalf("guardian review missing turn metadata: %#v", request.ClientMetadata)
		}
		var metadata map[string]any
		if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
			t.Fatalf("turn metadata json error = %v metadata=%q", err, metadataJSON)
		}
		if metadata["turn_trigger"] != "guardian_review" {
			t.Fatalf("turn_trigger = %#v metadata=%#v", metadata["turn_trigger"], metadata)
		}
		if request.ClientMetadata["x-openai-subagent"] != "guardian" ||
			request.ClientMetadata["parent_turn_id"] != "turn-trigger" ||
			request.ClientMetadata["target_item_id"] != "call-trigger" {
			t.Fatalf("client metadata = %#v", request.ClientMetadata)
		}
		return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"reviewed"}`}, nil
	})}
	if _, _, err := reviewer.Review(context.Background(), "thread-trigger", "turn-trigger", "call-trigger", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"}); err != nil {
		t.Fatalf("Review error = %v", err)
	}
}

func TestGuardianUsesCatalogAutoReviewModelOverrideLikeRust(t *testing.T) {
	const (
		threadID    = "thread-auto-review-model"
		turnID      = "turn-auto-review-model"
		parent      = "remote-auto-review-parent"
		reviewModel = "remote-auto-review-reviewer"
	)
	var captured *model.AgentRequest
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		copyRequest := *request
		captured = &copyRequest
		return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"catalog override"}`}, nil
	})
	router := NewRuntimeRouter(RuntimeServices{
		Agent: agent,
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
			Slug: parent, AutoReviewModelOverride: reviewModel,
		}}})),
		ThreadStatus: NewThreadStatusManager(),
	})
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID, Model: parent}); err != nil {
		t.Fatalf("register active turn: %v", err)
	}
	info := router.modelInfoForRuntimeWithConfig(parent, nil)
	if info == nil || info.AutoReviewModelOverride != reviewModel {
		t.Fatalf("catalog model info = %#v", info)
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{
		Model:                   parent,
		AutoReviewModelOverride: info.AutoReviewModelOverride,
	})
	reviewer := router.ensureGuardianReviewer(agent)
	decision, _, err := reviewer.Review(context.Background(), threadID, turnID, "patch-call", state.Action{Type: "apply_patch", CWD: t.TempDir(), Files: []string{"override.txt"}})
	if err != nil || decision != state.DecisionApproved {
		t.Fatalf("Guardian review decision=%s err=%v", decision, err)
	}
	if captured == nil || captured.Model != reviewModel {
		t.Fatalf("Guardian request = %#v, want model %q", captured, reviewModel)
	}
}

// TestGuardianReviewRequestUsesSelectedModelAndEffortLikeRust mirrors Rust
// #46292: a review samples with the catalog-selected review model and its
// request-level effort - `low` when that model supports it, its default effort
// otherwise, and the parent model plus the parent's selected effort when the
// catalog does not list the preferred review model.
func TestGuardianReviewRequestUsesSelectedModelAndEffortLikeRust(t *testing.T) {
	const (
		threadID     = "thread-review-selection"
		turnID       = "turn-review-selection"
		parentModel  = "gpt-parent"
		preferredCal = model.DefaultApprovalReviewPreferredModel
	)
	parentInfo := model.ModelInfo{
		Slug:                     parentModel,
		Visibility:               "list",
		SupportedInAPI:           true,
		DefaultReasoningLevel:    "medium",
		SupportedReasoningLevels: []string{"low", "medium", "high"},
	}
	reviewInfo := func(modelID string, defaultLevel string, levels []string) model.ModelInfo {
		return model.ModelInfo{
			Slug:                     modelID,
			Visibility:               "list",
			SupportedInAPI:           true,
			DefaultReasoningLevel:    defaultLevel,
			SupportedReasoningLevels: levels,
		}
	}
	tests := []struct {
		name         string
		models       []model.ModelInfo
		parentEffort string
		wantModel    string
		wantEffort   string
	}{
		{
			name:         "review model supports low",
			models:       []model.ModelInfo{parentInfo, reviewInfo(preferredCal, "medium", []string{"low", "medium"})},
			parentEffort: "high",
			wantModel:    preferredCal,
			wantEffort:   "low",
		},
		{
			name:         "review model without low keeps its default",
			models:       []model.ModelInfo{parentInfo, reviewInfo(preferredCal, "high", []string{"medium", "high"})},
			parentEffort: "medium",
			wantModel:    preferredCal,
			wantEffort:   "high",
		},
		{
			name:         "missing review model falls back to the parent",
			models:       []model.ModelInfo{parentInfo},
			parentEffort: "high",
			wantModel:    parentModel,
			wantEffort:   "low",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var captured *model.AgentRequest
			agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
				copyRequest := *request
				captured = &copyRequest
				return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"selection"}`}, nil
			})
			router := NewRuntimeRouter(RuntimeServices{
				Agent:        agent,
				Models:       model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: testCase.models})),
				ThreadStatus: NewThreadStatusManager(),
			})
			effort := testCase.parentEffort
			if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
				ThreadID: threadID,
				Model:    parentModel,
				Effort:   &effort,
			}); err != nil {
				t.Fatalf("register active turn: %v", err)
			}
			router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{Model: parentModel})
			reviewer := router.ensureGuardianReviewer(agent)
			decision, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-selection", state.Action{
				Type: "mcp_tool_call", Server: "apps", ToolName: "calendar",
			})
			if err != nil || decision != state.DecisionApproved {
				t.Fatalf("Guardian review decision=%s err=%v", decision, err)
			}
			if captured == nil || captured.Model != testCase.wantModel || captured.ReasoningEffort != testCase.wantEffort {
				t.Fatalf("Guardian request = %#v, want model %q effort %q", captured, testCase.wantModel, testCase.wantEffort)
			}
		})
	}
}

// TestGuardianReviewRequestCarriesThePolicyInstructionsLikeRust mirrors Rust
// build_guardian_review_session_config: a review's base instructions are the
// resolved policy substituted into the resolved template and terminated by the
// output contract, read from the reviewer's catalog entry - the review model's
// when the catalog lists the preferred review model, and the parent's otherwise.
func TestGuardianReviewRequestCarriesThePolicyInstructionsLikeRust(t *testing.T) {
	const (
		threadID    = "thread-review-instructions"
		turnID      = "turn-review-instructions"
		parentModel = "gpt-parent"
	)
	preferred := model.DefaultApprovalReviewPreferredModel
	reviewPolicy := "Review model policy."
	reviewTemplate := "Judge the action against:\n{{ tenant_policy_config }}"
	parentPolicy := "Parent model policy."
	parentTemplate := "Parent template:\n{{ tenant_policy_config }}"
	parentInfo := model.ModelInfo{
		Slug: parentModel, Visibility: "list", SupportedInAPI: true,
		ModelMessages: &model.ModelMessages{AutoReview: &model.AutoReviewMessages{
			Policy:         &parentPolicy,
			PolicyTemplate: &parentTemplate,
		}},
	}
	reviewInfo := model.ModelInfo{
		Slug: preferred, Visibility: "list", SupportedInAPI: true,
		ModelMessages: &model.ModelMessages{AutoReview: &model.AutoReviewMessages{
			Policy:         &reviewPolicy,
			PolicyTemplate: &reviewTemplate,
		}},
	}
	tests := []struct {
		name         string
		models       []model.ModelInfo
		wantPolicy   string
		wantTemplate string
	}{
		{
			name:         "review model entry wins",
			models:       []model.ModelInfo{parentInfo, reviewInfo},
			wantPolicy:   reviewPolicy,
			wantTemplate: reviewTemplate,
		},
		{
			name:         "catalog without the review model falls back to the parent",
			models:       []model.ModelInfo{parentInfo},
			wantPolicy:   parentPolicy,
			wantTemplate: parentTemplate,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var captured *model.AgentRequest
			agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
				copyRequest := *request
				captured = &copyRequest
				return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"instructions"}`}, nil
			})
			router := NewRuntimeRouter(RuntimeServices{
				Agent:        agent,
				Models:       model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: testCase.models})),
				ThreadStatus: NewThreadStatusManager(),
			})
			if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
				ThreadID: threadID,
				Model:    parentModel,
			}); err != nil {
				t.Fatalf("register active turn: %v", err)
			}
			router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{Model: parentModel})
			reviewer := router.ensureGuardianReviewer(agent)
			if _, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-instructions", state.Action{
				Type: "mcp_tool_call", Server: "apps", ToolName: "calendar",
			}); err != nil {
				t.Fatalf("Guardian review error = %v", err)
			}
			want := state.RenderGuardianPolicyInstructions(testCase.wantPolicy, "", testCase.wantTemplate, state.GuardianOutputContractPrompt())
			if captured == nil || captured.Instructions != want {
				t.Fatalf("Guardian instructions = %q, want %q", instructionsOrEmpty(captured), want)
			}
		})
	}
}

func instructionsOrEmpty(request *model.AgentRequest) string {
	if request == nil {
		return ""
	}
	return request.Instructions
}

// TestGuardianReviewInstructionsPrecedenceLikeRust pins the effective-text
// precedence Rust applies in build_guardian_review_session_config: the managed
// or configured policy wins over the catalog's, the catalog's wins over the
// bundled templates, and the output contract always terminates the result.
func TestGuardianReviewInstructionsPrecedenceLikeRust(t *testing.T) {
	contract := state.GuardianOutputContractPrompt()
	// No catalog and no config keeps the bundled documents.
	bundled := guardianReviewInstructions(nil, nil)
	wantBundled := state.RenderGuardianPolicyInstructions(state.GuardianPolicy(), "", state.GuardianPolicyTemplate(), contract)
	if bundled != wantBundled {
		t.Fatalf("bundled instructions drifted:\n%q\nwant\n%q", bundled, wantBundled)
	}
	if !strings.Contains(bundled, contract) || !strings.Contains(bundled, "## Environment Profile") {
		t.Fatalf("bundled instructions = %q", bundled)
	}

	// The catalog's policy and template win over the bundled ones.
	policy := "Catalog policy."
	template := "Catalog template:\n{{ tenant_policy_config }}"
	catalog := guardianReviewInstructions(nil, &model.AutoReviewMessages{Policy: &policy, PolicyTemplate: &template})
	if want := state.RenderGuardianPolicyInstructions(policy, "", template, contract); catalog != want {
		t.Fatalf("catalog instructions = %q, want %q", catalog, want)
	}

	// The managed policy wins over the catalog's, and the configured template
	// is used as-is.
	managed := "Managed policy."
	configuredTemplate := "Configured template:\n{{ tenant_policy_config }}"
	cfg := &config.Config{
		Values:       map[string]any{"auto_review": map[string]any{"experimental_policy_template": configuredTemplate}},
		Requirements: &config.ConfigRequirements{GuardianPolicyConfig: &managed},
	}
	overridden := guardianReviewInstructions(cfg, &model.AutoReviewMessages{Policy: &policy, PolicyTemplate: &template})
	if want := state.RenderGuardianPolicyInstructions(managed, "", configuredTemplate, contract); overridden != want {
		t.Fatalf("configured instructions = %q, want %q", overridden, want)
	}

	// Rust #47125: the managed extra policy reaches the template's
	// `{{ extra_policy }}` slot alongside the tenant policy.
	extraTemplate := "Tenant:\n{{ tenant_policy_config }}\n\nExtra:\n{{ extra_policy }}"
	managedExtra := "Managed extra policy."
	withExtra := &config.Config{
		Values: map[string]any{"auto_review": map[string]any{"experimental_policy_template": extraTemplate}},
		Requirements: &config.ConfigRequirements{
			GuardianPolicyConfig: &managed,
			GuardianExtraPolicy:  &managedExtra,
		},
	}
	got := guardianReviewInstructions(withExtra, nil)
	if want := state.RenderGuardianPolicyInstructions(managed, managedExtra, extraTemplate, contract); got != want {
		t.Fatalf("extra-policy instructions = %q, want %q", got, want)
	}
	if !strings.Contains(got, "Extra:\nManaged extra policy.") {
		t.Fatalf("extra policy did not reach the reviewer instructions: %q", got)
	}
}

// TestGuardianReviewInjectsNodeReplPolicyLikeRust mirrors Rust
// ensure_guardian_node_repl_policy: a `js` call on a node-repl-backed server
// gets the resolved node-REPL policy as its own unmarked developer fragment when
// the parent model requires computer-use review, and no fragment otherwise.
func TestGuardianReviewInjectsNodeReplPolicyLikeRust(t *testing.T) {
	const policy = "Node REPL and computer-use rules."
	tests := []struct {
		name                string
		action              state.Action
		autoReviewRequired  bool
		wantPolicyInRequest bool
	}{
		{
			name:                "node_repl js with computer-use review",
			action:              state.Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "js"},
			autoReviewRequired:  true,
			wantPolicyInRequest: true,
		},
		{
			name:                "cua_repl js with computer-use review",
			action:              state.Action{Type: "mcp_tool_call", Server: "cua_repl", ToolName: "js"},
			autoReviewRequired:  true,
			wantPolicyInRequest: true,
		},
		{
			name:               "node_repl js without computer-use review",
			action:             state.Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "js"},
			autoReviewRequired: false,
		},
		{
			name:               "node_repl non-js tool",
			action:             state.Action{Type: "mcp_tool_call", Server: "node_repl", ToolName: "inspect"},
			autoReviewRequired: true,
		},
		{
			name:               "other server",
			action:             state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "js"},
			autoReviewRequired: true,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var captured *model.AgentRequest
			reviewer := &modelGuardianReviewer{
				store:   state.NewReviewStore(),
				breaker: state.NewCircuitBreaker(),
				reviewPlan: func(string, string) guardianReviewPlan {
					return guardianReviewPlan{NodeReplPolicy: policy}
				},
				nodeReplAutoReviewRequired: func(string, string) bool { return testCase.autoReviewRequired },
				permissionProfile: func(string, string) *sandbox.PermissionProfile {
					return &sandbox.PermissionProfile{SandboxPolicy: sandbox.NewReadOnlyPolicy()}
				},
				agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
					copyRequest := *request
					captured = &copyRequest
					return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"policy"}`}, nil
				}),
			}
			if _, _, err := reviewer.Review(context.Background(), "thread-policy", "turn-policy", "call-policy", testCase.action); err != nil {
				t.Fatalf("Review() error = %v", err)
			}
			if captured == nil {
				t.Fatal("no agent request captured")
			}
			injected := false
			for _, item := range captured.InputItems {
				message, ok := item.(map[string]any)
				if !ok || message["role"] != "developer" {
					continue
				}
				content, _ := message["content"].([]map[string]any)
				for _, part := range content {
					if part["text"] == policy {
						injected = true
					}
				}
			}
			if injected != testCase.wantPolicyInRequest {
				t.Fatalf("node-REPL policy injected = %v, want %v (input items %#v)", injected, testCase.wantPolicyInRequest, captured.InputItems)
			}
		})
	}
}

func TestModelGuardianReviewerMapsTimeout(t *testing.T) {
	reviewer := &modelGuardianReviewer{
		timeout: time.Millisecond,
		agent: guardianAgentFunc(func(ctx context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
	}
	decision, reason, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"})
	if err != nil || decision != state.DecisionTimedOut || reason != state.GuardianTimeoutInstructions() {
		t.Fatalf("decision=%s reason=%q err=%v", decision, reason, err)
	}
}

// Mirrors Rust's parse-failure completion (completion.rs GuardianReviewError::
// Parse): the review fails closed with a denied decision carrying the failure
// rationale, not an aborted review.
func TestModelGuardianReviewerRejectsMalformedAssessment(t *testing.T) {
	reviewer := &modelGuardianReviewer{agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		return &model.AgentResponse{Message: `not-json`}, nil
	})}
	decision, reason, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"})
	if err != nil || decision != state.DecisionDenied {
		t.Fatalf("decision=%s err=%v", decision, err)
	}
	if !strings.Contains(reason, "Automatic approval review failed:") ||
		!strings.Contains(reason, "automatic approval review could not be completed") {
		t.Fatalf("reason = %q", reason)
	}
}

// TestGuardianReviewFailureTextSplitLikeRust mirrors Rust's failed-review
// completion: the assessment event and the GuardianWarning carry the failure
// rationale, while the tool rejection appends the review-failure instructions.
func TestGuardianReviewFailureTextSplitLikeRust(t *testing.T) {
	var events []*state.Event
	var warnings []string
	reviewer := &modelGuardianReviewer{
		store: state.NewReviewStore(), breaker: state.NewCircuitBreaker(),
		notify: func(_ string, event *state.Event) { events = append(events, event) },
		warn:   func(_ string, message string) { warnings = append(warnings, message) },
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return &model.AgentResponse{Message: "not-json"}, nil
		}),
	}
	decision, reason, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"})
	if err != nil || decision != state.DecisionDenied {
		t.Fatalf("decision=%s err=%v", decision, err)
	}
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "Automatic approval review failed: ") ||
		strings.Contains(warnings[0], "Do not bypass the approval check") {
		t.Fatalf("warnings = %#v, want the bare failure rationale", warnings)
	}
	if reason != warnings[0]+"\n"+guardianReviewFailureInstructions {
		t.Fatalf("reason = %q, warning = %q", reason, warnings[0])
	}
	if len(events) != 2 || events[1].Status != state.StatusDenied || events[1].Rationale != warnings[0] {
		t.Fatalf("events = %#v, warning = %q", events, warnings[0])
	}
}

func TestModelGuardianReviewerReadsAssessmentFromAgentMessageItem(t *testing.T) {
	reviewer := &modelGuardianReviewer{agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
		return &model.AgentResponse{Items: []model.AgentItem{{Type: "agent_message", Text: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"item"}`}}}, nil
	})}
	decision, reason, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"})
	if err != nil || decision != state.DecisionApproved || reason != "item" {
		t.Fatalf("decision=%s reason=%q err=%v", decision, reason, err)
	}
}

func TestModelGuardianReviewerEmitsLifecycleAndRecordsDenial(t *testing.T) {
	store := state.NewReviewStore()
	breaker := state.NewCircuitBreaker()
	var events []*state.Event
	reviewer := &modelGuardianReviewer{
		store: store, breaker: breaker,
		notify: func(threadID string, event *state.Event) {
			if threadID != "thread-1" {
				t.Fatalf("threadID=%q", threadID)
			}
			events = append(events, event)
		},
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return &model.AgentResponse{Message: `{"riskLevel":"high","userAuthorization":"low","outcome":"deny","rationale":"risky"}`}, nil
		}),
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "write"})
	if err != nil || decision != state.DecisionDenied || len(events) != 2 {
		t.Fatalf("decision=%s events=%#v err=%v", decision, events, err)
	}
	if events[0].ID != events[1].ID || events[0].Status != state.StatusInProgress || events[1].Status != state.StatusDenied || events[1].TargetItemID != "call-1" {
		t.Fatalf("events=%#v", events)
	}
	action := breaker.RecordDenial("turn-1")
	if action.ConsecutiveDenials != 2 {
		t.Fatalf("breaker action=%#v", action)
	}
}

func TestModelGuardianReviewerUsesModelSpecificAutoReviewInstructionsLikeRust(t *testing.T) {
	rejection := "Follow the managed policy and stop."
	timeout := "Managed timeout instruction."
	messages := &model.AutoReviewMessages{RejectionInstructions: &rejection, TimeoutInstructions: &timeout}
	reviewer := &modelGuardianReviewer{
		store: state.NewReviewStore(), breaker: state.NewCircuitBreaker(),
		reviewPlan: func(threadID, turnID string) guardianReviewPlan {
			return guardianReviewPlan{AutoReview: messages}
		},
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return &model.AgentResponse{Message: `{"riskLevel":"high","userAuthorization":"low","outcome":"deny","rationale":"risky"}`}, nil
		}),
	}
	decision, reason, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "write"})
	if err != nil || decision != state.DecisionDenied {
		t.Fatalf("decision=%s err=%v", decision, err)
	}
	if !strings.Contains(reason, "This action was rejected due to unacceptable risk.") || !strings.Contains(reason, rejection) {
		t.Fatalf("denial reason = %q, want model rejection instructions", reason)
	}

	timeoutReviewer := &modelGuardianReviewer{
		timeout: time.Millisecond,
		reviewPlan: func(threadID, turnID string) guardianReviewPlan {
			return guardianReviewPlan{AutoReview: messages}
		},
		agent: guardianAgentFunc(func(ctx context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
	}
	decision, reason, err = timeoutReviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "write"})
	if err != nil || decision != state.DecisionTimedOut || reason != timeout {
		t.Fatalf("timeout decision=%s reason=%q err=%v", decision, reason, err)
	}
}

// TestGuardianAutoReviewInstructionFallbacksMatchRust mirrors Rust's
// guardian_review_rejects_tool_call_with_acting_model_instructions and
// guardian_timeout_rejects_tool_call_with_acting_model_instructions cases:
// legacy_fallback resolves the bundled text when the catalog omits the field,
// catalog_override wins when present, and empty_override keeps the explicit
// empty value. The timed-out warning always carries Rust's review rationale.
func TestGuardianAutoReviewInstructionFallbacksMatchRust(t *testing.T) {
	empty := ""
	rejectionOverride := "Reviewer-only rejection instructions."
	timeoutOverride := "Acting model timeout instructions."
	for _, testCase := range []struct {
		name          string
		messages      *model.AutoReviewMessages
		wantRejection string
		wantTimeout   string
	}{
		{
			name:          "legacy_fallback",
			messages:      nil,
			wantRejection: "This action was rejected due to unacceptable risk.\nReason: risky\n" + state.GuardianRejectionInstructions(),
			wantTimeout:   state.GuardianTimeoutInstructions(),
		},
		{
			name:          "catalog_override",
			messages:      &model.AutoReviewMessages{RejectionInstructions: &rejectionOverride, TimeoutInstructions: &timeoutOverride},
			wantRejection: "This action was rejected due to unacceptable risk.\nReason: risky\n" + rejectionOverride,
			wantTimeout:   timeoutOverride,
		},
		{
			name:          "empty_override",
			messages:      &model.AutoReviewMessages{RejectionInstructions: &empty, TimeoutInstructions: &empty},
			wantRejection: "This action was rejected due to unacceptable risk.\nReason: risky\n",
			wantTimeout:   "",
		},
	} {
		t.Run(testCase.name+"/deny", func(t *testing.T) {
			reviewer := &modelGuardianReviewer{
				reviewPlan: func(threadID, turnID string) guardianReviewPlan {
					return guardianReviewPlan{AutoReview: testCase.messages}
				},
				agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
					return &model.AgentResponse{Message: `{"riskLevel":"high","userAuthorization":"low","outcome":"deny","rationale":"risky"}`}, nil
				}),
			}
			decision, reason, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "write"})
			if err != nil || decision != state.DecisionDenied || reason != testCase.wantRejection {
				t.Fatalf("decision=%s reason=%q err=%v, want %q", decision, reason, err, testCase.wantRejection)
			}
		})
		t.Run(testCase.name+"/timeout", func(t *testing.T) {
			var warnings []string
			reviewer := &modelGuardianReviewer{
				timeout: time.Millisecond,
				reviewPlan: func(threadID, turnID string) guardianReviewPlan {
					return guardianReviewPlan{AutoReview: testCase.messages}
				},
				warn: func(_ string, message string) { warnings = append(warnings, message) },
				agent: guardianAgentFunc(func(ctx context.Context, _ *model.AgentRequest) (*model.AgentResponse, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				}),
			}
			decision, reason, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "write"})
			if err != nil || decision != state.DecisionTimedOut || reason != testCase.wantTimeout {
				t.Fatalf("decision=%s reason=%q err=%v, want %q", decision, reason, err, testCase.wantTimeout)
			}
			if len(warnings) != 1 || warnings[0] != state.GuardianTimeoutRationale() {
				t.Fatalf("warnings = %#v, want the review rationale", warnings)
			}
		})
	}
}

func TestGuardianReviewTranscriptRendersStandaloneFunctionCallOutputLikeRust(t *testing.T) {
	// Rust #39791: standalone function_call_output items render in guardian
	// transcripts as "tool <namespace.name> result" entries with a placeholder
	// for non-text content.
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := "123e4567-e89b-42d3-a456-426614174100"
	now := fixedTime()
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli", ModelProvider: "openai"},
		Items: []session.Item{
			{ID: "standalone", Type: "function_call_output", Name: "read", Namespace: "drive", Text: "ok", CreatedAt: now},
			{ID: "paired", Type: "function_call_output", CallID: "call-1", Text: "paired", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	lines := router.guardianReviewTranscript(threadID)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "tool drive.read result: ok") {
		t.Fatalf("transcript missing standalone tool result: %q", joined)
	}
	if strings.Contains(joined, "tool  result: paired") || strings.Contains(joined, "tool result: paired") {
		t.Fatalf("paired function call output rendered as standalone tool result: %q", joined)
	}
}

func TestMarkThreadMemoryPollutedOnStandaloneExternalContextLikeRust(t *testing.T) {
	home := t.TempDir()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(context.Background(), sqliteConfig, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer stateRuntime.Close()
	threadID := "123e4567-e89b-42d3-a456-426614174101"
	rolloutPath := writeMemoryStartupTestRollout(t, home, threadID, time.Now().UTC().Add(-time.Hour).Truncate(time.Second))
	if err := stateRuntime.ReconcileRollout(context.Background(), rolloutPath, false); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		StateRuntime: stateRuntime,
		Config:       config.NewConfigService(home),
		Models:       model.NewModelService(nil),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		Agent:        newRecordingRuntimeAgent("ok"),
	})
	defer router.Close()
	if err := os.WriteFile(config.ConfigPath(home), []byte("[memories]\ndisable_on_external_context = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	router.markThreadMemoryPollutedOnExternalContext(threadID, []session.Item{{
		Type:   "function_call_output",
		Name:   "read",
		CallID: "",
	}})
	var mode string
	if err := stateRuntime.StateDB().QueryRow(`SELECT memory_mode FROM threads WHERE id = ?`, threadID).Scan(&mode); err != nil {
		t.Fatalf("query memory_mode: %v", err)
	}
	if mode != "polluted" {
		t.Fatalf("memory_mode = %q, want polluted", mode)
	}
}

// Rust parity: core/src/tools/registry.rs handle_any_tool marks the thread's
// memory mode polluted when a successful tool output declares external context
// and memories.disable_on_external_context is enabled.
func TestMarkThreadMemoryPollutedOnToolOutputLikeRust(t *testing.T) {
	home := t.TempDir()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(context.Background(), sqliteConfig, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer stateRuntime.Close()
	threadID := "123e4567-e89b-42d3-a456-426614174102"
	rolloutPath := writeMemoryStartupTestRollout(t, home, threadID, time.Now().UTC().Add(-time.Hour).Truncate(time.Second))
	if err := stateRuntime.ReconcileRollout(context.Background(), rolloutPath, false); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		StateRuntime: stateRuntime,
		Config:       config.NewConfigService(home),
		Models:       model.NewModelService(nil),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		Agent:        newRecordingRuntimeAgent("ok"),
	})
	defer router.Close()

	// Without the gate the marker is inert.
	router.markThreadMemoryPollutedOnToolOutput(context.Background(), threadID, &tool.Invocation{CallID: "call-1"})
	var mode string
	if err := stateRuntime.StateDB().QueryRow(`SELECT memory_mode FROM threads WHERE id = ?`, threadID).Scan(&mode); err != nil {
		t.Fatalf("query memory_mode: %v", err)
	}
	if mode == "polluted" {
		t.Fatalf("marker polluted a thread while the gate was disabled")
	}

	if err := os.WriteFile(config.ConfigPath(home), []byte("[memories]\ndisable_on_external_context = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	router.markThreadMemoryPollutedOnToolOutput(context.Background(), threadID, &tool.Invocation{CallID: "call-1"})
	if err := stateRuntime.StateDB().QueryRow(`SELECT memory_mode FROM threads WHERE id = ?`, threadID).Scan(&mode); err != nil {
		t.Fatalf("query memory_mode: %v", err)
	}
	if mode != "polluted" {
		t.Fatalf("memory_mode = %q, want polluted", mode)
	}
}

func TestModelGuardianReviewerInterruptsAfterDenialThreshold(t *testing.T) {
	interrupts := 0
	reviewer := &modelGuardianReviewer{
		store: state.NewReviewStore(), breaker: state.NewCircuitBreaker(),
		interrupt: func(threadID, turnID string) {
			if threadID != "thread-1" || turnID != "turn-1" {
				t.Fatalf("interrupt=%s/%s", threadID, turnID)
			}
			interrupts++
		},
		agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			return &model.AgentResponse{Message: `{"riskLevel":"high","userAuthorization":"low","outcome":"deny","rationale":"risky"}`}, nil
		}),
	}
	for i := 0; i < 4; i++ {
		decision, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "write"})
		if err != nil || decision != state.DecisionDenied {
			t.Fatalf("review %d decision=%s err=%v", i, decision, err)
		}
	}
	if interrupts != 1 {
		t.Fatalf("interrupts=%d", interrupts)
	}
}

func TestGuardianSessionRunnerReusesPreviousResponse(t *testing.T) {
	var requests []*model.AgentRequest
	runner := &guardianSessionRunner{agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		copyRequest := *request
		requests = append(requests, &copyRequest)
		return &model.AgentResponse{ResponseID: "resp-" + string(rune('1'+len(requests))), Message: "ok"}, nil
	})}
	for i := 0; i < 2; i++ {
		if _, err := runner.Run(context.Background(), &model.AgentRequest{Prompt: "review", Store: true, ClientMetadata: map[string]string{"x": "y"}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(requests) != 2 || requests[0].PreviousResponseID != "" || requests[1].PreviousResponseID != "resp-2" || requests[0].Store || requests[1].Store {
		t.Fatalf("requests=%#v", requests)
	}
}

func TestGuardianSessionRunnerReusesPrewarmResponseForFirstReview(t *testing.T) {
	agent := &guardianPrewarmAgent{}
	session := &guardianSessionRunner{agent: agent}
	if err := session.Prewarm(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := session.Run(context.Background(), &model.AgentRequest{Prompt: "review"}); err != nil {
			t.Fatal(err)
		}
	}
	_, websocketRuns, requests := agent.snapshot()
	if len(requests) != 2 || requests[0].PreviousResponseID != "warm-1" || requests[1].PreviousResponseID != "review-1" {
		t.Fatalf("requests=%#v", requests)
	}
	if websocketRuns != 2 {
		t.Fatalf("websocketRuns=%d", websocketRuns)
	}
}

func TestEnsureGuardianReviewerPrewarmsOnceAndFallsBackLazily(t *testing.T) {
	t.Run("once", func(t *testing.T) {
		agent := &guardianPrewarmAgent{}
		router := NewRuntimeRouter(RuntimeServices{Agent: agent})
		first := router.ensureGuardianReviewer(agent)
		second := router.ensureGuardianReviewer(agent)
		if first == nil || first != second {
			t.Fatalf("reviewers=%p/%p", first, second)
		}
		deadline := time.Now().Add(time.Second)
		prewarms, _, _ := agent.snapshot()
		for prewarms == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
			prewarms, _, _ = agent.snapshot()
		}
		if prewarms != 1 {
			t.Fatalf("prewarms=%d", prewarms)
		}
	})
	t.Run("failure", func(t *testing.T) {
		agent := &guardianPrewarmAgent{prewarmErr: errors.New("websocket unavailable")}
		router := NewRuntimeRouter(RuntimeServices{Agent: agent})
		reviewer := router.ensureGuardianReviewer(agent)
		deadline := time.Now().Add(time.Second)
		prewarms, _, _ := agent.snapshot()
		for prewarms == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
			prewarms, _, _ = agent.snapshot()
		}
		decision, _, err := reviewer.Review(context.Background(), "thread", "turn", "call", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "read"})
		_, _, requests := agent.snapshot()
		if err != nil || decision != state.DecisionApproved || len(requests) != 1 || requests[0].PreviousResponseID != "" {
			t.Fatalf("decision=%s requests=%#v err=%v", decision, requests, err)
		}
	})
}

func TestGuardianSessionRunnerFallsBackWhenWebSocketReviewFails(t *testing.T) {
	agent := &guardianPrewarmAgent{websocketErr: errors.New("websocket closed")}
	session := &guardianSessionRunner{agent: agent}
	if err := session.Prewarm(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := session.Run(context.Background(), &model.AgentRequest{Prompt: "review"})
	if err != nil || response == nil || response.ResponseID != "review-1" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	_, websocketRuns, requests := agent.snapshot()
	if websocketRuns != 1 || len(requests) != 1 || requests[0].PreviousResponseID != "" {
		t.Fatalf("websocketRuns=%d requests=%#v", websocketRuns, requests)
	}
}

func TestApproveGuardianDeniedActionInjectsExactAction(t *testing.T) {
	mailbox := turn.NewSteerMailbox()
	router := NewRuntimeRouter(RuntimeServices{SteerMailbox: mailbox, DefaultCWD: t.TempDir()})
	router.ephemeralThreads["thread-1"] = &session.Record{ID: "thread-1", Items: []session.Item{}}
	if err := router.registerActiveRuntimeTurn("thread-1", "turn-1", func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: "thread-1"}); err != nil {
		t.Fatal(err)
	}
	event := state.Event{ID: "review-1", TurnID: "turn-1", Status: state.StatusDenied, Action: state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "write", Extra: map[string]any{"arguments": map[string]any{"title": "Lunch"}}}}
	raw, _ := json.Marshal(event)
	response, err := router.handleThreadApproveGuardianDeniedActionRuntime(requestWithParams(t, IntID(1), MethodThreadApproveGuardianDeniedAction, ThreadApproveGuardianDeniedActionParams{ThreadID: "thread-1", Event: raw}))
	if err != nil || response == nil {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	record, ok := router.ephemeralThreadRecord("thread-1", true)
	if !ok || len(record.Items) != 1 || record.Items[0].Role != "developer" || !strings.Contains(record.Items[0].Text, state.DeniedActionApprovalPrefix) || !strings.Contains(record.Items[0].Text, `"outcome": "allowed"`) {
		t.Fatalf("record=%#v", record)
	}
	items := mailbox.Drain(&turn.SteerDrainParams{ThreadID: "thread-1", TurnID: "turn-1"})
	if len(items) != 1 || !strings.Contains(fmt.Sprint(items[0]), "exact action") {
		t.Fatalf("mailbox=%#v", items)
	}
}

func TestGuardianReviewNotificationPreservesExactAction(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	defer router.Close()
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	completedAt := int64(150)
	rationale := state.RiskHigh
	event := state.Event{
		ID:            "review-command",
		TurnID:        "turn-command",
		StartedAtMS:   100,
		CompletedAtMS: &completedAt,
		Status:        state.StatusDenied,
		RiskLevel:     &rationale,
		Rationale:     "Writes outside the sandbox.",
		Action: state.Action{
			Type:    "command",
			Source:  state.CommandSourceShell,
			Command: "rm -rf build",
			CWD:     `D:\repo`,
		},
	}
	router.notifyGuardianReviewEvent("thread-command", &event)
	notifications := sink.List()
	if len(notifications) != 1 || notifications[0].Method != NotificationItemGuardianApprovalReviewCompleted {
		t.Fatalf("Guardian notifications = %#v", notifications)
	}
	payload, ok := notifications[0].Params.(*ItemGuardianApprovalReviewCompletedNotification)
	if !ok {
		t.Fatalf("Guardian notification payload = %T", notifications[0].Params)
	}
	if payload.ThreadID != "thread-command" || payload.ReviewID != "review-command" || payload.Action.Type != "command" || payload.Action.Command != "rm -rf build" || payload.Action.CWD != `D:\repo` || payload.Action.Source != GuardianCommandSourceShell {
		t.Fatalf("Guardian notification payload = %#v", payload)
	}
}

func TestApproveGuardianDeniedActionIgnoresNonDeniedAndRejectsInvalidJSON(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	router.ephemeralThreads["thread-1"] = &session.Record{ID: "thread-1"}
	approved, _ := json.Marshal(state.Event{ID: "review-1", Status: state.StatusApproved})
	if _, err := router.handleThreadApproveGuardianDeniedActionRuntime(requestWithParams(t, IntID(1), MethodThreadApproveGuardianDeniedAction, ThreadApproveGuardianDeniedActionParams{ThreadID: "thread-1", Event: approved})); err != nil {
		t.Fatal(err)
	}
	record, _ := router.ephemeralThreadRecord("thread-1", true)
	if len(record.Items) != 0 {
		t.Fatalf("items=%#v", record.Items)
	}
	_, err := router.handleThreadApproveGuardianDeniedActionRuntime(&Request{JSONRPC: "2.0", ID: IntID(2), Method: MethodThreadApproveGuardianDeniedAction, Params: json.RawMessage(`{"threadId":"thread-1","event":"bad"}`)})
	if err == nil || !strings.Contains(err.Error(), "invalid Guardian denial event") {
		t.Fatalf("err=%v", err)
	}
}

func TestApproveGuardianDeniedActionRuntimeDispatchUsesLiveHandler(t *testing.T) {
	mailbox := turn.NewSteerMailbox()
	threadRouter := NewRouter(session.NewStore(t.TempDir()))
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, SteerMailbox: mailbox, DefaultCWD: t.TempDir()})
	router.rememberConnectionClientInfo("default", ClientInfo{Name: "test", Version: "1"})
	started := threadRouter.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if started.Error != nil {
		t.Fatalf("thread start=%#v", started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	router.markResponseThreadLoaded(started.Result, "default")
	if err := router.registerActiveRuntimeTurn(threadID, "turn-1", func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatal(err)
	}
	event := state.Event{ID: "review-dispatch", TurnID: "turn-1", Status: state.StatusDenied, Action: state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "write"}}
	raw, _ := json.Marshal(event)
	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadApproveGuardianDeniedAction, ThreadApproveGuardianDeniedActionParams{ThreadID: threadID, Event: raw}))
	if response.Error != nil {
		t.Fatalf("response=%#v", response.Error)
	}
	if items := mailbox.Drain(&turn.SteerDrainParams{ThreadID: threadID, TurnID: "turn-1"}); len(items) != 1 {
		t.Fatalf("mailbox=%#v", items)
	}
}

func TestThreadInjectItemsRuntimeDispatchFeedsActiveTurn(t *testing.T) {
	mailbox := turn.NewSteerMailbox()
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, SteerMailbox: mailbox, DefaultCWD: t.TempDir()})
	router.rememberConnectionClientInfo("default", ClientInfo{Name: "test", Version: "1"})
	started := threadRouter.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if started.Error != nil {
		t.Fatal(started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	router.markResponseThreadLoaded(started.Result, "default")
	if err := router.registerActiveRuntimeTurn(threadID, "turn-inject", func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"type":"message","role":"developer","content":[{"type":"input_text","text":"injected context"}]}`)
	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadInjectItems, ThreadInjectItemsParams{ThreadID: threadID, Items: []json.RawMessage{raw}}))
	if response.Error != nil {
		t.Fatalf("response=%#v", response.Error)
	}
	items := mailbox.Drain(&turn.SteerDrainParams{ThreadID: threadID, TurnID: "turn-inject"})
	if len(items) != 1 || !strings.Contains(fmt.Sprint(items[0]), "injected context") {
		t.Fatalf("mailbox=%#v", items)
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil || len(record.Items) == 0 || !strings.Contains(record.Items[len(record.Items)-1].Text, "injected context") {
		t.Fatalf("record=%#v err=%v", record, err)
	}
}

func TestThreadElicitationRuntimePausesUnifiedExec(t *testing.T) {
	manager := tool.NewUnifiedExecManager()
	defer manager.Close()
	threadRouter := NewRouter(session.NewStore(t.TempDir()))
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, UnifiedExec: manager, DefaultCWD: t.TempDir()})
	router.rememberConnectionClientInfo("default", ClientInfo{Name: "test", Version: "1"})
	started := threadRouter.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	router.markResponseThreadLoaded(started.Result, "default")
	increment := router.Handle(requestWithParams(t, IntID(2), MethodThreadIncrementElicitation, ThreadIncrementElicitationParams{ThreadID: threadID}))
	if increment.Error != nil || !manager.ThreadElicitationPaused(threadID) {
		t.Fatalf("increment=%#v paused=%v", increment.Error, manager.ThreadElicitationPaused(threadID))
	}
	decrement := router.Handle(requestWithParams(t, IntID(3), MethodThreadDecrementElicitation, ThreadDecrementElicitationParams{ThreadID: threadID}))
	if decrement.Error != nil || manager.ThreadElicitationPaused(threadID) {
		t.Fatalf("decrement=%#v paused=%v", decrement.Error, manager.ThreadElicitationPaused(threadID))
	}
}

func TestAccountDeviceLoginRuntimeCompletesAndCancels(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		home := t.TempDir()
		jwt := testJWTForGuardianLogin(map[string]any{"chatgpt_account_id": "account-1"})
		issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/api/accounts/deviceauth/usercode":
				_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "device-1", "user_code": "CODE-1", "interval": "0"})
			case "/api/accounts/deviceauth/token":
				_ = json.NewEncoder(w).Encode(map[string]string{"authorization_code": "code", "code_challenge": "challenge", "code_verifier": "verifier"})
			case "/oauth/token":
				_ = json.NewEncoder(w).Encode(map[string]string{"id_token": jwt, "access_token": "access", "refresh_token": "refresh"})
			default:
				http.NotFound(w, request)
			}
		}))
		defer issuer.Close()
		sink := NewNotificationBuffer()
		router := NewRuntimeRouter(RuntimeServices{DefaultCWD: home, AccountOAuthOptions: &auth.OAuthOptions{CodexHome: home, Issuer: issuer.URL, ClientID: "client", PollInterval: time.Millisecond, PollTimeout: time.Second}})
		router.SetNotificationSink(sink)
		response := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: "chatgptDeviceCode"}))
		if response.Error != nil {
			t.Fatalf("response=%#v", response.Error)
		}
		login := response.Result.(*auth.LoginAccountResponse)
		if login.UserCode != "CODE-1" || login.VerificationURL != issuer.URL+"/codex/device" {
			t.Fatalf("login=%#v", login)
		}
		waitForAccountLoginNotification(t, sink, login.LoginID, true)
		if account := router.requireAccount().GetAccount(nil).Account; account == nil || account.Type != auth.AccountChatGPT {
			t.Fatalf("account=%#v", account)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		home := t.TempDir()
		issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/api/accounts/deviceauth/usercode":
				_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "device-1", "user_code": "CODE-1", "interval": "1"})
			case "/api/accounts/deviceauth/token":
				http.Error(w, "pending", http.StatusForbidden)
			default:
				http.NotFound(w, request)
			}
		}))
		defer issuer.Close()
		router := NewRuntimeRouter(RuntimeServices{DefaultCWD: home, AccountOAuthOptions: &auth.OAuthOptions{CodexHome: home, Issuer: issuer.URL, ClientID: "client", PollInterval: time.Millisecond, PollTimeout: time.Minute}})
		loginResponse := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: "chatgptDeviceCode"}))
		login := loginResponse.Result.(*auth.LoginAccountResponse)
		cancelResponse := router.Handle(requestWithParams(t, IntID(2), MethodCancelLoginAccount, auth.CancelLoginAccountParams{LoginID: login.LoginID}))
		if cancelResponse.Error != nil || cancelResponse.Result.(*auth.CancelLoginAccountResponse).Status != auth.CancelLoginCanceled {
			t.Fatalf("cancel=%#v", cancelResponse)
		}
	})
}

func TestAccountBrowserLoginRuntimeStartsAndCancelsRealServer(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: home, AccountOAuthOptions: &auth.OAuthOptions{CodexHome: home, CallbackPort: 0}})
	response := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: auth.AccountChatGPT}))
	if response.Error != nil {
		t.Fatalf("response=%#v", response.Error)
	}
	login := response.Result.(*auth.LoginAccountResponse)
	parsed, err := url.Parse(login.AuthURL)
	if err != nil || parsed.Query().Get("redirect_uri") == "" || !strings.Contains(parsed.Query().Get("redirect_uri"), "/auth/callback") {
		t.Fatalf("authURL=%q err=%v", login.AuthURL, err)
	}
	cancelResponse := router.Handle(requestWithParams(t, IntID(2), MethodCancelLoginAccount, auth.CancelLoginAccountParams{LoginID: login.LoginID}))
	if cancelResponse.Error != nil || cancelResponse.Result.(*auth.CancelLoginAccountResponse).Status != auth.CancelLoginCanceled {
		t.Fatalf("cancel=%#v", cancelResponse)
	}
}

func TestAccountLogoutCancelsPendingLoginRuntime(t *testing.T) {
	home := t.TempDir()
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "device-1", "user_code": "CODE-1", "interval": "1"})
		case "/api/accounts/deviceauth/token":
			http.Error(w, "pending", http.StatusForbidden)
		default:
			http.NotFound(w, request)
		}
	}))
	defer issuer.Close()
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: home, AccountOAuthOptions: &auth.OAuthOptions{CodexHome: home, Issuer: issuer.URL, ClientID: "client", PollInterval: time.Millisecond, PollTimeout: time.Minute}})
	loginResponse := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: "chatgptDeviceCode"}))
	if loginResponse.Error != nil {
		t.Fatal(loginResponse.Error)
	}
	logoutResponse := router.Handle(&Request{JSONRPC: "2.0", ID: IntID(2), Method: MethodLogoutAccount})
	if logoutResponse.Error != nil {
		t.Fatal(logoutResponse.Error)
	}
	router.loginRuntimeMu.Lock()
	pending := len(router.loginRuntimeCancels)
	router.loginRuntimeMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending runtimes=%d", pending)
	}
	time.Sleep(30 * time.Millisecond)
	resolved, err := auth.NewStore(home).Resolve()
	if err != nil || resolved != nil {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}
}

func TestRuntimeRouterCloseCancelsPendingLoginRuntime(t *testing.T) {
	home := t.TempDir()
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/accounts/deviceauth/usercode" {
			_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "device-1", "user_code": "CODE-1", "interval": "1"})
			return
		}
		if request.URL.Path == "/api/accounts/deviceauth/token" {
			http.Error(w, "pending", http.StatusForbidden)
			return
		}
		http.NotFound(w, request)
	}))
	defer issuer.Close()
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: home, AccountOAuthOptions: &auth.OAuthOptions{CodexHome: home, Issuer: issuer.URL, ClientID: "client", PollInterval: time.Millisecond, PollTimeout: time.Minute}})
	response := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: "chatgptDeviceCode"}))
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	if err := router.Close(); err != nil {
		t.Fatal(err)
	}
	router.loginRuntimeMu.Lock()
	pending := len(router.loginRuntimeCancels)
	router.loginRuntimeMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending runtimes=%d", pending)
	}
}

func testJWTForGuardianLogin(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body, _ := json.Marshal(claims)
	return header + "." + base64.RawURLEncoding.EncodeToString(body) + ".signature"
}

func waitForAccountLoginNotification(t *testing.T, sink *NotificationBuffer, loginID string, success bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, notification := range sink.List() {
			if notification.Method != NotificationAccountLoginCompleted {
				continue
			}
			params, ok := notification.Params.(*auth.AccountLoginCompletedNotification)
			if ok && params.LoginID != nil && *params.LoginID == loginID && params.Success == success {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("login notification %s success=%v not found", loginID, success)
}

// TestGuardianMaxToolCallLagLikeRust mirrors Rust's GuardianV2Config resolution
// of max_tool_call_lag: the configured [features.guardianv2] value wins, then
// the model catalog's model_messages.guardian_v2.max_tool_call_lag, then
// DEFAULT_MAX_TOOL_CALL_LAG (2).
func TestGuardianMaxToolCallLagLikeRust(t *testing.T) {
	configured := &config.Config{Values: map[string]any{
		"features": map[string]any{"guardianv2": map[string]any{"max_tool_call_lag": int64(7)}},
	}}
	modelLag := 5
	info := &model.ModelInfo{ModelMessages: &model.ModelMessages{
		GuardianV2: &model.GuardianV2ModelConfig{MaxToolCallLag: &modelLag},
	}}
	if got := guardianMaxToolCallLag(configured, info); got != 7 {
		t.Fatalf("configured lag = %d, want 7", got)
	}
	if got := guardianMaxToolCallLag(&config.Config{}, info); got != 5 {
		t.Fatalf("model default lag = %d, want 5", got)
	}
	if got := guardianMaxToolCallLag(&config.Config{}, nil); got != 2 {
		t.Fatalf("fallback lag = %d, want Rust's default 2", got)
	}
	if got := guardianMaxToolCallLag(&config.Config{}, &model.ModelInfo{}); got != 2 {
		t.Fatalf("lag with model info but no guardian config = %d, want 2", got)
	}
	reviewer := &modelGuardianReviewer{}
	if got := reviewer.maxToolCallLagValue("thread", "turn"); got != 2 {
		t.Fatalf("reviewer lag = %d, want the default 2", got)
	}
	reviewer.maxToolCallLagFor = func(string, string) int { return 9 }
	if got := reviewer.maxToolCallLagValue("thread", "turn"); got != 9 {
		t.Fatalf("reviewer lag = %d, want the per-turn resolver value 9", got)
	}
}

// TestAppServiceTierForTurnHonorsPerTurnOverrideLikeRust mirrors Rust's
// TurnStartOptions.service_tier: the per-turn override wins for the turn (and
// "default" means standard speed) without consulting the thread/config tier.
func TestAppServiceTierForTurnHonorsPerTurnOverrideLikeRust(t *testing.T) {
	models := model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
		Slug:           "gpt-test",
		DisplayName:    "GPT Test",
		Visibility:     model.VisibilityVisible,
		SupportedInAPI: true,
		ServiceTiers:   []string{"priority"},
	}}}))
	router := NewRuntimeRouter(RuntimeServices{Models: models})
	cfg := &config.Config{Values: map[string]any{"service_tier": "priority"}}

	perTurn := "priority"
	got := router.appServiceTierForTurn(cfg, &turn.TurnStartParams{ServiceTierForTurn: &perTurn}, "gpt-test")
	if got != "priority" {
		t.Fatalf("per-turn tier = %q, want priority", got)
	}
	// "fast" normalizes to the request value, and "default" clears it.
	fast := "fast"
	if got := router.appServiceTierForTurn(cfg, &turn.TurnStartParams{ServiceTierForTurn: &fast}, "gpt-test"); got != "priority" {
		t.Fatalf("per-turn fast tier = %q, want priority", got)
	}
	standard := "default"
	if got := router.appServiceTierForTurn(cfg, &turn.TurnStartParams{ServiceTierForTurn: &standard}, "gpt-test"); got != "" {
		t.Fatalf("per-turn default tier = %q, want standard speed", got)
	}
}

// TestApproveGuardianDeniedActionRejectsNonObjectEventLikeRust covers Rust's
// typed event deserialization: the field is required and must be a Guardian
// assessment object, so null or a scalar fails the request.
func TestApproveGuardianDeniedActionRejectsNonObjectEventLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	cases := []struct {
		name  string
		event json.RawMessage
		want  string
	}{
		{"missing", nil, "event is required"},
		{"null", json.RawMessage("null"), "event is required"},
		{"scalar", json.RawMessage(`"denied"`), "invalid Guardian denial event"},
		{"array", json.RawMessage(`[]`), "invalid Guardian denial event"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := router.handleThreadApproveGuardianDeniedActionRuntime(requestWithParams(t, IntID(1), MethodThreadApproveGuardianDeniedAction, ThreadApproveGuardianDeniedActionParams{
				ThreadID: "thread-1",
				Event:    tc.event,
			}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
