package turn

import (
	"context"
	"testing"

	"codex_go/mcp"
	"codex_go/model"
	"codex_go/skillprovider"
	"codex_go/tool"
)

// requiredSkillsLoopAgent answers each sampling request with a completed
// response, recording how many model requests the turn actually issued.
type requiredSkillsLoopAgent struct {
	requests []model.AgentRequest
}

func (a *requiredSkillsLoopAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	return &model.AgentResponse{
		ResponseID: "resp-done",
		Message:    "done",
		Items:      []model.AgentItem{{Type: "agent_message", Text: "done"}},
	}, nil
}

// requiredSkillsToolLoopAgent issues one tool call before finishing, so a
// two-step turn exercises the per-step validation placement.
type requiredSkillsToolLoopAgent struct {
	requests []model.AgentRequest
}

func (a *requiredSkillsToolLoopAgent) Run(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	if len(a.requests) == 1 {
		return &model.AgentResponse{
			ResponseID: "resp-tool",
			Items:      []model.AgentItem{{ID: "call-1", Type: "function_call", Name: "echo", CallID: "call-1", Arguments: `{}`}},
		}, nil
	}
	return &model.AgentResponse{
		ResponseID: "resp-final",
		Message:    "done",
		Items:      []model.AgentItem{{ID: "msg-final", Type: "agent_message", Text: "done"}},
	}, nil
}

func executorRequirementProvider(entries ...skillprovider.CatalogEntry) *skillprovider.Registry {
	return skillprovider.NewRegistry(skillprovider.Source{
		Kind:  skillprovider.SourceExecutor,
		Label: "executor",
		Provider: skillprovider.ProviderFuncs{
			ListFunc: func(context.Context, skillprovider.ListQuery) (skillprovider.Catalog, error) {
				return skillprovider.Catalog{Entries: append([]skillprovider.CatalogEntry(nil), entries...)}, nil
			},
		},
	})
}

func executorRequirementEntry(name string, authorityID string, enabled bool) skillprovider.CatalogEntry {
	return skillprovider.CatalogEntry{
		PackageID: "skill://test/" + name,
		Authority: skillprovider.Authority{Kind: skillprovider.SourceExecutor, ID: authorityID},
		Name:      name,
		Enabled:   enabled,
	}
}

// TestRequiredSkillsCatalogResolvesAuthorityEnvironmentsLikeRust mirrors Rust
// #51157 reading `main_prompt.environment_path()`: Go scopes executor skills by
// capability root (Rust #46015), so the authority id is resolved to the
// environment that supplies the skill, and an entry with no environment
// provenance can never satisfy a requirement.
func TestRequiredSkillsCatalogResolvesAuthorityEnvironmentsLikeRust(t *testing.T) {
	catalog := RequiredSkillsCatalogFromEntries([]skillprovider.CatalogEntry{
		executorRequirementEntry("review", "root-1", true),
		executorRequirementEntry("lint", "root-2", true),
		{
			PackageID: "skill://host/host-only",
			Authority: skillprovider.Authority{Kind: skillprovider.SourceOrchestrator, ID: "required"},
			Name:      "host-only",
			Enabled:   true,
		},
	}, map[string]string{"root-1": "required"})

	if !catalog.Available {
		t.Fatalf("catalog.Available = false, want true for a configured provider")
	}
	if len(catalog.Entries) != 3 {
		t.Fatalf("entries = %#v, want three", catalog.Entries)
	}
	if catalog.Entries[0].EnvironmentID != "required" || catalog.Entries[0].Name != "review" || !catalog.Entries[0].Enabled {
		t.Fatalf("resolved entry = %#v, want review from `required`", catalog.Entries[0])
	}
	if catalog.Entries[1].EnvironmentID != "" {
		t.Fatalf("unmapped authority environment = %q, want empty", catalog.Entries[1].EnvironmentID)
	}
	if catalog.Entries[2].EnvironmentID != "" {
		t.Fatalf("non-executor authority environment = %q, want empty", catalog.Entries[2].EnvironmentID)
	}

	if empty := RequiredSkillsCatalogFromProviders(context.Background(), nil, "turn-1", nil); empty.Available {
		t.Fatalf("nil providers catalog = %#v, want an unavailable extension", empty)
	}
}

