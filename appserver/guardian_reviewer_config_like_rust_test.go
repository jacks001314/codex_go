package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/sandbox"
	"codex_go/state"
	"codex_go/turn"
)

func guardianTestReadySelection(id, cwd string, profile *sandbox.PermissionProfile) map[string]any {
	selection := map[string]any{"environmentId": id, "cwd": cwd}
	if profile == nil {
		selection["config"] = environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigReady, Config: &EnvironmentConfig{}})
		return selection
	}
	profileJSON, err := sandbox.RuntimePermissionProfileJSON(*profile)
	if err != nil {
		panic(err)
	}
	selection["config"] = environmentConfigStateToAny(EnvironmentConfigState{
		Kind: EnvironmentConfigReady,
		Config: &EnvironmentConfig{
			AllowLoginShell:         true,
			PermissionProfile:       profile,
			PermissionProfileJSON:   profileJSON,
			ActivePermissionProfile: "workspace",
		},
	})
	return selection
}

func guardianTestPreparedConfig(t *testing.T, prepared *guardianReviewerPreparedOptions) *config.Config {
	t.Helper()
	if prepared == nil || prepared.Config == nil {
		t.Fatal("prepared reviewer config is nil")
	}
	return prepared.Config
}

// TestGuardianPrepareReviewerOptionsIsolatesParentConfigLikeRust mirrors Rust
// #40742 prepares_isolated_guardian_internal_session: the reviewer receives a
// copy of the parent config with every instruction, notification, MCP, memory,
// budget, and feature surface the parent may have configured removed.
func TestGuardianPrepareReviewerOptionsIsolatesParentConfigLikeRust(t *testing.T) {
	parent := &config.Config{Values: map[string]any{
		"model":                                   "gpt-parent",
		"model_context_window":                    100000,
		"model_auto_compact_token_limit":          50000,
		"developer_instructions":                  "parent developer instructions",
		"notify":                                  []any{"notify-parent"},
		"project_doc_max_bytes":                   4096,
		"personality":                             "pragmatic",
		"include_apps_instructions":               true,
		"include_collaboration_mode_instructions": true,
		"skills":                                  map[string]any{"include_instructions": true},
		"memories":                                map[string]any{"use_memories": true, "dedicated_tools": true},
		"mcp_servers":                             map[string]any{"parent-server": map[string]any{"command": "parent-mcp"}},
		"goals":                                   map[string]any{"max_goal_token_budget": 1000},
		"features": map[string]any{
			"apps":           true,
			"hooks":          true,
			"multi_agent":    true,
			"plugins":        true,
			"token_budget":   true,
			"tool_suggest":   true,
			"guardianv2":     map[string]any{"transcript_mode": "json"},
			"multi_agent_v2": map[string]any{"disable_native_tools": true},
		},
	}}

	prepared, err := guardianPrepareReviewerOptions(guardianReviewerPrepareInput{
		ParentConfig:                    parent,
		ParentModel:                     "gpt-parent",
		ReviewerModel:                   "codex-auto-review",
		ReviewerEffort:                  "low",
		BaseInstructions:                "reviewer policy prompt",
		PreserveClientDeveloperMessages: false,
		Ephemeral:                       true,
		ProviderID:                      "custom-provider",
		SessionID:                       "parent-session",
	})
	if err != nil {
		t.Fatalf("guardianPrepareReviewerOptions() error = %v", err)
	}
	if prepared.SessionSource != InternalSessionSourceGuardian || prepared.ThreadSource != ThreadSourceGuardianReview {
		t.Fatalf("prepared sources = %q/%q", prepared.SessionSource, prepared.ThreadSource)
	}
	if prepared.Model != "codex-auto-review" || prepared.ReasoningEffort != "low" {
		t.Fatalf("prepared model = %q effort %q", prepared.Model, prepared.ReasoningEffort)
	}
	if prepared.RequestMaxRetries != 1 || prepared.StreamMaxRetries != 1 {
		t.Fatalf("prepared retries = %d/%d, want 1/1", prepared.RequestMaxRetries, prepared.StreamMaxRetries)
	}
	if !prepared.Ephemeral || prepared.ProviderID != "custom-provider" || prepared.SessionID != "parent-session" {
		t.Fatalf("prepared persistence identity = %#v", prepared)
	}

	cfg := guardianTestPreparedConfig(t, prepared)
	values := cfg.Values
	if values["model"] != "codex-auto-review" || values["model_reasoning_effort"] != "low" {
		t.Fatalf("reviewer model values = %#v", values)
	}
	if values["base_instructions"] != "reviewer policy prompt" || values["base_instructions_provenance"] != "custom" {
		t.Fatalf("reviewer base instructions = %#v / %#v", values["base_instructions"], values["base_instructions_provenance"])
	}
	for _, key := range []string{"developer_instructions", "notify", "personality", "token_budget", "rollout_budget", "model_context_window", "model_auto_compact_token_limit"} {
		if _, present := values[key]; present {
			t.Fatalf("prepared config retained %q: %#v", key, values[key])
		}
	}
	if values["project_doc_max_bytes"] != 0 {
		t.Fatalf("project_doc_max_bytes = %#v, want 0", values["project_doc_max_bytes"])
	}
	if values["include_apps_instructions"] != false || values["include_collaboration_mode_instructions"] != false {
		t.Fatalf("include instructions = %#v/%#v", values["include_apps_instructions"], values["include_collaboration_mode_instructions"])
	}
	if skills, _ := values["skills"].(map[string]any); skills["include_instructions"] != false {
		t.Fatalf("skills.include_instructions = %#v, want false", skills["include_instructions"])
	}
	if memories, _ := values["memories"].(map[string]any); memories["use_memories"] != false || memories["dedicated_tools"] != false {
		t.Fatalf("memories = %#v", memories)
	}
	if mcp, _ := values["mcp_servers"].(map[string]any); len(mcp) != 0 {
		t.Fatalf("mcp_servers = %#v, want empty", mcp)
	}
	if goals, _ := values["goals"].(map[string]any); len(goals) != 0 {
		t.Fatalf("goals = %#v, want empty", goals)
	}
	if values["approval_policy"] != string(sandbox.ApprovalNever) {
		t.Fatalf("approval_policy = %#v, want never", values["approval_policy"])
	}
	for _, key := range guardianReviewerDisabledFeatures() {
		if features.Enabled(cfg.FeatureSettings(), key) {
			t.Fatalf("feature %q is still enabled for the reviewer", key)
		}
	}
	// Nested feature tables survive disablement so model-owned settings are kept
	// while the feature itself is off.
	guardianV2, _ := values["features"].(map[string]any)["guardianv2"].(map[string]any)
	if guardianV2["transcript_mode"] != "json" || guardianV2["enabled"] != false {
		t.Fatalf("guardianv2 table = %#v", guardianV2)
	}
	multiAgentV2, _ := values["features"].(map[string]any)["multi_agent_v2"].(map[string]any)
	if multiAgentV2["disable_native_tools"] != true || multiAgentV2["enabled"] != false {
		t.Fatalf("multi_agent_v2 table = %#v", multiAgentV2)
	}
	if features.Enabled(cfg.FeatureSettings(), "retain_client_developer_messages") {
		t.Fatal("retain_client_developer_messages enabled without a node-repl reviewer requirement")
	}

	// The parent configuration must not be mutated through the clone.
	if parent.Values["developer_instructions"] != "parent developer instructions" ||
		parent.Values["include_apps_instructions"] != true {
		t.Fatalf("parent config was mutated: %#v", parent.Values)
	}
	parentFeatures, _ := parent.Values["features"].(map[string]any)
	if parentFeatures["apps"] != true {
		t.Fatalf("parent feature table was mutated: %#v", parentFeatures)
	}
}

