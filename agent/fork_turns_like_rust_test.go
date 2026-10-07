package agent

import (
	"strings"
	"testing"

	"codex_go/tool"
)

func forkTurnsPtr(value string) *string {
	return &value
}

// Rust #51329 (core/src/tools/handlers/multi_agents_v2/spawn.rs::parse_fork_turns):
// `none` starts the child without parent history, `all` inherits it, and a legacy
// positive integer string is accepted as a full-history fork. Zero (`NonZeroUsize`
// rejects it) and non-numeric values are rejected.
func TestValidateForkTurnsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		value   *string
		wantErr bool
	}{
		{name: "omitted defaults to all"},
		{name: "all", value: forkTurnsPtr("all")},
		{name: "all is case-insensitive", value: forkTurnsPtr("ALL")},
		{name: "none", value: forkTurnsPtr("none")},
		{name: "legacy one", value: forkTurnsPtr("1")},
		{name: "legacy three", value: forkTurnsPtr("3")},
		{name: "legacy leading zero", value: forkTurnsPtr("01")},
		{name: "zero", value: forkTurnsPtr("0"), wantErr: true},
		{name: "leading-zero zero", value: forkTurnsPtr("00"), wantErr: true},
		{name: "empty", value: forkTurnsPtr(""), wantErr: true},
		{name: "banana", value: forkTurnsPtr("banana"), wantErr: true},
		{name: "negative", value: forkTurnsPtr("-1"), wantErr: true},
		{name: "fractional", value: forkTurnsPtr("1.5"), wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateForkTurns(testCase.value)
			if testCase.wantErr {
				if err == nil || err.Error() != "fork_turns must be `none` or `all`" {
					t.Fatalf("validateForkTurns(%v) error = %v, want the `none`/`all` rejection", testCase.value, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateForkTurns(%v) error = %v", testCase.value, err)
			}
		})
	}
}

// TestMultiAgentV2ForkTurnsGuidanceLikeRust pins the model-facing guidance Rust
// #51329 rewrote: the spec only describes `all`/`none`, the agent type applies
// regardless of the inherited history, and the model-override hint points at
// `fork_turns: "none"` instead of a positive integer.
func TestMultiAgentV2ForkTurnsGuidanceLikeRust(t *testing.T) {
	registry := registerV2WithOverrides(t, nil)
	spawn, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, "spawn_agent"))
	if !ok {
		t.Fatal("spawn_agent was not registered")
	}
	properties, _ := spawn.Spec().InputSchema["properties"].(map[string]any)
	forkTurns, _ := properties["fork_turns"].(map[string]any)
	wantForkTurns := "Parent history to inherit. Defaults to `all`; use `none` to start without parent history. Only `all` and `none` are supported."
	if got, _ := forkTurns["description"].(string); got != wantForkTurns {
		t.Fatalf("fork_turns description = %q, want %q", got, wantForkTurns)
	}
	agentType, _ := properties["agent_type"].(map[string]any)
	gotAgentType, _ := agentType["description"].(string)
	if !strings.HasSuffix(gotAgentType, "The selected role applies regardless of how much parent history is inherited.") {
		t.Fatalf("agent_type description = %q, want the inherited-history guidance", gotAgentType)
	}
	if strings.Contains(gotAgentType, "positive integer") {
		t.Fatalf("agent_type description = %q, want the legacy fork_turns guidance gone", gotAgentType)
	}
	wantHint := `Full-history forks (` + "`fork_turns`" + ` omitted or ` + "`\"all\"`" + `) inherit the parent model and reasoning effort and do not accept overrides. Only set ` + "`model`" + ` or ` + "`reasoning_effort`" + ` when explicitly requested by the user, applicable ` + "`AGENTS.md`" + ` instructions, or skill instructions; when doing so, set ` + "`fork_turns`" + ` to ` + "`\"none\"`" + `.`
	if MultiAgentV2ModelOverrideUsageHint != wantHint {
		t.Fatalf("MultiAgentV2ModelOverrideUsageHint = %q, want %q", MultiAgentV2ModelOverrideUsageHint, wantHint)
	}
}
