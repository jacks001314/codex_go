package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

const granularNoSandboxApprovalConfig = "approval_policy = { granular = { sandbox_approval = false, rules = true, skill_approval = true, request_permissions = true, mcp_elicitations = true } }\n"

func newWriteStdinActiveGrantRouter(t *testing.T, configBody string, reviewer GuardianReviewer) *RuntimeRouter {
	t.Helper()
	home := t.TempDir()
	cwd := t.TempDir()
	body := "model = \"gpt-5.4\"\n" + configBody + "\n[features]\nwrite_stdin_approval = true\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:       cwd,
		Config:           config.NewConfigService(home),
		GuardianReviewer: reviewer,
	})
	t.Cleanup(func() { _ = router.Close() })
	params := &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-5.4"}
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	return router
}

func activeGrantWriteStdinRequest(profile *sandbox.AdditionalPermissionProfile) *tool.WriteStdinApprovalRequest {
	return &tool.WriteStdinApprovalRequest{
		ProcessID:             9,
		CallID:                "write-call",
		LaunchCallID:          "exec-call",
		ThreadID:              "thread-1",
		TurnID:                "turn-1",
		EnvironmentID:         "local",
		Chars:                 "y\n",
		TTY:                   true,
		SandboxPermissions:    sandbox.SandboxPermissionsWithAdditionalPermissions,
		AdditionalPermissions: profile,
	}
}

// Rust #54119: a terminal retaining additional permissions still reaches
// approval review when an identical grant is active for its environment.
func TestWriteStdinActiveGrantReachesReviewLikeRust(t *testing.T) {
	router := newWriteStdinActiveGrantRouter(t, granularNoSandboxApprovalConfig, nil)
	root := t.TempDir()
	retained := &sandbox.AdditionalPermissionProfile{
		FileSystem:     []string{filepath.Join(root, "first"), filepath.Join(root, "second")},
		ReadFileSystem: []string{filepath.Join(root, "read")},
	}
	router.recordPermissionGrant("thread-1", "turn-1", "local", string(PermissionGrantScopeTurn), retained)
	prompted := false
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		prompted = true
		router.requireServerRequests().Resolve(OK(request.ID, &CommandExecutionRequestApprovalResponse{Decision: CommandExecutionApprovalAccept}))
	}))
	if err := router.writeStdinApproval(context.Background(), activeGrantWriteStdinRequest(retained)); err != nil {
		t.Fatalf("writeStdinApproval() error = %v", err)
	}
	if !prompted {
		t.Fatal("an active grant must still review the input")
	}
}

// Rust #54119: session grants stay active after the launching turn.
func TestWriteStdinSessionGrantReachesLaterReviewLikeRust(t *testing.T) {
	router := newWriteStdinActiveGrantRouter(t, granularNoSandboxApprovalConfig, nil)
	retained := &sandbox.AdditionalPermissionProfile{FileSystem: []string{filepath.Join(t.TempDir(), "shared")}}
	router.recordPermissionGrant("thread-1", "", "local", string(PermissionGrantScopeSession), retained)
	prompted := false
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		prompted = true
		router.requireServerRequests().Resolve(OK(request.ID, &CommandExecutionRequestApprovalResponse{Decision: CommandExecutionApprovalAccept}))
	}))
	if err := router.writeStdinApproval(context.Background(), activeGrantWriteStdinRequest(retained)); err != nil {
		t.Fatalf("writeStdinApproval() error = %v", err)
	}
	if !prompted {
		t.Fatal("a session grant must still review the input")
	}
}

// Rust #54119: guardian review uses the same active-grant gate and still
// reviews the input.
func TestWriteStdinActiveGrantRoutesToGuardianLikeRust(t *testing.T) {
	reviewer := &fakeGuardianReviewer{decision: state.DecisionApproved}
	router := newWriteStdinActiveGrantRouter(t, granularNoSandboxApprovalConfig+"approvals_reviewer = \"auto_review\"\n", reviewer)
	retained := &sandbox.AdditionalPermissionProfile{FileSystem: []string{filepath.Join(t.TempDir(), "shared")}}
	router.recordPermissionGrant("thread-1", "turn-1", "local", string(PermissionGrantScopeTurn), retained)
	prompted := false
	router.SetServerRequestSink(ServerRequestSinkFunc(func(*ServerRequest) { prompted = true }))
	if err := router.writeStdinApproval(context.Background(), activeGrantWriteStdinRequest(retained)); err != nil {
		t.Fatalf("writeStdinApproval() error = %v", err)
	}
	if prompted {
		t.Fatal("an auto-review turn must not prompt the user")
	}
	if len(reviewer.actions) != 1 || reviewer.actions[0].Type != "write_stdin" {
		t.Fatalf("guardian actions = %#v", reviewer.actions)
	}
}

