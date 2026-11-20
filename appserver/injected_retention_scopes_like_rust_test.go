package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/compact"
	"codex_go/session"
)

// retentionPolicyBool returns the address of a policy value for table cases.
func retentionPolicyBool(value bool) *bool {
	return &value
}

// injectedMessage renders one wrapped `{item, retention}` injection of a
// developer message, the shape `thread/injectItems` accepts.
func injectedMessage(t *testing.T, text string, retention string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"item": map[string]any{
			"type":    "message",
			"role":    "developer",
			"content": []any{map[string]any{"type": "input_text", "text": text}},
		},
		"retention": retention,
	})
	if err != nil {
		t.Fatalf("Marshal(injected item) error = %v", err)
	}
	return raw
}

func injectedHarnessMetadata(t *testing.T, item session.Item) map[string]any {
	t.Helper()
	raw, ok := item.Data["harness_metadata"].(json.RawMessage)
	if !ok {
		t.Fatalf("injected item has no harness metadata: %#v", item.Data)
	}
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("decode harness metadata: %v", err)
	}
	return metadata
}

// Rust #62654 `From<InjectedResponseItem> for ResponseItemEnvelope`: each policy
// records the context-window decision, and only the two new policies record the
// child decision, so a rollout written before #62654 keeps `always` meaning both
// scopes.
func TestInjectedRetentionPoliciesRecordBothScopesLikeRust(t *testing.T) {
	cases := []struct {
		retention      string
		wantAcross     bool
		wantInChildren *bool
	}{
		{retention: "default", wantAcross: false},
		{retention: "always", wantAcross: true},
		{retention: "alwaysInThread", wantAcross: true, wantInChildren: retentionPolicyBool(false)},
		{retention: "alwaysInChildren", wantAcross: false, wantInChildren: retentionPolicyBool(true)},
	}
	for _, testCase := range cases {
		t.Run(testCase.retention, func(t *testing.T) {
			parsed, err := parseInjectedResponseItems([]json.RawMessage{injectedMessage(t, "policy", testCase.retention)})
			if err != nil {
				t.Fatalf("parseInjectedResponseItems() error = %v", err)
			}
			if err := validateInjectedResponseItems(parsed); err != nil {
				t.Fatalf("validateInjectedResponseItems() error = %v", err)
			}
			items, err := injectedSessionItems(parsed, time.Now().UTC(), true)
			if err != nil {
				t.Fatalf("injectedSessionItems() error = %v", err)
			}
			if len(items) != 1 {
				t.Fatalf("injected items = %d, want 1", len(items))
			}
			metadata := injectedHarnessMetadata(t, items[0])
			if across, ok := metadata[compact.RetainAcrossWindowsMetadataKey].(bool); !ok || across != testCase.wantAcross {
				t.Fatalf("retain_across_windows = %v, want %v", metadata[compact.RetainAcrossWindowsMetadataKey], testCase.wantAcross)
			}
			inherit, stated := metadata[compact.RetainInChildrenMetadataKey].(bool)
			if testCase.wantInChildren == nil {
				if _, present := metadata[compact.RetainInChildrenMetadataKey]; present {
					t.Fatalf("retain_in_children = %v, want omitted", inherit)
				}
				return
			}
			if !stated || inherit != *testCase.wantInChildren {
				t.Fatalf("retain_in_children = %v, want %v", metadata[compact.RetainInChildrenMetadataKey], *testCase.wantInChildren)
			}
		})
	}
}

// Rust #62654: an unknown retention variant is rejected with the four accepted
// policies named, so a client typo cannot silently fall back to ordinary
// retention.
func TestInjectedRetentionPolicyRejectsUnknownVariantLikeRust(t *testing.T) {
	_, err := parseInjectedResponseItems([]json.RawMessage{injectedMessage(t, "policy", "alwaysEverywhere")})
	if err == nil {
		t.Fatal("parseInjectedResponseItems() accepted an unknown retention policy")
	}
	if !strings.Contains(err.Error(), "expected `default`, `always`, `alwaysInThread` or `alwaysInChildren`") {
		t.Fatalf("retention error = %v, want the accepted policies named", err)
	}
}

// Rust #62654 `validate_injected_response_item`: a retained tool exchange must be
// adjacent with the same policy on both items, so one policy decides the whole
// exchange.
func TestInjectedRetainedToolExchangeRequiresSamePolicyLikeRust(t *testing.T) {
	call := func(retention string) json.RawMessage {
		raw, err := json.Marshal(map[string]any{
			"item":      map[string]any{"type": "function_call", "call_id": "call-1", "name": "shell", "arguments": "{}"},
			"retention": retention,
		})
		if err != nil {
			t.Fatalf("Marshal(call) error = %v", err)
		}
		return raw
	}
	output := func(retention string) json.RawMessage {
		raw, err := json.Marshal(map[string]any{
			"item":      map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "done"},
			"retention": retention,
		})
		if err != nil {
			t.Fatalf("Marshal(output) error = %v", err)
		}
		return raw
	}
	validate := func(retention string) error {
		parsed, err := parseInjectedResponseItems([]json.RawMessage{call(retention), output(retention)})
		if err != nil {
			return err
		}
		return validateRetainedToolAdjacency(parsed)
	}
	for _, retention := range []string{"always", "alwaysInThread", "alwaysInChildren"} {
		if err := validate(retention); err != nil {
			t.Fatalf("matching %q exchange error = %v", retention, err)
		}
	}
	if err := validate("default"); err != nil {
		t.Fatalf("default exchange error = %v", err)
	}

	mixed, err := parseInjectedResponseItems([]json.RawMessage{call("always"), output("alwaysInThread")})
	if err != nil {
		t.Fatalf("parseInjectedResponseItems() error = %v", err)
	}
	err = validateRetainedToolAdjacency(mixed)
	if err == nil {
		t.Fatal("validateRetainedToolAdjacency() accepted an exchange with two policies")
	}
	if !strings.Contains(err.Error(), "the same retention policy") {
		t.Fatalf("mixed exchange error = %v, want the same-policy requirement", err)
	}

	unpaired, err := parseInjectedResponseItems([]json.RawMessage{call("alwaysInChildren")})
	if err != nil {
		t.Fatalf("parseInjectedResponseItems() error = %v", err)
	}
	if err := validateRetainedToolAdjacency(unpaired); err == nil {
		t.Fatal("validateRetainedToolAdjacency() accepted a retained call without its output")
	}
}
