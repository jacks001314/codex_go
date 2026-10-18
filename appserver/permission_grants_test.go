package appserver

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"codex_go/sandbox"
	"codex_go/tool"
	"codex_go/turn"
)

func TestPermissionGrantsMergeTurnAndSessionThenClearLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	sessionPath := filepath.Join(t.TempDir(), "session")
	turnPath := filepath.Join(t.TempDir(), "turn")
	router.recordPermissionGrant("thread-1", "turn-1", "remote", string(PermissionGrantScopeSession),
		&sandbox.AdditionalPermissionProfile{FileSystem: []string{sessionPath}})
	router.recordPermissionGrant("thread-1", "turn-1", "remote", string(PermissionGrantScopeTurn),
		&sandbox.AdditionalPermissionProfile{ReadFileSystem: []string{turnPath}})
	granted := router.grantedPermissionsForEnvironment("thread-1", "turn-1", "remote")
	if granted == nil || len(granted.FileSystem) != 1 || granted.FileSystem[0] != sessionPath ||
		len(granted.ReadFileSystem) != 1 || granted.ReadFileSystem[0] != turnPath {
		t.Fatalf("granted permissions = %#v", granted)
	}

	router.clearTurnPermissionGrants("thread-1", "turn-1")
	granted = router.grantedPermissionsForEnvironment("thread-1", "turn-1", "remote")
	if granted == nil || len(granted.FileSystem) != 1 || len(granted.ReadFileSystem) != 0 {
		t.Fatalf("session grant after turn clear = %#v", granted)
	}
	router.forgetPermissionGrants("thread-1")
	if granted := router.grantedPermissionsForEnvironment("thread-1", "turn-2", "remote"); granted != nil {
		t.Fatalf("grant survived thread cleanup: %#v", granted)
	}
}

func TestRequestedPermissionSubsetCannotWidenGrantLikeRust(t *testing.T) {
	cwd := t.TempDir()
	requestedRoot := filepath.Join(cwd, "requested")
	outside := filepath.Join(cwd, "outside")
	network := true
	granted := intersectRequestedPermissions(
		&sandbox.AdditionalPermissionProfile{
			FileSystem: []string{requestedRoot},
			Network:    &network,
		},
		&sandbox.AdditionalPermissionProfile{
			FileSystem: []string{filepath.Join(requestedRoot, "nested"), outside},
			Network:    &network,
		},
		cwd,
	)
	if granted.Network == nil || !*granted.Network {
		t.Fatalf("network grant = %#v", granted.Network)
	}
	if len(granted.FileSystem) != 1 || granted.FileSystem[0] != filepath.Join(requestedRoot, "nested") {
		t.Fatalf("filesystem grant = %#v", granted.FileSystem)
	}
}

func TestRequestPermissionsUserApprovalRecordsGrantLikeRust(t *testing.T) {
	broker := NewServerRequestBroker()
	requests := make(chan *ServerRequest, 1)
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) { requests <- request }))
	router := NewRuntimeRouter(RuntimeServices{ServerRequests: broker})
	cwd := t.TempDir()
	requestedProfile, err := additionalPermissionProfileFromJSONMap(map[string]any{
		"fileSystem": map[string]any{"write": []string{cwd}},
	})
	if err != nil || len(requestedProfile.FileSystem) != 1 {
		t.Fatalf("requested profile = %#v err=%v", requestedProfile, err)
	}
	if subset := intersectRequestedPermissions(requestedProfile, &sandbox.AdditionalPermissionProfile{FileSystem: []string{cwd}}, cwd); len(subset.FileSystem) != 1 {
		t.Fatalf("direct subset = %#v", subset)
	}
	params := &turn.TurnStartParams{ThreadID: "thread-grant", CWD: cwd, ApprovalPolicy: sandbox.ApprovalOnRequest}
	if err := router.threads.RegisterTurn("thread-grant", "turn-grant", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}

	type result struct {
		decision tool.RequestPermissionsDecision
		err      error
	}
	done := make(chan result, 1)
	go func() {
		decision, err := router.requestPermissionsGuardianReviewer("thread-grant")(
			context.Background(), "", "turn-grant", "call-grant", "remote", "need writes",
			map[string]any{"fileSystem": map[string]any{"write": []string{cwd}}},
		)
		done <- result{decision: decision, err: err}
	}()
	var pending *ServerRequest
	select {
	case pending = <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for permission approval")
	}
	allowed := false
	if ok, err := broker.Resolve(OK(pending.ID, &PermissionsRequestApprovalResponse{
		Permissions: &GrantedPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{
			Write: []string{cwd},
		}},
		Scope:            PermissionGrantScopeSession,
		StrictAutoReview: &allowed,
	})); err != nil || !ok {
		t.Fatalf("Resolve() ok=%v err=%v", ok, err)
	}
	select {
	case result := <-done:
		if result.err != nil || !result.decision.Approved || result.decision.Permissions == nil ||
			len(result.decision.Permissions.FileSystem) != 1 || result.decision.Scope != "session" {
			t.Fatalf("decision permissions = %#v scope=%q approved=%v err=%v",
				result.decision.Permissions, result.decision.Scope, result.decision.Approved, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for permission decision")
	}
	granted := router.grantedPermissionsForEnvironment("thread-grant", "turn-grant", "remote")
	if granted == nil || len(granted.FileSystem) != 1 || granted.FileSystem[0] != cwd {
		t.Fatalf("recorded grant = %#v", granted)
	}
}