// Rust #54119: stale or mismatched retained authority never reaches review.
func TestWriteStdinStaleOrMismatchedGrantIsRejectedLikeRust(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	read := filepath.Join(root, "read")
	other := filepath.Join(root, "other")
	networkEnabled := true
	tests := []struct {
		name   string
		config string
		grant  *sandbox.AdditionalPermissionProfile
		scope  PermissionGrantScope
	}{
		{name: "grant expired", config: granularNoSandboxApprovalConfig},
		{
			name:   "filesystem order changed",
			config: granularNoSandboxApprovalConfig,
			grant:  &sandbox.AdditionalPermissionProfile{FileSystem: []string{second, first}},
			scope:  PermissionGrantScopeTurn,
		},
		{
			name:   "read grant mismatch",
			config: granularNoSandboxApprovalConfig,
			grant:  &sandbox.AdditionalPermissionProfile{FileSystem: []string{first}, ReadFileSystem: []string{other}},
			scope:  PermissionGrantScopeTurn,
		},
		{
			name:   "network grant lost",
			config: granularNoSandboxApprovalConfig,
			grant:  &sandbox.AdditionalPermissionProfile{FileSystem: []string{first}},
			scope:  PermissionGrantScopeTurn,
		},
		{
			name:   "approval policy never",
			config: "approval_policy = \"never\"\n",
			grant:  &sandbox.AdditionalPermissionProfile{FileSystem: []string{first}},
			scope:  PermissionGrantScopeTurn,
		},
	}
	retained := &sandbox.AdditionalPermissionProfile{
		FileSystem:     []string{first, second},
		ReadFileSystem: []string{read},
	}
	networkRetained := &sandbox.AdditionalPermissionProfile{
		FileSystem:     []string{first},
		ReadFileSystem: []string{read},
		Network:        &networkEnabled,
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := newWriteStdinActiveGrantRouter(t, test.config, nil)
			if test.grant != nil {
				router.recordPermissionGrant("thread-1", "turn-1", "local", string(test.scope), test.grant)
			}
			request := activeGrantWriteStdinRequest(retained)
			if test.name == "network grant lost" {
				request.AdditionalPermissions = networkRetained
			}
			err := router.writeStdinApproval(context.Background(), request)
			if err == nil {
				t.Fatal("writeStdinApproval() = nil, want a policy rejection")
			}
			want := "sandbox_approval is false"
			if test.name == "approval policy never" {
				want = "set to Never"
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("writeStdinApproval() error = %v, want %q", err, want)
			}
		})
	}
}

// Rust #54119: an active network grant permits review when the terminal
// retained network access.
func TestWriteStdinActiveNetworkGrantReachesReviewLikeRust(t *testing.T) {
	router := newWriteStdinActiveGrantRouter(t, granularNoSandboxApprovalConfig, nil)
	root := t.TempDir()
	networkEnabled := true
	retained := &sandbox.AdditionalPermissionProfile{FileSystem: []string{filepath.Join(root, "first")}, Network: &networkEnabled}
	router.recordPermissionGrant("thread-1", "turn-1", "local", string(PermissionGrantScopeTurn), retained)
	prompted := false
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		prompted = true
		router.requireServerRequests().Resolve(OK(request.ID, &CommandExecutionRequestApprovalResponse{Decision: CommandExecutionApprovalAccept}))
	}))
	if err := router.writeStdinApproval(context.Background(), activeGrantWriteStdinRequest(retained)); err != nil {
		t.Fatalf("writeStdinApproval() error = %v", err)
	}
	if !prompted {
		t.Fatal("an active network grant must still review the input")
	}
}