// TestAgentLoopGatesInferenceOnRequiredEnvironmentSkillsLikeRust mirrors Rust
// #51157's `required_environment_skill_gates_inference`: only an enabled skill
// from the requiring environment lets the turn reach the model; a missing,
// disabled, or wrongly attributed skill fails the turn before its model request
// with the Rust message, and dropping the environment recovers.
func TestAgentLoopGatesInferenceOnRequiredEnvironmentSkillsLikeRust(t *testing.T) {
	requirements := []mcp.EnvironmentSkillRequirements{{EnvironmentID: "required", SkillNames: []string{"review"}}}
	const wantMessage = `Fatal error: Required skill "review" from environment "required" is unavailable`
	authorityEnvironments := map[string]string{"root-required": "required", "root-primary": "primary"}

	for _, tc := range []struct {
		name    string
		entries []skillprovider.CatalogEntry
		ok      bool
	}{
		{
			name:    "available",
			entries: []skillprovider.CatalogEntry{executorRequirementEntry("review", "root-required", true)},
			ok:      true,
		},
		{name: "missing"},
		{
			name:    "disabled",
			entries: []skillprovider.CatalogEntry{executorRequirementEntry("review", "root-required", false)},
		},
		{
			name:    "other environment",
			entries: []skillprovider.CatalogEntry{executorRequirementEntry("review", "root-primary", true)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			providers := executorRequirementProvider(tc.entries...)
			agent := &requiredSkillsLoopAgent{}
			loop := NewAgentLoop(&AgentLoopOptions{Agent: agent, MaxTurns: 3})
			_, err := loop.Run(context.Background(), &AgentLoopRequest{
				Prompt:                "Review the workspace.",
				Model:                 "gpt-test",
				ThreadID:              "thread-1",
				TurnID:                "turn-1",
				PreSamplingValidation: NewRequiredSkillsValidation(providers, authorityEnvironments, "turn-1", requirements),
			})
			if tc.ok {
				if err != nil {
					t.Fatalf("Run() error = %v, want nil", err)
				}
				if len(agent.requests) != 1 {
					t.Fatalf("model requests = %d, want 1", len(agent.requests))
				}
				return
			}
			if err == nil || err.Error() != wantMessage {
				t.Fatalf("Run() error = %v, want %q", err, wantMessage)
			}
			if len(agent.requests) != 0 {
				t.Fatalf("model requests = %d, want 0 (the turn must fail before inference)", len(agent.requests))
			}

			// Recovery after deselection: the same turn without the required
			// environment carries no requirements, so inference proceeds.
			recovered := &requiredSkillsLoopAgent{}
			recoveredLoop := NewAgentLoop(&AgentLoopOptions{Agent: recovered, MaxTurns: 3})
			if _, err := recoveredLoop.Run(context.Background(), &AgentLoopRequest{
				Prompt:                "Continue without that environment.",
				Model:                 "gpt-test",
				ThreadID:              "thread-1",
				TurnID:                "turn-2",
				PreSamplingValidation: NewRequiredSkillsValidation(providers, authorityEnvironments, "turn-2", nil),
			}); err != nil {
				t.Fatalf("recovered Run() error = %v", err)
			}
			if len(recovered.requests) != 1 {
				t.Fatalf("recovered model requests = %d, want 1", len(recovered.requests))
			}
		})
	}
}

// TestAgentLoopReportsUnavailableSkillsExtensionLikeRust mirrors Rust #51157
// failing when the skills extension state is missing: the turn fails before
// inference with the extension message instead of a missing-skill message.
func TestAgentLoopReportsUnavailableSkillsExtensionLikeRust(t *testing.T) {
	agent := &requiredSkillsLoopAgent{}
	loop := NewAgentLoop(&AgentLoopOptions{Agent: agent, MaxTurns: 3})
	_, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:   "Review the workspace.",
		Model:    "gpt-test",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		PreSamplingValidation: NewRequiredSkillsValidation(nil, nil, "turn-1", []mcp.EnvironmentSkillRequirements{
			{EnvironmentID: "required", SkillNames: []string{"review"}},
		}),
	})
	if err == nil || err.Error() != "required skills cannot be validated because the skills extension is unavailable" {
		t.Fatalf("Run() error = %v, want the unavailable-extension message", err)
	}
	if len(agent.requests) != 0 {
		t.Fatalf("model requests = %d, want 0", len(agent.requests))
	}
}

// TestAgentLoopValidatesBeforeEverySamplingRequestLikeRust pins Rust's
// placement: the check runs before each step's model request, so a later step
// of the same turn is gated too.
func TestAgentLoopValidatesBeforeEverySamplingRequestLikeRust(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "tool result"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	agent := &requiredSkillsToolLoopAgent{}
	loop := NewAgentLoop(&AgentLoopOptions{
		Agent:      agent,
		Dispatcher: NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)}),
		MaxTurns:   3,
	})
	var iterations []int
	_, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:   "run echo",
		Model:    "gpt-test",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		PreSamplingValidation: func(_ context.Context, iteration int) error {
			iterations = append(iterations, iteration)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(iterations) != 2 || iterations[0] != 0 || iterations[1] != 1 {
		t.Fatalf("validation iterations = %#v, want [0 1]", iterations)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(agent.requests))
	}
}
