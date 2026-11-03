package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/sandbox"
)

func TestCommandExecutionAvailableDecisionsMatchRustDefaults(t *testing.T) {
	t.Run("command with prefix", func(t *testing.T) {
		params := &CommandExecutionRequestApprovalParams{
			ProposedExecPolicyAmendment: []string{"git", "status"},
		}
		decisions := commandExecutionAvailableDecisions(params)
		if len(decisions) != 3 || decisions[0] != string(CommandExecutionApprovalAccept) ||
			decisions[2] != string(CommandExecutionApprovalCancel) {
			t.Fatalf("decisions = %#v", decisions)
		}
		encoded, err := json.Marshal(decisions[1])
		if err != nil {
			t.Fatal(err)
		}
		// ExecPolicyAmendment is #[serde(transparent)] over Vec<String> in Rust,
		// so availableDecisions carries the bare token list.
		if got, want := string(encoded), `{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git","status"]}}`; got != want {
			t.Fatalf("prefix decision = %s, want %s", got, want)
		}
	})

	t.Run("network", func(t *testing.T) {
		params := &CommandExecutionRequestApprovalParams{
			NetworkApprovalContext: &NetworkApprovalContext{Host: "example.com"},
			ProposedNetworkPolicyAmendments: []map[string]any{
				{"host": "example.com", "action": string(NetworkPolicyRuleAllow)},
			},
		}
		decisions := commandExecutionAvailableDecisions(params)
		if len(decisions) != 4 || decisions[1] != string(CommandExecutionApprovalAcceptForSession) ||
			decisions[3] != string(CommandExecutionApprovalCancel) {
			t.Fatalf("decisions = %#v", decisions)
		}
	})

	t.Run("additional permissions", func(t *testing.T) {
		params := &CommandExecutionRequestApprovalParams{AdditionalPermissions: &sandbox.AdditionalPermissionProfile{}}
		decisions := commandExecutionAvailableDecisions(params)
		if len(decisions) != 2 || decisions[0] != string(CommandExecutionApprovalAccept) ||
			decisions[1] != string(CommandExecutionApprovalCancel) {
			t.Fatalf("decisions = %#v", decisions)
		}
	})
}

func TestCommandExecutionRequestApprovalParamsMarshalsAvailableDecisions(t *testing.T) {
	params := &CommandExecutionRequestApprovalParams{
		AvailableDecisions: []any{
			string(CommandExecutionApprovalAccept),
			string(CommandExecutionApprovalCancel),
		},
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	decisions, ok := decoded["availableDecisions"].([]any)
	if !ok || len(decisions) != 2 {
		t.Fatalf("availableDecisions = %#v", decoded["availableDecisions"])
	}
}
