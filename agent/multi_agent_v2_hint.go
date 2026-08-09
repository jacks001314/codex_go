package agent

import (
	"fmt"
	"strings"
)

// Multi-agent V2 usage hints. The text mirrors Rust's
// DEFAULT_MULTI_AGENT_V2_ROOT_AGENT_USAGE_HINT_TEXT /
// DEFAULT_MULTI_AGENT_V2_SUBAGENT_USAGE_HINT_TEXT and the shared/wait/model
// fragments composed by `default_multi_agent_v2_usage_hint_text`
// (codex-rs/core/src/config/mod.rs). Both the exec runner and the app-server
// world state render these so V2 sessions see the same collaboration contract.
const (
	MultiAgentV2RootUsageHint = "You are `/root`, the primary agent in a team of agents collaborating to fulfill the user's goals.\n\n" +
		"At the start of your turn, you are the active agent.\n" +
		"You can spawn sub-agents to handle subtasks, and those sub-agents can spawn their own sub-agents.\n" +
		"All agents in the team, including the agents that you can assign tasks to, are equally intelligent and capable, and have access to the same set of tools.\n\n" +
		"You can use `spawn_agent` to create a new agent, `followup_task` to give an existing agent a new task and trigger a turn, and `send_message` to pass a message to a running agent without triggering a turn.\n" +
		"Child agents can also spawn their own sub-agents.\n" +
		"You can decide how much context you want to propagate to your sub-agents with the `fork_turns` parameter.\n\n" +
		"You will receive messages in the analysis channel in the form:\n" +
		"```\nMessage Type: MESSAGE | FINAL_ANSWER\nTask name: <recipient>\nSender: <author>\nPayload:\n<payload text>\n```\n" +
		"They may be addressed as to=/root"

	MultiAgentV2SubagentUsageHint = "You are an agent in a team of agents collaborating to complete a task.\n\n" +
		"You can spawn sub-agents to handle subtasks, and those sub-agents can spawn their own sub-agents. All agents in the team, including the agents that you can assign tasks to, are equally intelligent and capable, and have access to the same set of tools.\n\n" +
		"You can use `spawn_agent` to create a new agent, `followup_task` to give an existing agent a new task and trigger a turn, and `send_message` to pass a message to a running agent.\n" +
		"Child agents can also spawn their own sub-agents.\n\n" +
		"When you provide a response in the final channel, that content is immediately delivered back to your parent agent.\n\n" +
		"You will receive messages in the analysis channel in the form:\n" +
		"```\nMessage Type: NEW_TASK | MESSAGE | FINAL_ANSWER\nTask name: <recipient>\nSender: <author>\nPayload:\n<payload text>\n```\n" +
		"You may also see them addressed as to=/root/..., which indicates your identity is /root/..."

	MultiAgentV2SharedUsageHint = "Note that collaboration tools cannot be called from inside `functions.exec`. Call `spawn_agent`, `send_message`, `followup_task`, `wait_agent`, `interrupt_agent`, and `list_agents` only as direct tool calls using the recipient shown in their tool definitions, such as `to=functions.collaboration.spawn_agent`, since they are intentionally absent from the `functions.exec` `tools.*` namespace. Available tools in `functions.exec` are explicitly described with a `tools` namespace in the developer message.\n\n" +
		"All agents share the same directory. In detail:\n" +
		"- All agents have access to the same container and filesystem as you.\n" +
		"- All agents use the same current working directory.\n" +
		"- As a result, edits made by one agent are immediately visible to all other agents."

	MultiAgentV2WaitAgentUsageHint = "When calling `wait_agent`, prefer longer waits (minutes) to avoid busy polling."

	MultiAgentV2ModelOverrideUsageHint = "Full-history forks (`fork_turns` omitted or `\"all\"`) inherit the parent model and reasoning effort and do not accept overrides. Only set `model` or `reasoning_effort` when explicitly requested by the user, applicable `AGENTS.md` instructions, or skill instructions; when doing so, set `fork_turns` to `\"none\"` or a positive integer string."
)

// MultiAgentV2UsageHintOptions controls the rendered multi-agent V2 usage hint.
type MultiAgentV2UsageHintOptions struct {
	IsSubagent                     bool
	MaxConcurrency                 int
	WaitAgentEnabled               bool
	ExposeSpawnAgentModelOverrides bool
	RootUsageHintText              *string
	SubagentUsageHintText          *string
}

// MultiAgentV2UsageHint composes the developer-instruction fragment shown to a
// V2 root or subagent. Configured root/subagent hint text replaces the built-in
// identity (Rust's resolve_optional_prompt_text); the shared, wait_agent, and
// concurrency fragments are appended to the defaults only.
func MultiAgentV2UsageHint(options MultiAgentV2UsageHintOptions) string {
	identity := MultiAgentV2RootUsageHint
	if options.IsSubagent {
		identity = MultiAgentV2SubagentUsageHint
		if options.SubagentUsageHintText != nil {
			identity = strings.TrimSpace(*options.SubagentUsageHintText)
		}
	} else if options.RootUsageHintText != nil {
		identity = strings.TrimSpace(*options.RootUsageHintText)
	}
	parts := []string{identity, MultiAgentV2SharedUsageHint}
	// Rust 92b83e226d (#37189): present wait_agent polling guidance in the
	// developer instructions only when the tool is enabled.
	if options.WaitAgentEnabled {
		parts = append(parts, MultiAgentV2WaitAgentUsageHint)
	}
	maxConcurrency := options.MaxConcurrency
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	parts = append(parts, fmt.Sprintf("There are %d available concurrency slots, meaning that up to %d agents can be active at once, including you.", maxConcurrency, maxConcurrency))
	if options.ExposeSpawnAgentModelOverrides {
		parts = append(parts, MultiAgentV2ModelOverrideUsageHint)
	}
	return strings.Join(nonEmptyStrings(parts), "\n\n")
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}
