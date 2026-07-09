package bottompane

import (
	"reflect"
	"testing"

	"codex_go/internal/sandbox"
	"codex_go/internal/tui"
)

func TestExecApprovalOptionsMatchRustLabels(t *testing.T) {
	options := ExecApprovalOptions([]ApprovalCommandDecision{
		{Kind: ApprovalCommandAccept},
		{Kind: ApprovalCommandAcceptForSession},
		{Kind: ApprovalCommandAcceptWithExecpolicyAmendment, ExecpolicyCommand: []string{"echo"}},
		{Kind: ApprovalCommandCancel},
	}, nil, nil)
	if got := approvalOptionLabels(options); !reflect.DeepEqual(got, []string{
		"Yes, proceed",
		"Yes, and don't ask again for this command in this session",
		"Yes, and don't ask again for commands that start with `echo`",
		"No, and tell Codex what to do differently",
	}) {
		t.Fatalf("generic labels = %#v", got)
	}

	network := &ApprovalNetworkContext{Host: "example.com", Protocol: "https"}
	options = ExecApprovalOptions([]ApprovalCommandDecision{
		{Kind: ApprovalCommandAccept},
		{Kind: ApprovalCommandAcceptForSession},
		{Kind: ApprovalCommandAcceptWithExecpolicyAmendment, ExecpolicyCommand: []string{"curl"}},
		{Kind: ApprovalCommandApplyNetworkPolicyAmendment, NetworkPolicyHost: "example.com", NetworkPolicyAction: ApprovalNetworkPolicyAllow},
		{Kind: ApprovalCommandCancel},
	}, network, nil)
	if got := approvalOptionLabels(options); !reflect.DeepEqual(got, []string{
		"Yes, just this once",
		"Yes, and allow this host for this conversation",
		"Yes, and allow this host in the future",
		"No, and tell Codex what to do differently",
	}) {
		t.Fatalf("network labels = %#v", got)
	}
}

func TestPermissionsPatchAndElicitationOptionsMatchRustLabels(t *testing.T) {
	if got := approvalOptionLabels(PermissionsApprovalOptions()); !reflect.DeepEqual(got, []string{
		"Yes, grant these permissions for this turn",
		"Yes, grant for this turn with strict auto review",
		"Yes, grant these permissions for this session",
		"No, continue without permissions",
	}) {
		t.Fatalf("permissions labels = %#v", got)
	}
	if got := approvalOptionLabels(PatchApprovalOptions()); !reflect.DeepEqual(got, []string{
		"Yes, proceed",
		"Yes, and don't ask again for these files",
		"No, and tell Codex what to do differently",
	}) {
		t.Fatalf("patch labels = %#v", got)
	}
	if got := approvalOptionLabels(McpElicitationApprovalOptions()); !reflect.DeepEqual(got, []string{
		"Yes, provide the requested info",
		"No, but continue without it",
		"Cancel this request",
	}) {
		t.Fatalf("elicitation labels = %#v", got)
	}
}

func TestFormatApprovalPermissionsRuleMatchesRust(t *testing.T) {
	permissions := approvalTestPermissions()
	if got := FormatApprovalPermissionsRule(permissions); got != "network; read `/tmp/readme.txt`; write `/tmp/out.txt`" {
		t.Fatalf("permission rule = %q", got)
	}

	root := sandbox.FileSystemPath{Type: "special", Value: &sandbox.FileSystemSpecialPath{Kind: "root"}}
	glob := sandbox.FileSystemPath{Type: "glob_pattern", Pattern: "**/*.env"}
	permissions = &sandbox.RequestPermissionProfile{FileSystem: &sandbox.AdditionalFileSystemPermissions{Entries: []sandbox.FileSystemSandboxEntry{
		{Path: root, Access: sandbox.FileSystemAccessWrite},
		{Path: glob, Access: sandbox.FileSystemAccessDeny},
	}}}
	if got := FormatApprovalPermissionsRule(permissions); got != "write `:root`; deny read glob `**/*.env`" {
		t.Fatalf("special rule = %q", got)
	}

	subpath := ".git"
	workspace := sandbox.FileSystemPath{Type: "special", Value: &sandbox.FileSystemSpecialPath{Kind: "project_roots", Subpath: &subpath}}
	permissions = &sandbox.RequestPermissionProfile{FileSystem: &sandbox.AdditionalFileSystemPermissions{Entries: []sandbox.FileSystemSandboxEntry{
		{Path: workspace, Access: sandbox.FileSystemAccessRead},
	}}}
	if got := FormatApprovalPermissionsRule(permissions); got != "read `:workspace_roots/.git`" {
		t.Fatalf("workspace rule = %q", got)
	}
}