// TestGuardianPrepareReviewerOptionsKeepsSameModelContextLikeRust keeps the
// parent's context-window settings when the reviewer falls back to the parent
// model, matching Rust's `reviewer_model.info.slug != parent_model` guard.
func TestGuardianPrepareReviewerOptionsKeepsSameModelContextLikeRust(t *testing.T) {
	parent := &config.Config{Values: map[string]any{
		"model":                          "gpt-parent",
		"model_context_window":           100000,
		"model_auto_compact_token_limit": 50000,
	}}
	prepared, err := guardianPrepareReviewerOptions(guardianReviewerPrepareInput{
		ParentConfig:     parent,
		ParentModel:      "gpt-parent",
		ReviewerModel:    "gpt-parent",
		BaseInstructions: "policy",
	})
	if err != nil {
		t.Fatalf("guardianPrepareReviewerOptions() error = %v", err)
	}
	if prepared.Config.Values["model_context_window"] != 100000 ||
		prepared.Config.Values["model_auto_compact_token_limit"] != 50000 {
		t.Fatalf("same-model reviewer context settings = %#v", prepared.Config.Values)
	}
}

// TestGuardianPrepareReviewerOptionsRetainsClientDeveloperMessagesLikeRust
// enables RetainClientDeveloperMessages only when the parent model requires
// node-repl auto review.
func TestGuardianPrepareReviewerOptionsRetainsClientDeveloperMessagesLikeRust(t *testing.T) {
	parent := &config.Config{Values: map[string]any{"model": "gpt-parent"}}
	prepared, err := guardianPrepareReviewerOptions(guardianReviewerPrepareInput{
		ParentConfig:                    parent,
		ParentModel:                     "gpt-parent",
		ReviewerModel:                   "codex-auto-review",
		BaseInstructions:                "policy",
		PreserveClientDeveloperMessages: true,
	})
	if err != nil {
		t.Fatalf("guardianPrepareReviewerOptions() error = %v", err)
	}
	if !features.Enabled(prepared.Config.FeatureSettings(), "retain_client_developer_messages") {
		t.Fatal("retain_client_developer_messages was not enabled for a node-repl reviewer")
	}
}

