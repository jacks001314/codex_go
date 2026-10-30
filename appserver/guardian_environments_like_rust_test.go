package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

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