func TestApprovalOverlayRowsMatchRustSnapshots(t *testing.T) {
	exec := NewApprovalOverlay(ApprovalRequest{
		Kind:                  ApprovalRequestExec,
		ThreadID:              "thread-1",
		ID:                    "exec-1",
		Command:               []string{"cat", "/tmp/readme.txt"},
		Reason:                "need filesystem access",
		AvailableDecisions:    []ApprovalCommandDecision{{Kind: ApprovalCommandAccept}, {Kind: ApprovalCommandCancel}},
		AdditionalPermissions: approvalTestPermissions(),
	})
	rows := exec.Rows(120)
	for _, want := range []string{
		"Would you like to run the following command?",
		"Reason: need filesystem access",
		"Permission rule: network; read `/tmp/readme.txt`; write `/tmp/out.txt`",
		"$ cat /tmp/readme.txt",
		"Press enter to confirm or esc to cancel",
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("exec rows missing %q: %#v", want, rows)
		}
	}
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow(tui.NumberedSelectionPrefix(0, true)+"Yes, proceed (y)")) {
		t.Fatalf("exec rows missing selected option: %#v", rows)
	}

	permissions := NewApprovalOverlay(ApprovalRequest{
		Kind:        ApprovalRequestPermissions,
		ThreadID:    "thread-1",
		CallID:      "permissions-1",
		Reason:      "need workspace access",
		Permissions: approvalTestPermissions(),
	})
	rows = permissions.Rows(120)
	for _, want := range []string{
		"Would you like to grant these permissions?",
		"Reason: need workspace access",
		"Permission rule: network; read `/tmp/readme.txt`; write `/tmp/out.txt`",
		tui.NumberedSelectionPrefix(1, false) + "Yes, grant for this turn with strict auto review (r)",
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("permissions rows missing %q: %#v", want, rows)
		}
	}
}

func TestApprovalOverlayNetworkPromptHidesCommandAndUsesNetworkLabels(t *testing.T) {
	overlay := NewApprovalOverlay(ApprovalRequest{
		Kind:     ApprovalRequestExec,
		ThreadID: "thread-1",
		ID:       "exec-1",
		Command:  []string{"curl", "https://example.com"},
		Reason:   "network request blocked",
		NetworkContext: &ApprovalNetworkContext{
			Host:     "example.com",
			Protocol: "https",
		},
		AvailableDecisions: []ApprovalCommandDecision{
			{Kind: ApprovalCommandAccept},
			{Kind: ApprovalCommandAcceptForSession},
			{Kind: ApprovalCommandApplyNetworkPolicyAmendment, NetworkPolicyHost: "example.com", NetworkPolicyAction: ApprovalNetworkPolicyAllow},
			{Kind: ApprovalCommandCancel},
		},
	})
	rows := overlay.Rows(100)
	for _, want := range []string{
		`Do you want to approve network access to "example.com"?`,
		"Reason: network request blocked",
		tui.NumberedSelectionPrefix(1, false) + "Yes, and allow this host for this conversation (a)",
		tui.NumberedSelectionPrefix(2, false) + "Yes, and allow this host in the future (p)",
	} {
		if !bottomPaneContainsRow(rows, want) {
			t.Fatalf("network rows missing %q: %#v", want, rows)
		}
	}
	if bottomPaneContainsRow(rows, "$ curl https://example.com") {
		t.Fatalf("network prompt should hide command: %#v", rows)
	}
}

