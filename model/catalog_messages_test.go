package model

import (
	"encoding/json"
	"testing"
)

// TestModelMessagesParseRustWireNames pins the catalog wire names of
// ModelMessages against Rust protocol::openai_models::ModelMessages. The
// catalog ships `approvals` (not `approval`) and `persistent_instructions`, so a
// model's replacement approval texts and persistent-mode guidance only take
// effect when those exact keys parse.
func TestModelMessagesParseRustWireNames(t *testing.T) {
	raw := []byte(`{
		"instructions_template": "template",
		"instructions_variables": {"personality_default": "d", "personality_friendly": "f", "personality_pragmatic": "p"},
		"approvals": {
			"on_request": "a",
			"on_request_auto_review": "b",
			"never": "c",
			"unless_trusted": "d"
		},
		"permissions": {"danger_full_access": "x", "workspace_write": "y", "read_only": "z", "approved_prefixes": "{{ approved_prefixes }}"},
		"persistent_instructions": "persist",
		"collaboration_modes": {"default": "cm", "plan": "pm"},
		"auto_review": {"policy": "policy"},
		"multi_agent": {"role": {"root": "r", "subagent": "s"}},
		"token_budget": {"enabled": true, "use_history_notes_extension": true, "reminder_threshold_tokens": 1, "reminder_message_template": "m"},
		"confirmation_policies": {"browser_use": "b"},
		"tools": {"send_user_message_async": {"description": "d"}}
	}`)
	var messages ModelMessages
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if messages.Approvals == nil {
		t.Fatal("approvals did not parse")
	}
	for name, value := range map[string]*string{
		"on_request":             messages.Approvals.OnRequest,
		"on_request_auto_review": messages.Approvals.OnRequestAutoReview,
		"never":                  messages.Approvals.Never,
		"unless_trusted":         messages.Approvals.UnlessTrusted,
	} {
		if value == nil {
			t.Fatalf("approvals.%s did not parse", name)
		}
	}
	if messages.PersistentInstructions == nil || *messages.PersistentInstructions != "persist" {
		t.Fatalf("persistent_instructions = %#v", messages.PersistentInstructions)
	}
	if messages.Permissions == nil || messages.Permissions.ApprovedPrefixes == nil || *messages.Permissions.ApprovedPrefixes != "{{ approved_prefixes }}" {
		t.Fatalf("permissions.approved_prefixes = %#v", messages.Permissions)
	}
	if messages.TokenBudget == nil || !messages.TokenBudget.Enabled || !messages.TokenBudget.UseHistoryNotesExtension {
		t.Fatalf("token_budget activation = %#v", messages.TokenBudget)
	}
	// The legacy singular key must not silently populate the field: Rust only
	// reads `approvals`.
	var legacy ModelMessages
	if err := json.Unmarshal([]byte(`{"approval":{"on_request":"a"}}`), &legacy); err != nil {
		t.Fatalf("legacy Unmarshal() error = %v", err)
	}
	if legacy.Approvals != nil {
		t.Fatalf("singular `approval` populated Approvals: %#v", legacy.Approvals)
	}
}

// TestBundledCatalogCarriesModelMessagesLikeRust checks the Rust catalog's
// `approvals` / `persistent_instructions` reach Go's parsed models (the bundled
// catalog is present in a development checkout; skipped otherwise).
func TestBundledCatalogCarriesModelMessagesLikeRust(t *testing.T) {
	catalog, err := loadBundledModelsResponse()
	if err != nil {
		t.Skipf("bundled Rust catalog unavailable: %v", err)
	}
	withApprovals := 0
	withPersistent := 0
	for i := range catalog.Models {
		messages := catalog.Models[i].ModelMessages
		if messages == nil {
			continue
		}
		if messages.Approvals != nil {
			withApprovals++
		}
		if messages.PersistentInstructions != nil {
			withPersistent++
		}
	}
	if withApprovals == 0 {
		t.Fatal("no bundled model parsed its catalog approvals")
	}
	if withPersistent == 0 {
		t.Fatal("no bundled model parsed its catalog persistent_instructions")
	}
}

// TestModelMessagesParsesGuardianV2LikeRust pins the catalog's
// `model_messages.guardian_v2` shape (Rust GuardianV2ModelConfig); Go consumes
// max_tool_call_lag as the stale-score bound's model default.
func TestModelMessagesParsesGuardianV2LikeRust(t *testing.T) {
	var messages ModelMessages
	if err := json.Unmarshal([]byte(`{
		"guardian_v2": {
			"classifier_instructions": "classify",
			"review_threshold_basis_points": 5000,
			"max_tool_call_lag": 4,
			"reasoning_effort": "low",
			"max_action_tokens": 10000,
			"transcript": {"sources": ["tool_calls"]}
		}
	}`), &messages); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if messages.GuardianV2 == nil {
		t.Fatal("guardian_v2 did not parse")
	}
	if messages.GuardianV2.MaxToolCallLag == nil || *messages.GuardianV2.MaxToolCallLag != 4 {
		t.Fatalf("max_tool_call_lag = %#v", messages.GuardianV2.MaxToolCallLag)
	}
	if messages.GuardianV2.ClassifierInstructions == nil || *messages.GuardianV2.ClassifierInstructions != "classify" {
		t.Fatalf("classifier_instructions = %#v", messages.GuardianV2.ClassifierInstructions)
	}
	if len(messages.GuardianV2.Transcript) == 0 {
		t.Fatal("transcript was not retained")
	}
}

