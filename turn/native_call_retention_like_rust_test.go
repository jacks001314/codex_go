package turn

import (
	"encoding/json"
	"testing"

	"codex_go/model"
)

// Mirrors Rust #58040: the dispatcher supplies the original wire item so a
// native-review tool can retain replay ciphertext and encrypted argument
// metadata that the dispatch view alone does not carry.
func TestResponseItemFromAgentItemRetainsWireItemLikeRust(t *testing.T) {
	encryptedArgs := []string{"message"}
	item := &model.AgentItem{
		ID:                    "fc_action",
		Type:                  "function_call",
		Name:                  "send_message",
		Namespace:             "collaboration",
		CallID:                "action",
		Arguments:             `{"message":"opaque"}`,
		EncryptedFunctionArgs: &encryptedArgs,
		Data:                  map[string]any{"encrypted_content": "opaque-replay"},
	}
	responseItem, ok := responseItemFromAgentItem(item)
	if !ok {
		t.Fatal("responseItemFromAgentItem() rejected a function call")
	}
	if len(responseItem.Raw) == 0 {
		t.Fatal("response item did not retain the original wire item")
	}
	var wire map[string]any
	if err := json.Unmarshal(responseItem.Raw, &wire); err != nil {
		t.Fatalf("decode raw item: %v", err)
	}
	if wire["type"] != "function_call" || wire["call_id"] != "action" || wire["name"] != "send_message" {
		t.Fatalf("raw item = %s", responseItem.Raw)
	}
	if wire["encrypted_content"] != "opaque-replay" {
		t.Fatalf("raw item lost replay ciphertext: %s", responseItem.Raw)
	}
	if list, ok := wire["encrypted_function_args"].([]any); !ok || len(list) != 1 || list[0] != "message" {
		t.Fatalf("raw item lost encrypted argument metadata: %s", responseItem.Raw)
	}
}