func TestApprovalOverlayShortcutsEmitDecisionsAndAdvanceQueue(t *testing.T) {
	overlay := NewApprovalOverlay(ApprovalRequest{
		Kind:               ApprovalRequestExec,
		ThreadID:           "thread-1",
		ID:                 "exec-1",
		Command:            []string{"echo", "hi"},
		AvailableDecisions: []ApprovalCommandDecision{{Kind: ApprovalCommandAccept}, {Kind: ApprovalCommandCancel}},
	})
	overlay.EnqueueRequest(ApprovalRequest{
		Kind:        ApprovalRequestPermissions,
		ThreadID:    "thread-1",
		CallID:      "permissions-1",
		Permissions: approvalTestPermissions(),
	})
	overlay.HandleKey("y")
	if overlay.IsComplete() || overlay.CurrentRequest == nil || overlay.CurrentRequest.Kind != ApprovalRequestPermissions {
		t.Fatalf("queue state complete=%v current=%#v", overlay.IsComplete(), overlay.CurrentRequest)
	}
	overlay.HandleKey("r")
	want := []ApprovalEvent{
		{Kind: ApprovalEventExecDecision, ThreadID: "thread-1", ID: "exec-1", CommandDecision: ApprovalCommandDecision{Kind: ApprovalCommandAccept}},
		{Kind: ApprovalEventPermissionsDecision, ThreadID: "thread-1", CallID: "permissions-1", Permissions: ApprovalPermissionsGrantForTurnStrictAutoReview},
	}
	if !overlay.IsComplete() || !reflect.DeepEqual(overlay.Events(), want) {
		t.Fatalf("events = %#v complete=%v", overlay.Events(), overlay.IsComplete())
	}
}

func TestApprovalOverlayCancelAndElicitationSemanticsMatchRust(t *testing.T) {
	exec := NewApprovalOverlay(ApprovalRequest{
		Kind:               ApprovalRequestExec,
		ThreadID:           "thread-1",
		ID:                 "exec-1",
		Command:            []string{"echo", "hi"},
		AvailableDecisions: []ApprovalCommandDecision{{Kind: ApprovalCommandAccept}, {Kind: ApprovalCommandCancel}},
	})
	exec.HandleKey("esc")
	if got := exec.Events(); len(got) != 1 || got[0].CommandDecision.Kind != ApprovalCommandCancel || !exec.IsComplete() {
		t.Fatalf("exec cancel events = %#v complete=%v", got, exec.IsComplete())
	}

	elicitation := NewApprovalOverlay(ApprovalRequest{
		Kind:       ApprovalRequestMcpElicitation,
		ThreadID:   "thread-1",
		ServerName: "test-server",
		RequestID:  "request-1",
		Message:    "Need more information",
	})
	elicitation.HandleKey("n")
	if got := elicitation.Events(); len(got) != 1 || got[0].McpElicitation != ApprovalMcpElicitationDecline {
		t.Fatalf("elicitation decline events = %#v", got)
	}
	elicitation = NewApprovalOverlay(ApprovalRequest{Kind: ApprovalRequestMcpElicitation, ThreadID: "thread-1", ServerName: "test-server", RequestID: "request-1"})
	elicitation.HandleKey("esc")
	if got := elicitation.Events(); len(got) != 1 || got[0].McpElicitation != ApprovalMcpElicitationCancel {
		t.Fatalf("elicitation cancel events = %#v", got)
	}
}

func TestApprovalOverlayOpenThreadAndDismissMatchRust(t *testing.T) {
	overlay := NewApprovalOverlay(ApprovalRequest{
		Kind:               ApprovalRequestExec,
		ThreadID:           "thread-worker",
		ThreadLabel:        "Robie [explorer]",
		ID:                 "exec-1",
		Command:            []string{"echo", "hi"},
		AvailableDecisions: []ApprovalCommandDecision{{Kind: ApprovalCommandAccept}, {Kind: ApprovalCommandCancel}},
	})
	overlay.HandleKey("o")
	if got := overlay.Events(); len(got) != 1 || got[0].Kind != ApprovalEventSelectThread || got[0].ThreadID != "thread-worker" {
		t.Fatalf("open-thread events = %#v", got)
	}
	if overlay.DismissResolvedRequest(ApprovalRequestExec, "other", "", "") {
		t.Fatal("non-matching resolved request should not dismiss")
	}
	if !overlay.DismissResolvedRequest(ApprovalRequestExec, "exec-1", "", "") || !overlay.IsComplete() {
		t.Fatalf("dismiss complete=%v", overlay.IsComplete())
	}
}

func approvalTestPermissions() *sandbox.RequestPermissionProfile {
	enabled := true
	return &sandbox.RequestPermissionProfile{
		Network: &sandbox.AdditionalNetworkPermissions{Enabled: &enabled},
		FileSystem: &sandbox.AdditionalFileSystemPermissions{
			Read:  []string{"/tmp/readme.txt"},
			Write: []string{"/tmp/out.txt"},
		},
	}
}

func approvalOptionLabels(options []ApprovalOption) []string {
	labels := make([]string, 0, len(options))
	for _, option := range options {
		labels = append(labels, option.Label)
	}
	return labels
}
