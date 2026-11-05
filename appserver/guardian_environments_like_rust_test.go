package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"codex_go/mcp"
	"codex_go/model"
	"codex_go/sandbox"
	"codex_go/state"
)

func guardianEnvironmentAllowResponse() *model.AgentResponse {
	return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}
}

// Rust #47630 ReviewAction::validate denies an environment-scoped action whose
// environment is not part of the reviewed turn, before any Guardian inference.
func TestGuardianReviewRejectsUnavailableTargetEnvironmentLikeRust(t *testing.T) {
	calls := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			return guardianEnvironmentAllowResponse(), nil
		}),
		store: state.NewReviewStore(),
		targetEnvironmentAvailable: func(threadID, turnID, environmentID string) bool {
			return environmentID == "primary"
		},
	}
	decision, reason, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{
		Type:          "command",
		Command:       "ls",
		CWD:           "/repo",
		EnvironmentID: "secondary",
	})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if decision != state.DecisionDenied {
		t.Fatalf("Review() decision = %q, want denied", decision)
	}
	if reason != "automatic approval review cannot access the request's environment" {
		t.Fatalf("Review() reason = %q", reason)
	}
	if calls != 0 {
		t.Fatalf("Guardian inference ran %d times for an unavailable environment", calls)
	}

	sessionID := 7
	decision, reason, err = reviewer.Review(context.Background(), "thread-1", "turn-1", "call-2", state.Action{
		Type:          "write_stdin",
		EnvironmentID: "secondary",
		SessionID:     &sessionID,
		Chars:         "ls\n",
		CWD:           "/repo",
	})
	if err != nil {
		t.Fatalf("Review() write_stdin error = %v", err)
	}
	if decision != state.DecisionDenied || reason != "automatic approval review cannot access the terminal's environment; select it before retrying" {
		t.Fatalf("Review() write_stdin = %q/%q", decision, reason)
	}
	if calls != 0 {
		t.Fatalf("Guardian inference ran %d times for an unavailable terminal environment", calls)
	}

	decision, _, err = reviewer.Review(context.Background(), "thread-1", "turn-1", "call-3", state.Action{
		Type:          "write_stdin",
		EnvironmentID: "primary",
		SessionID:     &sessionID,
		Chars:         "ls\n",
		CWD:           "/repo",
	})
	if err != nil || decision != state.DecisionApproved || calls != 1 {
		t.Fatalf("Review() available write_stdin = %q, err=%v, calls=%d", decision, err, calls)
	}

	// MCP actions have no target environment (Rust's target_environment_id is
	// None), so the guard does not apply and the review still runs.
	mcpReviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			return guardianEnvironmentAllowResponse(), nil
		}),
		store: state.NewReviewStore(),
		targetEnvironmentAvailable: func(threadID, turnID, environmentID string) bool {
			return false
		},
	}
	decision, _, err = mcpReviewer.Review(context.Background(), "thread-1", "turn-1", "call-4", state.Action{
		Type:     "mcp_tool_call",
		Server:   "apps",
		ToolName: "calendar",
	})
	if err != nil || decision != state.DecisionApproved || calls != 2 {
		t.Fatalf("Review() mcp = %q, err=%v, calls=%d", decision, err, calls)
	}
}

// Rust #47630 rejects an environment id above 256 bytes before inference while
// leaving the action renderable for manual approval.
func TestGuardianReviewRejectsOversizedEnvironmentIDLikeRust(t *testing.T) {
	calls := 0
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			return guardianEnvironmentAllowResponse(), nil
		}),
		store: state.NewReviewStore(),
		targetEnvironmentAvailable: func(threadID, turnID, environmentID string) bool {
			return true
		},
	}
	oversized := strings.Repeat("a", state.GuardianMaxEnvironmentIDBytes+1)
	action := state.Action{
		Type:          "request_permissions",
		EnvironmentID: oversized,
		Permissions:   map[string]any{"network": map[string]any{"enabled": true}},
	}
	decision, message, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", action)
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if decision != state.DecisionDenied {
		t.Fatalf("Review() decision = %q, want denied", decision)
	}
	if !strings.Contains(message, "approval environment id exceeds Guardian's 256-byte limit") {
		t.Fatalf("Review() message = %q", message)
	}
	if calls != 0 {
		t.Fatalf("Guardian inference ran %d times for an oversized environment id", calls)
	}
	if _, _, formatErr := state.FormatGuardianActionPretty(action); formatErr != nil {
		t.Fatalf("FormatGuardianActionPretty() error = %v, want the action renderable for manual approval", formatErr)
	}
}