// TestGuardianPrepareReviewerOptionsNarrowsEnvironmentsLikeRust covers Rust's
// read_only_permissions_preserve_parent_environments_and_denied_reads: every
// Ready attachment becomes read-only, keeps its deny-read entries, and keeps
// every other field, while FromThread stays untouched.
func TestGuardianPrepareReviewerOptionsNarrowsEnvironmentsLikeRust(t *testing.T) {
	denied := sandbox.FileSystemSandboxEntry{
		Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: "**/*.secret"},
		Access: sandbox.FileSystemAccessDeny,
	}
	primary := sandbox.PermissionProfile{
		SandboxPolicy:     sandbox.NewWorkspaceWritePolicy(),
		DeniedReadEntries: []sandbox.FileSystemSandboxEntry{denied},
	}
	secondary := sandbox.WorkspaceWritePermissionProfile()
	parent := []map[string]any{
		guardianTestReadySelection("primary", "/repo", &primary),
		guardianTestReadySelection("secondary", "/repo-2", &secondary),
		{"environmentId": "inherited"},
	}

	environments, err := guardianPrepareReviewerEnvironments(parent)
	if err != nil {
		t.Fatalf("guardianPrepareReviewerEnvironments() error = %v", err)
	}
	if len(environments) != 3 {
		t.Fatalf("environments = %d, want 3", len(environments))
	}
	for index, wantCWD := range []string{"/repo", "/repo-2"} {
		selection := environments[index]
		if selection["cwd"] != wantCWD {
			t.Fatalf("environment %d cwd = %#v", index, selection["cwd"])
		}
		resolved, err := environmentConfigStateFromAnyMap(selection)
		if err != nil {
			t.Fatalf("environment %d parse: %v", index, err)
		}
		if resolved.Kind != EnvironmentConfigReady || resolved.Config == nil {
			t.Fatalf("environment %d state = %#v", index, resolved)
		}
		profile := resolved.Config.PermissionProfile
		if profile == nil || profile.SandboxPolicy == nil || profile.SandboxPolicy.Kind != sandbox.SandboxReadOnly {
			t.Fatalf("environment %d profile = %#v, want read-only", index, profile)
		}
		if !resolved.Config.AllowLoginShell {
			t.Fatalf("environment %d lost allow_login_shell", index)
		}
		if resolved.Config.ActivePermissionProfile != "" {
			t.Fatalf("environment %d kept named profile %q", index, resolved.Config.ActivePermissionProfile)
		}
	}
	resolvedPrimary, _ := environmentConfigStateFromAnyMap(environments[0])
	if !resolvedPrimary.Config.PermissionProfile.HasDenyReadEntries() {
		t.Fatal("read-only primary environment lost the parent's deny-read entries")
	}
	inherited, _ := environmentConfigStateFromAnyMap(environments[2])
	if inherited.Kind != EnvironmentConfigFromThread {
		t.Fatalf("FromThread selection became %#v", inherited)
	}
}

