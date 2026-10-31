package config

import "testing"

func TestValidateModelCatalogJSONReportsSerdeDiagnostics(t *testing.T) {
	base := `"slug":"m","display_name":"M","supported_reasoning_levels":[],"shell_type":"shell_command","visibility":"list","supported_in_api":true,"priority":1,"support_verbosity":true,"truncation_policy":{"mode":"tokens","limit":5},"experimental_supported_tools":[]`
	cases := []struct {
		name   string
		doc    string
		want   string
		models int
	}{
		{name: "not json", doc: "not json", want: "expected ident at line 1 column 2"},
		{name: "open object", doc: "{", want: "EOF while parsing an object at line 1 column 1"},
		{name: "missing value", doc: `{"models":`, want: "EOF while parsing a value at line 1 column 10"},
		{name: "open list", doc: `{"models": [`, want: "EOF while parsing a list at line 1 column 12"},
		{name: "trailing characters", doc: `{"models": []} x`, want: "trailing characters at line 1 column 16"},
		{name: "open string", doc: `{"models": ["abc]}`, want: "EOF while parsing a string at line 1 column 18"},
		{name: "invalid escape", doc: `{"models": ["\q"]}`, want: "invalid escape at line 1 column 15"},
		{name: "control character", doc: "{\"models\": [\"a\tb\"]}", want: `control character (\u0000-\u001F) found while parsing a string at line 1 column 15`},
		{name: "expected value", doc: `{"models": [}`, want: "expected value at line 1 column 13"},
		{name: "expected colon", doc: `{"models" 5}`, want: "expected `:` at line 1 column 11"},
		{name: "key must be string", doc: `{models: []}`, want: "key must be a string at line 1 column 2"},
		{name: "trailing comma", doc: `{"models": [{"slug":"a",}]}`, want: "trailing comma at line 1 column 25"},
		{name: "leading zero", doc: `{"models": [01]}`, want: "invalid number at line 1 column 14"},
		{name: "bad exponent", doc: `{"models": [{"priority":1E.5}]}`, want: "invalid number at line 1 column 27"},
		{name: "empty document", doc: "", want: "EOF while parsing a value at line 1 column 0"},
		{name: "root list", doc: `[]`, want: "invalid length 0, expected struct ModelsResponse with 1 element at line 1 column 2"},
		{name: "root string", doc: `"x"`, want: `invalid type: string "x", expected struct ModelsResponse at line 1 column 3`},
		{name: "root integer", doc: `5`, want: "invalid type: integer `5`, expected struct ModelsResponse at line 1 column 1"},
		{name: "root bool", doc: `true`, want: "invalid type: boolean `true`, expected struct ModelsResponse at line 1 column 4"},
		{name: "root null", doc: `null`, want: "invalid type: null, expected struct ModelsResponse at line 1 column 4"},
		{name: "missing models", doc: `{}`, want: "missing field `models` at line 1 column 2"},
		{name: "duplicate models", doc: `{"models": [], "models": []}`, want: "duplicate field `models` at line 1 column 23"},
		{name: "models null", doc: `{"models": null}`, want: "invalid type: null, expected a sequence at line 1 column 15"},
		{name: "models integer", doc: `{"models": 5}`, want: "invalid type: integer `5`, expected a sequence at line 1 column 12"},
		{name: "models map", doc: `{"models": {"a":1}}`, want: "invalid type: map, expected a sequence at line 1 column 11"},
		{name: "model integer", doc: `{"models": [5]}`, want: "invalid type: integer `5`, expected struct ModelInfoWithLegacyBaseInstructions at line 1 column 13"},
		{name: "model float", doc: `{"models": [1.5]}`, want: "invalid type: number, expected struct ModelInfoWithLegacyBaseInstructions at line 1 column 15"},
		{name: "model list", doc: `{"models": [[1]]}`, want: "invalid type: sequence, expected struct ModelInfoWithLegacyBaseInstructions at line 1 column 12"},
		{name: "missing slug", doc: `{"models": [{}]}`, want: "missing field `slug` at line 1 column 14"},
		{name: "missing display name", doc: `{"models": [{"slug": "m"}]}`, want: "missing field `display_name` at line 1 column 25"},
		{name: "missing slug multiline", doc: "{\"models\": [\n  {}\n]}", want: "missing field `slug` at line 2 column 4"},
		{name: "slug type", doc: `{"models": [{"slug": 5}]}`, want: "invalid type: integer `5`, expected a string at line 1 column 23"},
		{name: "slug null", doc: `{"models": [{"slug": null}]}`, want: "invalid type: null, expected a string at line 1 column 26"},
		{name: "duplicate slug", doc: `{"models": [{"slug":"m","slug":"n"}]}`, want: "duplicate field `slug` at line 1 column 35"},
		{name: "truncation missing mode", doc: `{"models": [{"truncation_policy": {}}]}`, want: "missing field `mode` at line 1 column 37"},
		{name: "truncation missing limit", doc: `{"models": [{"truncation_policy":{"mode":"tokens"}}]}`, want: "missing field `limit` at line 1 column 51"},
		{name: "truncation from sequence", doc: `{"models": [{"truncation_policy": ["tokens"]}]}`, want: "invalid length 1, expected struct TruncationPolicyConfig with 2 elements at line 1 column 45"},
		{name: "truncation sequence too long", doc: `{"models": [{"truncation_policy": ["tokens", 5, 6]}]}`, want: "invalid length 3, expected 2 elements in sequence at line 1 column 51"},
		{name: "preset missing description", doc: `{"models": [{"supported_reasoning_levels":[{"effort":"low"}]}]}`, want: "missing field `description` at line 1 column 61"},
		{name: "upgrade missing model", doc: `{"models": [{"upgrade":{}}]}`, want: "missing field `model` at line 1 column 26"},
		{name: "priority string", doc: `{"models": [{"priority":"x"}]}`, want: `invalid type: string "x", expected i32 at line 1 column 28`},
		{name: "priority float", doc: `{"models": [{"priority":1.5}]}`, want: "invalid type: map, expected i32 at line 1 column 28"},
		{name: "priority overflow", doc: `{"models": [{"priority": 2147483648}]}`, want: "invalid value: integer `2147483648`, expected i32 at line 1 column 36"},
		{name: "visibility integer", doc: `{"models": [{"visibility":5}]}`, want: "invalid type: integer `5`, expected string or map at line 1 column 28"},
		{name: "visibility unknown", doc: `{"models": [{"visibility":"bogus"}]}`, want: "unknown variant `bogus`, expected one of `list`, `hide`, `none` at line 1 column 34"},
		{name: "visibility empty map", doc: `{"models": [{"visibility": {}}]}`, want: "invalid value: map, expected map with a single key at line 1 column 30"},
		{name: "visibility unit variant", doc: `{"models": [{"visibility": {"list": 1}}]}`, want: "invalid type: integer `1`, expected unit at line 1 column 39"},
		{name: "shell type unknown", doc: `{"models": [{"shell_type":"bogus"}]}`, want: "unknown variant `bogus`, expected one of `default`, `local`, `shell_command`, `unified_exec`, `disabled` at line 1 column 34"},
		{name: "tool element type", doc: `{"models": [{"experimental_supported_tools":[5]}]}`, want: "invalid type: integer `5`, expected a string at line 1 column 48"},
		{name: "messages type", doc: `{"models": [{"model_messages":5}]}`, want: "invalid type: integer `5`, expected struct ModelMessages at line 1 column 32"},
		{name: "base instructions type", doc: `{"models": [{"base_instructions":5}]}`, want: "invalid type: integer `5`, expected a string at line 1 column 34"},
		{name: "base instructions map", doc: `{"models": [{"base_instructions": {}}]}`, want: "invalid type: map, expected a string at line 1 column 34"},
		{name: "base instructions duplicate", doc: `{"models": [{"base_instructions":"a","base_instructions":"b"}]}`, want: "duplicate field `base_instructions` at line 1 column 56"},
		{name: "missing instructions", doc: `{"models": [{` + base + `}]}`, want: "model `m` is missing both `base_instructions` and `model_messages.instructions_template` at line 1 column 270"},
		{name: "missing instructions before other fields", doc: `{"models": [{` + base + `}], "x": 1}`, want: "model `m` is missing both `base_instructions` and `model_messages.instructions_template` at line 1 column 269"},
		{name: "model messages approvals type", doc: `{"models": [{"model_messages": {"approvals": 5}}]}`, want: "invalid type: integer `5`, expected struct ApprovalMessages at line 1 column 48"},
		{name: "availability nux type", doc: `{"models": [{"availability_nux": 5}]}`, want: "invalid type: integer `5`, expected struct ModelAvailabilityNux at line 1 column 35"},
		{name: "service tier missing name", doc: `{"models": [{"service_tiers": [{"id":"a"}]}]}`, want: "missing field `name` at line 1 column 43"},
		{name: "input modality unknown", doc: `{"models": [{"input_modalities": ["text","bogus"]}]}`, want: "unknown variant `bogus`, expected one of `text`, `image`, `audio` at line 1 column 50"},
		{name: "lone surrogate", doc: `{"models": ["a\ud800"]}`, want: "unexpected end of hex escape at line 1 column 21"},
		{name: "token budget missing threshold", doc: `{"models": [{"model_messages": {"token_budget": {}}}]}`, want: "missing field `reminder_threshold_tokens` at line 1 column 52"},
		{name: "token budget enabled type", doc: `{"models": [{"model_messages": {"token_budget": {"enabled": "x"}}}]}`, want: "invalid type: string \"x\", expected a boolean at line 1 column 66"},
		{name: "token budget threshold type", doc: `{"models": [{"model_messages": {"token_budget": {"reminder_threshold_tokens": "x"}}}]}`, want: "invalid type: string \"x\", expected i64 at line 1 column 84"},
		{name: "token budget threshold float", doc: `{"models": [{"model_messages": {"token_budget": {"reminder_threshold_tokens": 1.5}}}]}`, want: "invalid type: map, expected i64 at line 1 column 84"},
		{name: "token budget fallback null", doc: `{"models": [{"model_messages": {"token_budget": {"auto_compact_fallback_prompt": null}}}]}`, want: "invalid type: null, expected a string at line 1 column 88"},
		{name: "token budget type", doc: `{"models": [{"model_messages": {"token_budget": "x"}}]}`, want: "invalid type: string \"x\", expected struct ModelTokenBudgetConfig at line 1 column 53"},
		{name: "token budget full", doc: `{"models": [{"model_messages": {"token_budget": {"reminder_threshold_tokens": 1, "reminder_message_template": "a", "guidance_message": "b", "auto_compact_fallback_prompt": "c", "auto_compact_fallback_buffer_tokens": 2}}}]}`, want: "missing field `slug` at line 1 column 220"},
		{name: "access programs missing cyber", doc: `{"models": [{"available_access_programs": {}}]}`, want: "missing field `cyber` at line 1 column 45"},
		{name: "access programs cyber type", doc: `{"models": [{"available_access_programs": {"cyber": 5}}]}`, want: "invalid type: integer `5`, expected a sequence at line 1 column 55"},
		{name: "access programs cyber element", doc: `{"models": [{"available_access_programs": {"cyber": [5]}}]}`, want: "invalid type: integer `5`, expected a string at line 1 column 57"},
		{name: "access programs cyber null", doc: `{"models": [{"available_access_programs": {"cyber": null}}]}`, want: "invalid type: null, expected a sequence at line 1 column 58"},
		{name: "access programs unknown program", doc: `{"models": [{"available_access_programs": {"cyber": ["standard","bogus"]}}]}`, want: "missing field `slug` at line 1 column 74"},
		{name: "confirmation browser type", doc: `{"models": [{"model_messages": {"confirmation_policies": {"browser_use": 5}}}]}`, want: "invalid type: integer `5`, expected a string at line 1 column 77"},
		{name: "confirmation computer type", doc: `{"models": [{"model_messages": {"confirmation_policies": {"browser_use": "x", "computer_use": 7}}}]}`, want: "invalid type: integer `7`, expected a string at line 1 column 98"},
		{name: "confirmation unknown key", doc: `{"models": [{"model_messages": {"confirmation_policies": {"browser_use": "x", "zzz": 1}}}]}`, want: "missing field `slug` at line 1 column 89"},
		{name: "root list with models", doc: `[[]]`, want: "No error", models: 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			models, err := validateModelCatalogJSON([]byte(testCase.doc))
			if testCase.want == "No error" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected %q, got nil error", testCase.want)
			}
			if err.Error() != testCase.want {
				t.Fatalf("unexpected diagnostic:\n got: %s\nwant: %s", err.Error(), testCase.want)
			}
			if models != 0 {
				t.Fatalf("expected no models, got %d", models)
			}
		})
	}
}

func TestValidateModelCatalogJSONAcceptsCatalog(t *testing.T) {
	doc := `{"models": [{"slug":"m","display_name":"M","supported_reasoning_levels":[],` +
		`"shell_type":"shell_command","visibility":"list","supported_in_api":true,"priority":1,` +
		`"support_verbosity":true,"truncation_policy":{"mode":"tokens","limit":5},` +
		`"experimental_supported_tools":[],"model_messages":{"instructions_template":"x"}}]}`
	models, err := validateModelCatalogJSON([]byte(doc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if models != 1 {
		t.Fatalf("expected 1 model, got %d", models)
	}
}

func TestValidateModelCatalogJSONAcceptsLegacyBaseInstructions(t *testing.T) {
	doc := `{"models": [{"slug":"m","display_name":"M","supported_reasoning_levels":[],` +
		`"shell_type":"unified_exec","visibility":"hide","supported_in_api":false,"priority":1,` +
		`"support_verbosity":false,"truncation_policy":{"mode":"bytes","limit":5},` +
		`"experimental_supported_tools":[],"base_instructions":"legacy"}]}`
	models, err := validateModelCatalogJSON([]byte(doc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if models != 1 {
		t.Fatalf("expected 1 model, got %d", models)
	}
}