// Rust's guardian_environments_tests bind each review to the environment that
// owns the action while reusing one reviewer session across environments.
func TestGuardianReviewsTargetEnvironmentAndReusesPrefixLikeRust(t *testing.T) {
	var requests []*model.AgentRequest
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		captured := *request
		captured.InputItems = append([]any(nil), request.InputItems...)
		requests = append(requests, &captured)
		response := guardianEnvironmentAllowResponse()
		response.ResponseID = "resp-" + string(rune('1'+len(requests)-1))
		return response, nil
	})
	cwd := t.TempDir()
	deniedPath := filepath.Join(cwd, "secondary-private")
	reviewer := &modelGuardianReviewer{
		agent: &guardianSessionRunner{agent: agent},
		store: state.NewReviewStore(),
		targetEnvironmentAvailable: func(threadID, turnID, environmentID string) bool {
			return environmentID == "secondary" || environmentID == "primary"
		},
		turnPermissionProfile: func(threadID, turnID, environmentID string) (*sandbox.PermissionProfile, string, *string) {
			id := environmentID
			switch environmentID {
			case "secondary":
				profile := sandbox.WorkspaceWritePermissionProfile()
				profile.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{{
					Path:   sandbox.FileSystemPath{Type: "path", Path: deniedPath},
					Access: sandbox.FileSystemAccessDeny,
				}}
				return &profile, cwd, &id
			case "primary":
				profile := sandbox.ReadOnlyPermissionProfile()
				return &profile, cwd, &id
			default:
				return nil, "", nil
			}
		},
	}
	actions := []state.Action{
		{
			Type:          "command",
			Command:       "cat secondary-private",
			CWD:           cwd,
			EnvironmentID: "secondary",
		},
		{
			Type:          "apply_patch",
			CWD:           cwd,
			Files:         []string{filepath.Join(cwd, "guardian-marker.txt")},
			Patch:         "*** Begin Patch\n*** Environment ID: primary\n*** Add File: guardian-marker.txt\n+review-only\n*** End Patch\n",
			EnvironmentID: "primary",
		},
	}
	for i, action := range actions {
		decision, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "action-"+string(rune('0'+i)), action)
		if err != nil {
			t.Fatalf("Review() #%d error = %v", i+1, err)
		}
		if decision != state.DecisionApproved {
			t.Fatalf("Review() #%d decision = %q, want approved", i+1, decision)
		}
	}
	if len(requests) != 2 {
		t.Fatalf("review requests = %d, want 2", len(requests))
	}
	if requests[1].PreviousResponseID != "resp-1" {
		t.Fatalf("second review PreviousResponseID = %q, want the first response", requests[1].PreviousResponseID)
	}
	rendered := make([]string, 0, len(requests))
	for i, request := range requests {
		text := request.Prompt
		for _, item := range request.InputItems {
			encoded, marshalErr := json.Marshal(item)
			if marshalErr != nil {
				t.Fatalf("request #%d input item marshal error = %v", i+1, marshalErr)
			}
			text += "\n" + string(encoded)
		}
		rendered = append(rendered, text)
	}
	secondary := rendered[0]
	if !strings.Contains(secondary, `environment "secondary"`) || !strings.Contains(secondary, "- path `"+deniedPath+"`") {
		t.Fatalf("secondary review missing its denied-read evidence:\n%s", secondary)
	}
	if !strings.Contains(secondary, `"environment_id": "secondary"`) || !strings.Contains(secondary, `"tool": "exec_command"`) {
		t.Fatalf("secondary review missing the planned action environment:\n%s", secondary)
	}
	primary := rendered[1]
	if !strings.Contains(primary, `environment "primary"`) || !strings.Contains(primary, "has no explicit denied-read paths/globs") {
		t.Fatalf("primary review missing its own permission evidence:\n%s", primary)
	}
	if strings.Contains(primary, deniedPath) {
		t.Fatalf("primary review inherited the secondary denied path:\n%s", primary)
	}
	if !strings.Contains(primary, `"environment_id": "primary"`) || !strings.Contains(primary, `"tool": "apply_patch"`) {
		t.Fatalf("primary review missing the planned action environment:\n%s", primary)
	}
}

type guardianOriginCapturingReviewer struct {
	origin *guardianReviewOrigin
	action state.Action
}

func (r *guardianOriginCapturingReviewer) Review(ctx context.Context, _ string, _ string, _ string, action state.Action) (state.ReviewDecision, string, error) {
	r.origin = guardianReviewOriginFromContext(ctx)
	r.action = action
	return state.DecisionApproved, "", nil
}