// TestGranularApprovalMessagesParseRustWireNames mirrors Rust #57325: the
// catalog's optional approvals.granular object preserves sparse template
// strings verbatim, treats null as absent, and distinguishes an empty object
// from a null value.
func TestGranularApprovalMessagesParseRustWireNames(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "null", raw: `null`, want: ``},
		{name: "empty object", raw: `{}`, want: `{}`},
		{name: "null fields", raw: `{"intro":null,"prompted_categories":null,"rejected_categories":null}`, want: `{}`},
		{name: "empty template", raw: `{"intro":""}`, want: `{"intro":""}`},
		{
			name: "populated",
			raw:  `{"intro":"Catalog: {{ policy }}","prompted_categories":"Allowed: {{ categories }}","rejected_categories":"Denied: {{ categories }}"}`,
			want: `{"intro":"Catalog: {{ policy }}","prompted_categories":"Allowed: {{ categories }}","rejected_categories":"Denied: {{ categories }}"}`,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var messages ModelMessages
			if err := json.Unmarshal([]byte(`{"approvals":{"on_request":"existing override","granular":`+test.raw+`}}`), &messages); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if messages.Approvals == nil || messages.Approvals.OnRequest == nil || *messages.Approvals.OnRequest != "existing override" {
				t.Fatalf("approvals.on_request = %#v", messages.Approvals)
			}
			encoded, err := json.Marshal(messages.Approvals)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatalf("round-trip Unmarshal() error = %v", err)
			}
			granular, present := got["granular"]
			if test.want == "" {
				if present {
					t.Fatalf("granular = %s, want absent", granular)
				}
				return
			}
			if !present || string(granular) != test.want {
				t.Fatalf("granular = %s, want %s", granular, test.want)
			}
		})
	}
}

// TestPermissionMessagesParsePathWordingLikeRust pins the Rust #56309/#57263
// catalog wire keys for writable-root and permission-path presentation.
func TestPermissionMessagesParsePathWordingLikeRust(t *testing.T) {
	var empty PermissionMessages
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil {
		t.Fatalf("empty Unmarshal() error = %v", err)
	}
	if empty.WritableRoot != nil || empty.WritableRoots != nil || empty.OmittedPaths != nil || empty.DeniedReads != nil {
		t.Fatalf("empty permission messages = %#v", empty)
	}
	encoded, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("empty Marshal() error = %v", err)
	}
	if string(encoded) != "{}" {
		t.Fatalf("sparse permission messages = %s, want {}", encoded)
	}

	raw := []byte(`{
		"writable_root": " Root: {{ roots }}.",
		"writable_roots": "Roots: {{ roots }}.",
		"omitted_paths": "Some paths are omitted.",
		"denied_reads": "Blocked reads:\n{{ denied_reads }}"
	}`)
	var messages PermissionMessages
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	for name, value := range map[string]*string{
		"writable_root":  messages.WritableRoot,
		"writable_roots": messages.WritableRoots,
		"omitted_paths":  messages.OmittedPaths,
		"denied_reads":   messages.DeniedReads,
	} {
		if value == nil {
			t.Fatalf("permissions.%s did not parse", name)
		}
	}
	if *messages.DeniedReads != "Blocked reads:\n{{ denied_reads }}" {
		t.Fatalf("denied_reads = %q", *messages.DeniedReads)
	}

	// Explicit nulls behave like omitted fields and never render `null`.
	var sparse PermissionMessages
	if err := json.Unmarshal([]byte(`{"writable_root":null,"writable_roots":null,"omitted_paths":null,"denied_reads":null}`), &sparse); err != nil {
		t.Fatalf("sparse Unmarshal() error = %v", err)
	}
	if sparse.WritableRoot != nil || sparse.WritableRoots != nil || sparse.OmittedPaths != nil || sparse.DeniedReads != nil {
		t.Fatalf("null permission messages = %#v", sparse)
	}
	encoded, err = json.Marshal(sparse)
	if err != nil {
		t.Fatalf("sparse Marshal() error = %v", err)
	}
	if string(encoded) != "{}" {
		t.Fatalf("null permission messages = %s, want {}", encoded)
	}
}
