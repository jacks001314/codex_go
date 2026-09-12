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
		"permissions": {"danger_full_access": "x", "workspace_write": "y", "read_only": "z"},
		"persistent_instructions": "persist",
		"collaboration_modes": {"default": "cm", "plan": "pm"},
		"auto_review": {"policy": "policy"},
		"multi_agent": {"role": {"root": "r", "subagent": "s"}},
		"token_budget": {"reminder_threshold_tokens": 1, "reminder_message_template": "m"},
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