// Rust #59028: an MCP call's Guardian review uses the permission source captured
// when the call was prepared, while a thread-owned server keeps the reviewed
// turn's evidence. The sandbox context stays out of the serialized action.
func TestGuardianMCPReviewUsesPreparedPermissionSourceLikeRust(t *testing.T) {
	reviewer := &guardianOriginCapturingReviewer{}
	router := NewRuntimeRouter(RuntimeServices{GuardianReviewer: reviewer})
	defer router.Close()
	handler := &appserverMCPToolApprovalHandler{router: router, threadID: "thread-1", turnID: "turn-1"}
	profile := sandbox.FullAccessPermissionProfile()
	request := &mcp.MCPToolApprovalRequest{
		Server:        "remote",
		Tool:          "query",
		CallID:        "call-1",
		Arguments:     map[string]any{"q": "secret"},
		EnvironmentID: "remote",
		PreparedPermissionSource: &mcp.MCPServerPermissionSource{
			Profile:       &profile,
			CWD:           "/remote",
			EnvironmentID: "remote",
		},
	}
	decision, askUser, err := handler.reviewViaGuardian(context.Background(), request)
	if err != nil || askUser || decision != mcp.MCPToolApprovalApprove {
		t.Fatalf("reviewViaGuardian() = %v askUser=%v err=%v, want approved", decision, askUser, err)
	}
	if reviewer.origin == nil {
		t.Fatal("Guardian review lost the prepared permission source")
	}
	if reviewer.origin.PermissionProfile == nil || !reviewer.origin.PermissionProfile.Disabled ||
		reviewer.origin.CWD != "/remote" || reviewer.origin.EnvironmentID != "remote" {
		t.Fatalf("Guardian origin = %#v, want the prepared remote owner", reviewer.origin)
	}
	if reviewer.action.Type != "mcp_tool_call" || reviewer.action.Server != "remote" || reviewer.action.ToolName != "query" {
		t.Fatalf("serialized action = %#v, want the MCP call only", reviewer.action)
	}

	threadProfile := sandbox.ReadOnlyPermissionProfile()
	request.PreparedPermissionSource = &mcp.MCPServerPermissionSource{
		Profile: &threadProfile,
		CWD:     "/thread",
		Thread:  true,
	}
	decision, askUser, err = handler.reviewViaGuardian(context.Background(), request)
	if err != nil || askUser || decision != mcp.MCPToolApprovalApprove {
		t.Fatalf("thread reviewViaGuardian() = %v askUser=%v err=%v, want approved", decision, askUser, err)
	}
	if reviewer.origin != nil {
		t.Fatalf("thread-owned MCP call overrode turn evidence with %#v", reviewer.origin)
	}
}

// Mirrors Rust #59489: an MCP server can belong to an environment other than the
// turn's primary one. A low score issued for the primary's rules must not skip
// the review when the prepared owner's permissions differ, while matching owner
// evidence still reuses the score.
func TestGuardianMCPOwnerDoesNotReusePrimaryScoreLikeRust(t *testing.T) {
	cwd := t.TempDir()
	deniedPath := filepath.Join(cwd, "owner-private")
	ownerProfile := sandbox.WorkspaceWritePermissionProfile()
	ownerProfile.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "path", Path: deniedPath},
		Access: sandbox.FileSystemAccessDeny,
	}}
	primaryProfile := sandbox.ReadOnlyPermissionProfile()
	primary := "primary"
	scored := state.ServingAuthorization{
		EnvironmentID:           &primary,
		DeniedPaths:             sandbox.UnreadableRootsWithCWD(&primaryProfile, cwd),
		DeniedGlobs:             sandbox.UnreadableGlobsWithCWD(&primaryProfile, cwd),
		RetainedContextComplete: true,
		PolicyFingerprint:       "model|policy",
	}
	if slices.Equal(sandbox.UnreadableRootsWithCWD(&ownerProfile, cwd), scored.DeniedPaths) {
		t.Fatal("test setup: the owner and primary denied paths must differ")
	}
	action := state.Action{Type: "mcp_tool_call", Server: "files", ToolName: "echo"}
	review := func(t *testing.T, origin *guardianReviewOrigin) int {
		t.Helper()
		reviewer, calls := servingAssessmentReviewer(t, &state.ServingAssessment{Authorization: scored}, scored)
		ctx := withGuardianReviewOrigin(context.Background(), origin)
		if decision, reason, err := reviewer.Review(ctx, "thread-owner", "turn-owner", "call-serving", action); err != nil || decision != state.DecisionApproved {
			t.Fatalf("Review() = %q, %q, %v; want approved", decision, reason, err)
		}
		return *calls
	}
	ownerOrigin := func(profile *sandbox.PermissionProfile, environmentID string) *guardianReviewOrigin {
		return &guardianReviewOrigin{
			CWD:                    cwd,
			EnvironmentID:          environmentID,
			PermissionProfile:      profile,
			PermissionEvidenceOnly: true,
			MCPOwnerPermissions:    true,
		}
	}
	if calls := review(t, ownerOrigin(&ownerProfile, "local")); calls != 1 {
		t.Fatalf("review calls = %d, want 1 when the MCP owner's rules differ from the score", calls)
	}
	if calls := review(t, ownerOrigin(&primaryProfile, "primary")); calls != 0 {
		t.Fatalf("review calls = %d, want the matching owner evidence to reuse the score", calls)
	}
	if calls := review(t, nil); calls != 0 {
		t.Fatalf("review calls = %d, want a thread-owned call to keep reusing its score", calls)
	}
}