// TestGuardianPrepareReviewerOptionsRejectsUnresolvedEnvironmentsLikeRust fails
// closed for Pending/Failed attachments instead of reviewing under unknown
// permissions.
func TestGuardianPrepareReviewerOptionsRejectsUnresolvedEnvironmentsLikeRust(t *testing.T) {
	cases := []struct {
		name      string
		selection map[string]any
	}{
		{"pending", map[string]any{"environmentId": "remote", "config": map[string]any{"state": "pending"}}},
		{"failed", map[string]any{"environmentId": "remote", "config": map[string]any{"state": "failed", "error": "boom"}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := guardianPrepareReviewerEnvironments([]map[string]any{testCase.selection})
			if err == nil || !strings.Contains(err.Error(), "guardian reviewer environment `remote` has no resolved permissions") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

// TestGuardianPrepareReviewerOptionsExternalSandboxFallbackLikeRust keeps an
// external-sandbox parent on the external boundary instead of returning nil.
func TestGuardianPrepareReviewerOptionsExternalSandboxFallbackLikeRust(t *testing.T) {
	external := sandbox.PermissionProfile{SandboxPolicy: sandbox.NewExternalSandboxPolicy(sandbox.NetworkEnabled)}
	readOnly := guardianReviewerReadOnlyProfile(&external)
	if readOnly == nil || readOnly.SandboxPolicy == nil || readOnly.SandboxPolicy.Kind != "external-sandbox" {
		t.Fatalf("external read-only profile = %#v", readOnly)
	}
	if readOnly.SandboxPolicy.ExternalNetwork != sandbox.NetworkRestricted {
		t.Fatalf("external network = %q, want restricted", readOnly.SandboxPolicy.ExternalNetwork)
	}
}

// TestGuardianReviewPlanPreparesReviewerSessionLikeRust wires the prepared
// options into the reviewed turn: session identity and ephemeral intent travel
// on the review request, and an unresolved environment fails the review closed.
func TestGuardianReviewPlanPreparesReviewerSessionLikeRust(t *testing.T) {
	const (
		threadID    = "thread-reviewer-prepared"
		turnID      = "turn-reviewer-prepared"
		parentModel = "gpt-parent"
	)
	catalog := []model.ModelInfo{
		{
			Slug:                     parentModel,
			Visibility:               "list",
			SupportedInAPI:           true,
			DefaultReasoningLevel:    "high",
			SupportedReasoningLevels: []string{"low", "medium", "high"},
		},
		{
			Slug:                     model.DefaultApprovalReviewPreferredModel,
			Visibility:               "list",
			SupportedInAPI:           true,
			DefaultReasoningLevel:    "medium",
			SupportedReasoningLevels: []string{"low", "medium"},
		},
	}
	var captured *model.AgentRequest
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		clone := *request
		captured = &clone
		return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"prepared"}`}, nil
	})
	router := NewRuntimeRouter(RuntimeServices{
		Agent:        agent,
		Models:       model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: catalog})),
		ThreadStatus: NewThreadStatusManager(),
	})
	parentProfile := sandbox.PermissionProfile{
		SandboxPolicy: sandbox.NewWorkspaceWritePolicy(),
		DeniedReadEntries: []sandbox.FileSystemSandboxEntry{{
			Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: "**/*.secret"},
			Access: sandbox.FileSystemAccessDeny,
		}},
	}
	params := &turn.TurnStartParams{
		ThreadID: threadID,
		Model:    parentModel,
		Environments: []map[string]any{
			guardianTestReadySelection("primary", "/repo", &parentProfile),
		},
	}
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), params); err != nil {
		t.Fatalf("register active turn: %v", err)
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{
		Model:      parentModel,
		ProviderID: "custom-provider",
		SessionID:  "parent-session",
		Ephemeral:  true,
	})

	plan := router.guardianReviewPlanForTurn(threadID, turnID)
	if plan.PreparedError != "" {
		t.Fatalf("plan.PreparedError = %q", plan.PreparedError)
	}
	if plan.Prepared == nil || plan.Prepared.PermissionProfile == nil {
		t.Fatalf("plan.Prepared = %#v", plan.Prepared)
	}
	if plan.Prepared.PermissionProfile.SandboxPolicy == nil ||
		plan.Prepared.PermissionProfile.SandboxPolicy.Kind != sandbox.SandboxReadOnly ||
		!plan.Prepared.PermissionProfile.HasDenyReadEntries() {
		t.Fatalf("prepared review profile = %#v", plan.Prepared.PermissionProfile)
	}

	reviewer := router.ensureGuardianReviewerWithPrewarm(agent, false)
	decision, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-prepared", state.Action{
		Type: "mcp_tool_call", Server: "apps", ToolName: "calendar",
	})
	if err != nil {
		t.Fatalf("Guardian review: %v", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("decision = %q, want approved", decision)
	}
	if captured == nil {
		t.Fatal("review request was not captured")
	}
	if captured.ProviderID != "custom-provider" || captured.SessionID != "parent-session" || !captured.Ephemeral {
		t.Fatalf("review request identity = %#v", captured)
	}
	if captured.PermissionProfile == nil || captured.PermissionProfile.SandboxPolicy == nil ||
		captured.PermissionProfile.SandboxPolicy.Kind != sandbox.SandboxReadOnly ||
		!captured.PermissionProfile.HasDenyReadEntries() {
		t.Fatalf("review request profile = %#v", captured.PermissionProfile)
	}

	// Pending permissions fail closed before any model call.
	blockedRouter := NewRuntimeRouter(RuntimeServices{
		Agent: guardianAgentFunc(func(context.Context, *model.AgentRequest) (*model.AgentResponse, error) {
			t.Fatal("reviewer model must not run for an unresolved environment")
			return nil, nil
		}),
		Models:       model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: catalog})),
		ThreadStatus: NewThreadStatusManager(),
	})
	blockedParams := &turn.TurnStartParams{
		ThreadID: threadID + "-pending",
		Model:    parentModel,
		Environments: []map[string]any{{
			"environmentId": "remote",
			"config":        map[string]any{"state": "pending"},
		}},
	}
	if err := blockedRouter.registerActiveRuntimeTurn(threadID+"-pending", turnID+"-pending", func() {}, time.Now().UnixMilli(), blockedParams); err != nil {
		t.Fatalf("register blocked turn: %v", err)
	}
	blockedRouter.updateActiveRuntimeTurnAnalytics(threadID+"-pending", turnID+"-pending", "", &appTurnRunConfig{Model: parentModel})
	blockedReviewer := blockedRouter.ensureGuardianReviewerWithPrewarm(blockedRouter.services.Agent, false)
	blockedDecision, message, err := blockedReviewer.Review(context.Background(), threadID+"-pending", turnID+"-pending", "call-pending", state.Action{
		Type: "mcp_tool_call", Server: "apps", ToolName: "calendar",
	})
	if err != nil {
		t.Fatalf("blocked Guardian review error: %v", err)
	}
	if blockedDecision != state.DecisionDenied {
		t.Fatalf("blocked decision = %q, want denied", blockedDecision)
	}
	if !strings.Contains(message, "no resolved permissions") && !strings.Contains(message, "could not prepare") {
		t.Fatalf("blocked message = %q", message)
	}
}
