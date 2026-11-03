package appserver

import "strings"

// commandExecutionAvailableDecisions mirrors Rust's
// ExecApprovalRequestEvent::effective_available_decisions. The app-server
// sends the ordered list so every client renders the same approval choices;
// clients must not infer additional reusable decisions on their own.
func commandExecutionAvailableDecisions(params *CommandExecutionRequestApprovalParams) []any {
	if params == nil {
		return nil
	}
	if len(params.AvailableDecisions) > 0 {
		return params.AvailableDecisions
	}

	decisions := []any{string(CommandExecutionApprovalAccept)}
	if params.NetworkApprovalContext != nil {
		decisions = append(decisions, string(CommandExecutionApprovalAcceptForSession))
		if amendment := networkPolicyAllowAmendment(params.ProposedNetworkPolicyAmendments); amendment != nil {
			decisions = append(decisions, map[string]any{
				string(CommandExecutionApprovalApplyNetworkPolicyAmendment): map[string]any{
					"network_policy_amendment": amendment,
				},
			})
		}
		return append(decisions, string(CommandExecutionApprovalCancel))
	}
	if params.AdditionalPermissions != nil {
		return append(decisions, string(CommandExecutionApprovalCancel))
	}
	prefix := stringSliceFromAny(params.ProposedExecPolicyAmendment)
	if len(prefix) > 0 && !strings.ContainsAny(strings.Join(prefix, " "), "\r\n") {
		decisions = append(decisions, map[string]any{
			string(CommandExecutionApprovalAcceptWithExecpolicyAmendment): map[string]any{
				// Rust's ExecPolicyAmendment is #[serde(transparent)] over
				// Vec<String>: the wire value is the bare token list.
				"execpolicy_amendment": append([]string(nil), prefix...),
			},
		})
	}
	return append(decisions, string(CommandExecutionApprovalCancel))
}

func networkPolicyAllowAmendment(amendments []map[string]any) map[string]any {
	for _, amendment := range amendments {
		if strings.TrimSpace(stringFromAny(amendment["action"])) != string(NetworkPolicyRuleAllow) {
			continue
		}
		cloned := make(map[string]any, len(amendment))
		for key, value := range amendment {
			cloned[key] = value
		}
		return cloned
	}
	return nil
}
