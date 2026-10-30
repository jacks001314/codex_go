package agentboard

import (
	"reflect"
	"testing"

	"codex_go/agent"
	"codex_go/tool"
)

// TestMessageBoardDescriptionsFollowLiveStoreLikeRust mirrors Rust #47670: the
// model catalog may replace any channel tool's description independently, an
// empty string suppresses the bundled text, missing entries keep it, the fixed
// parameter schema never changes, and replacing the live store updates the
// executor used by the next sampling step.
func TestMessageBoardDescriptionsFollowLiveStoreLikeRust(t *testing.T) {
	store := NewMessageBoardToolOverrides(nil)
	executors := NewMessageBoardToolsWithOverrideStore(&InMemoryBoard{}, "caller", agent.AgentPathRoot, "delegation", "Shared discussion.", store)
	if len(executors) != len(MessageBoardToolNames) {
		t.Fatalf("built %d tools, want %d", len(executors), len(MessageBoardToolNames))
	}
	byName := map[string]tool.Executor{}
	bundled := map[string]string{}
	schemas := map[string]any{}
	for _, executor := range executors {
		spec := executor.Spec()
		byName[spec.Name.Name] = executor
		bundled[spec.Name.Name] = spec.Description
		schemas[spec.Name.Name] = spec.InputSchema
	}
	for _, name := range MessageBoardToolNames {
		if bundled[name] == "" {
			t.Fatalf("%s has no bundled description", name)
		}
	}

	catalogPost := "Catalog post."
	store.Replace(map[string]MessageBoardToolOverride{
		"post":      {Description: &catalogPost},
		"subscribe": {Description: messageBoardTestString("")},
	})
	for _, name := range MessageBoardToolNames {
		spec := byName[name].Spec()
		want := bundled[name]
		switch name {
		case "post":
			want = catalogPost
		case "subscribe":
			want = ""
		}
		if spec.Description != want {
			t.Fatalf("%s description = %q, want %q", name, spec.Description, want)
		}
		if spec.Name.Namespace != "delegation" {
			t.Fatalf("%s namespace = %q", name, spec.Name.Namespace)
		}
		if !reflect.DeepEqual(spec.InputSchema, schemas[name]) {
			t.Fatalf("%s schema changed with a description override: %#v", name, spec.InputSchema)
		}
	}

	// Every channel tool follows the live store, not a turn-start snapshot.
	store.Replace(nil)
	for _, name := range MessageBoardToolNames {
		if got := byName[name].Spec().Description; got != bundled[name] {
			t.Fatalf("%s description after reset = %q, want bundled %q", name, got, bundled[name])
		}
	}

	// A nil store is the no-override case and must stay usable.
	plain := NewMessageBoardToolsWithOverrideStore(&InMemoryBoard{}, "caller", agent.AgentPathRoot, "", "", nil)
	for _, executor := range plain {
		spec := executor.Spec()
		if spec.Description != messageBoardSpecFor(spec.Name.Name).description {
			t.Fatalf("%s plain description = %q", spec.Name.Name, spec.Description)
		}
	}
}

func messageBoardTestString(value string) *string {
	return &value
}
