package app

import "testing"

func TestPendingAppServerRequestsNoteAndResolveApprovalsMatchRust(t *testing.T) {
	pending := NewPendingAppServerRequests()
	if unsupported := pending.NoteServerRequest(ServerRequest{
		ID:         "req-exec",
		Kind:       ServerRequestCommandExecutionApproval,
		ItemID:     "item-exec",
		ApprovalID: "approval-exec",
	}); unsupported != nil {
		t.Fatalf("exec approval unsupported = %#v", unsupported)
	}
	pending.NoteServerRequest(ServerRequest{
		ID:     "req-file",
		Kind:   ServerRequestFileChangeApproval,
		ItemID: "item-file",
	})
	pending.NoteServerRequest(ServerRequest{
		ID:     "req-perm",
		Kind:   ServerRequestPermissionsApproval,
		ItemID: "item-perm",
	})

	if pending.PendingCount() != 3 {
		t.Fatalf("PendingCount() = %d, want 3", pending.PendingCount())
	}
	resolved, ok := pending.ResolveNotification("req-exec")
	if !ok || resolved.Kind != ResolvedAppServerRequestExecApproval || resolved.ID != "approval-exec" {
		t.Fatalf("ResolveNotification(exec) = %#v/%v", resolved, ok)
	}
	resolved, ok = pending.ResolveNotification("req-file")
	if !ok || resolved.Kind != ResolvedAppServerRequestFileChangeApproval || resolved.ID != "item-file" {
		t.Fatalf("ResolveNotification(file) = %#v/%v", resolved, ok)
	}
	resolved, ok = pending.ResolveNotification("req-perm")
	if !ok || resolved.Kind != ResolvedAppServerRequestPermissionsApproval || resolved.ID != "item-perm" {
		t.Fatalf("ResolveNotification(permissions) = %#v/%v", resolved, ok)
	}
	if pending.PendingCount() != 0 {
		t.Fatalf("PendingCount() after resolves = %d, want 0", pending.PendingCount())
	}
}

func TestPendingAppServerRequestsUserInputFIFOMatchRust(t *testing.T) {
	pending := NewPendingAppServerRequests()
	pending.NoteServerRequest(ServerRequest{ID: "req-1", Kind: ServerRequestUserInput, TurnID: "turn-1", ItemID: "call-1"})
	pending.NoteServerRequest(ServerRequest{ID: "req-2", Kind: ServerRequestUserInput, TurnID: "turn-1", ItemID: "call-2"})

	if requestID, ok := pending.PopUserInputRequestForTurn("turn-1"); !ok || requestID != "req-1" {
		t.Fatalf("first PopUserInputRequestForTurn() = %q/%v", requestID, ok)
	}
	resolved, ok := pending.ResolveNotification("req-2")
	if !ok || resolved.Kind != ResolvedAppServerRequestUserInput || resolved.CallID != "call-2" {
		t.Fatalf("ResolveNotification(user input) = %#v/%v", resolved, ok)
	}
	if pending.PendingCount() != 0 {
		t.Fatalf("PendingCount() = %d, want 0", pending.PendingCount())
	}
}

func TestPendingAppServerRequestsScopesApprovalsByThread(t *testing.T) {
	// Rust #39372: approval ids can collide across concurrent threads; pending
	// tracking must key by both thread and id so resolution cannot dismiss the
	// wrong thread's request.
	pending := NewPendingAppServerRequests()
	pending.NoteServerRequest(ServerRequest{ID: "req-a", Kind: ServerRequestCommandExecutionApproval, ThreadID: rustThreadID1, ItemID: "item-1", ApprovalID: "approval-1"})
	pending.NoteServerRequest(ServerRequest{ID: "req-b", Kind: ServerRequestCommandExecutionApproval, ThreadID: rustThreadID2, ItemID: "item-1", ApprovalID: "approval-1"})
	if pending.PendingCount() != 2 {
		t.Fatalf("PendingCount() = %d, want 2", pending.PendingCount())
	}
	// Resolving thread A's request must not dismiss thread B's colliding id.
	resolved, ok := pending.ResolveNotification("req-a")
	if !ok || resolved.Kind != ResolvedAppServerRequestExecApproval || resolved.ID != "approval-1" || resolved.ThreadID != canonicalApprovalThreadID(rustThreadID1) {
		t.Fatalf("ResolveNotification(req-a) = %#v/%v", resolved, ok)
	}
	if pending.PendingCount() != 1 {
		t.Fatalf("PendingCount() after resolving thread A = %d, want 1", pending.PendingCount())
	}
	// The remaining request is thread B's.
	remaining, ok := pending.ResolveNotification("req-b")
	if !ok || remaining.ThreadID != canonicalApprovalThreadID(rustThreadID2) {
		t.Fatalf("ResolveNotification(req-b) = %#v/%v", remaining, ok)
	}
}

func TestPendingAppServerRequestsMcpResolveMatchRust(t *testing.T) {
	pending := NewPendingAppServerRequests()
	pending.NoteServerRequest(ServerRequest{ID: "req-mcp", Kind: ServerRequestMcpElicitation, ServerName: "sentry"})

	resolved, ok := pending.ResolveNotification("req-mcp")
	if !ok || resolved.Kind != ResolvedAppServerRequestMcpElicitation || resolved.ServerName != "sentry" || resolved.RequestID != "req-mcp" {
		t.Fatalf("ResolveNotification(mcp) = %#v/%v", resolved, ok)
	}
}

func TestPendingAppServerRequestsUnsupportedMatchRust(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{ServerRequestDynamicToolCall, "Dynamic tool calls are not available in TUI yet."},
		{ServerRequestAttestationGenerate, "Attestation generation is not available in TUI."},
		{ServerRequestCurrentTimeRead, "External current time is not available in TUI."},
		{ServerRequestApplyPatchApproval, "Legacy patch approval requests are not available in TUI yet."},
		{ServerRequestExecCommandApproval, "Legacy command approval requests are not available in TUI yet."},
	}
	for _, tc := range cases {
		pending := NewPendingAppServerRequests()
		unsupported := pending.NoteServerRequest(ServerRequest{ID: "req", Kind: tc.kind})
		if unsupported == nil || unsupported.RequestID != "req" || unsupported.Message != tc.want {
			t.Fatalf("unsupported %s = %#v, want %q", tc.kind, unsupported, tc.want)
		}
		if !pending.ContainsServerRequest(ServerRequest{ID: "req", Kind: tc.kind}) {
			t.Fatalf("ContainsServerRequest(%s) = false, want true for unsupported parity", tc.kind)
		}
	}

	pending := NewPendingAppServerRequests()
	if unsupported := pending.NoteServerRequest(ServerRequest{ID: "auth", Kind: ServerRequestChatGPTAuthTokensRefresh}); unsupported != nil {
		t.Fatalf("auth refresh unsupported = %#v, want nil", unsupported)
	}
}

func TestPendingAppServerRequestsContainsAndClear(t *testing.T) {
	pending := NewPendingAppServerRequests()
	request := ServerRequest{ID: "req-exec", Kind: ServerRequestCommandExecutionApproval, ItemID: "item-exec"}
	pending.NoteServerRequest(request)
	if !pending.ContainsServerRequest(request) {
		t.Fatal("ContainsServerRequest(exec) = false, want true")
	}
	pending.Clear()
	if pending.PendingCount() != 0 {
		t.Fatalf("PendingCount() after clear = %d, want 0", pending.PendingCount())
	}
	if pending.ContainsServerRequest(request) {
		t.Fatal("ContainsServerRequest(exec after clear) = true, want false")
	}
}
