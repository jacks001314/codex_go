package tool

// IsStateChangingMultiAgentTool classifies state-changing MAv2 calls in the
// configured namespace (Rust GuardianScope::for_multi_agent_tool, #57455). An
// absent configured namespace accepts the default namespace spellings; callers
// gate the lookup on the relevant feature.
func IsStateChangingMultiAgentTool(name ToolName, configuredNamespace *string) bool {
	matchesNamespace := false
	if configuredNamespace != nil {
		matchesNamespace = name.Namespace == *configuredNamespace
	} else {
		matchesNamespace = name.Namespace == "" || name.Namespace == DefaultFunctionNamespace
	}
	if !matchesNamespace {
		return false
	}
	switch name.Name {
	case "spawn_agent", "send_message", "followup_task", "interrupt_agent":
		return true
	default:
		return false
	}
}