// Mirrors Rust #59489: a follow-up approval from a local MCP server during a
// remote-primary turn keeps the action the server asked about but reviews the
// owner's captured rules. Thread-owned servers keep the turn's evidence.
func TestGuardianMCPElicitationUsesOwnerPermissionsLikeRust(t *testing.T) {
	ownerCWD := t.TempDir()
	deniedPath := filepath.Join(ownerCWD, "owner-private")
	ownerProfile := sandbox.WorkspaceWritePermissionProfile()
	ownerProfile.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "path", Path: deniedPath},
		Access: sandbox.FileSystemAccessDeny,
	}}
	service := mcp.NewMCPService(nil)
	release := service.RegisterToolInvocation("cua_repl", "call-1", mcp.MCPInvocationMetadata{
		ServerName: "cua_repl",
		ToolName:   "js",
		ThreadID:   "thread-node-repl",
		TurnID:     "turn-node-repl",
		PermissionSource: &mcp.MCPServerPermissionSource{
			Profile:       &ownerProfile,
			CWD:           ownerCWD,
			EnvironmentID: "local",
		},
	})
	defer release()

	reviewElicitation := func(t *testing.T, service *mcp.MCPService, callID string) (*guardianReviewOrigin, state.Action) {
		t.Helper()
		var origin *guardianReviewOrigin
		var reviewed state.Action
		reviewer := guardianReviewerFunc(func(ctx context.Context, _, _, _ string, action state.Action) (state.ReviewDecision, string, error) {
			origin = guardianReviewOriginFromContext(ctx)
			reviewed = action
			return state.DecisionApproved, "", nil
		})
		meta := nodeReplNestedReviewMeta()
		meta["callId"] = callID
		if _, err := (&appserverMCPElicitationHandler{service: service, reviewer: reviewer}).
			HandleMCPElicitation(context.Background(), nodeReplNestedReviewRequest("cua_repl", meta)); err != nil {
			t.Fatalf("HandleMCPElicitation() error = %v", err)
		}
		return origin, reviewed
	}

	origin, action := reviewElicitation(t, service, "call-1")
	if origin == nil || !origin.MCPOwnerPermissions || !origin.PermissionEvidenceOnly {
		t.Fatalf("elicitation origin = %#v, want the prepared owner's permission evidence", origin)
	}
	if origin.EnvironmentID != "local" || origin.CWD != ownerCWD || origin.PermissionProfile == nil {
		t.Fatalf("elicitation origin = %#v, want the local MCP owner", origin)
	}
	if denied := sandbox.UnreadableRootsWithCWD(origin.PermissionProfile, origin.CWD); !slices.Contains(denied, deniedPath) {
		t.Fatalf("elicitation origin denied paths = %#v, want %s", denied, deniedPath)
	}
	if action.Server != "cua_repl" || action.ToolName != "browser.click" || action.Type != "mcp_tool_call" {
		t.Fatalf("reviewed action = %#v, want the nested action the server asked about", action)
	}

	threadService := mcp.NewMCPService(nil)
	threadProfile := sandbox.ReadOnlyPermissionProfile()
	threadRelease := threadService.RegisterToolInvocation("cua_repl", "call-2", mcp.MCPInvocationMetadata{
		ServerName:       "cua_repl",
		ToolName:         "js",
		PermissionSource: &mcp.MCPServerPermissionSource{Profile: &threadProfile, CWD: ownerCWD, Thread: true},
	})
	defer threadRelease()
	if origin, _ := reviewElicitation(t, threadService, "call-2"); origin != nil {
		t.Fatalf("thread-owned elicitation overrode turn evidence with %#v", origin)
	}
}
