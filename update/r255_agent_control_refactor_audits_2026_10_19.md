# Agent-control refactor audits

Rust upstream refactors:

- `#45670` Move V2 agent message delivery into `AgentControl`
- `#46547` Expose a backend-independent agent control contract
- `#46559` Encapsulate rollout budget accounting in `LocalAgentControl`

## Verdict

All three are not applicable as code-organization changes. Go already routes V2
messages through `appserver/agent_controller.go` and the agent package, exposes
agent control at the agent/appserver boundary, and accounts rollout/goal budgets
through its state and turn accounting rather than a Rust `LocalAgentControl`
budget object. There are no equivalent Rust module boundaries to move.
