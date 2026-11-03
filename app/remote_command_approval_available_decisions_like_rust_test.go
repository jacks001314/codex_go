package app

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/appserver"
	codextea "codex_go/tui/tea"
)

func TestRemoteCommandApprovalUsesAvailableDecisions(t *testing.T) {
	params := &appserver.CommandExecutionRequestApprovalParams{
		AvailableDecisions: []any{
			string(appserver.CommandExecutionApprovalAccept),
			map[string]any{
				string(appserver.CommandExecutionApprovalAcceptWithExecpolicyAmendment): map[string]any{
					"execpolicy_amendment": []string{"git", "status"},
				},
			},
			string(appserver.CommandExecutionApprovalCancel),
		},
	}
	options := remoteCommandExecutionApprovalOptions(params)
	if len(options) != 3 {
		t.Fatalf("options = %#v, want accept/prefix/cancel", options)
	}
	if options[0].ID != "allow_once" || options[1].ID != "allow_prefix" || options[2].ID != "cancel" {
		t.Fatalf("option ids = %#v", []string{options[0].ID, options[1].ID, options[2].ID})
	}
	if !strings.Contains(options[1].Label, "git status") {
		t.Fatalf("prefix label = %q", options[1].Label)
	}
	if options[2].DisplayShortcut != "esc" {
		t.Fatalf("cancel display shortcut = %q", options[2].DisplayShortcut)
	}
}

func TestRemoteCommandApprovalAvailableDecisionsDriveResponse(t *testing.T) {
	params := &appserver.CommandExecutionRequestApprovalParams{
		AvailableDecisions: []any{
			map[string]any{
				string(appserver.CommandExecutionApprovalAcceptWithExecpolicyAmendment): map[string]any{
					"execpolicy_amendment": []string{"go", "test"},
				},
			},
		},
	}
	decision := remoteCommandExecutionDecision(codextea.ModalResponse{OptionID: "allow_prefix"}, params)
	data, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	// Rust's ExecPolicyAmendment is #[serde(transparent)] over Vec<String>, so
	// the response carries the bare token list.
	const want = `{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["go","test"]}}`
	if string(data) != want {
		t.Fatalf("decision = %s, want %s", data, want)
	}
	if got := remoteCommandExecutionDecision(codextea.ModalResponse{OptionID: "cancel"}, params); got != appserver.CommandExecutionApprovalCancel {
		t.Fatalf("cancel decision = %#v", got)
	}
}
