package config

// Rust #62372: features.guardianv3 accepts the optional instruction cap and the
// configurable housekeeper routing and reviewer retrieval guidance, rejects
// unknown nested fields and bad types, and project configuration may not supply
// the prompts or the cap.

import (
	"os"
	"strings"
	"testing"
)

func TestGuardianV3GuidanceConfigLikeRust(t *testing.T) {
	body := `
[features.guardianv3]
enabled = true
max_instruction_tokens = 200

[features.guardianv3.housekeeper]
routing_instructions = "Use my routing"
batch_timeout_ms = 15000

[features.guardianv3.reviewer]
retrieval_instructions = "Use my retrieval"
`
	dir := t.TempDir()
	if err := os.WriteFile(ConfigPath(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadEffectiveWithOptions(dir, &EffectiveOptions{StrictConfig: true})
	if err != nil {
		t.Fatalf("guardianv3 guidance config rejected: %v", err)
	}
	if !cfg.FeatureSettings()["guardianv3"] {
		t.Fatal("guardianv3 guidance config did not enable the feature")
	}
}

func TestGuardianV3GuidanceRejectsBadFieldsLikeRust(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unknown housekeeper field",
			body: "[features.guardianv3.housekeeper]\nunknown = true\n",
			want: "features.guardianv3.housekeeper.unknown",
		},
		{
			name: "unknown reviewer field",
			body: "[features.guardianv3.reviewer]\nunknown = true\n",
			want: "features.guardianv3.reviewer.unknown",
		},
		{
			name: "routing instructions must be a string",
			body: "[features.guardianv3.housekeeper]\nrouting_instructions = 5\n",
			want: "features.guardianv3.housekeeper.routing_instructions",
		},
		{
			name: "batch timeout must be positive",
			body: "[features.guardianv3.housekeeper]\nbatch_timeout_ms = 0\n",
			want: "features.guardianv3.housekeeper.batch_timeout_ms",
		},
		{
			name: "instruction cap must be an integer",
			body: "[features.guardianv3]\nmax_instruction_tokens = \"lots\"\n",
			want: "features.guardianv3.max_instruction_tokens",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(ConfigPath(dir), []byte(testCase.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadEffectiveWithOptions(dir, &EffectiveOptions{StrictConfig: true}); err == nil {
				t.Fatalf("config %q loaded, want an error", testCase.body)
			} else if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want it to mention %s", err, testCase.want)
			}
		})
	}
}

func TestGuardianV3GuidanceProjectSanitizeLikeRust(t *testing.T) {
	values := map[string]any{
		"features": map[string]any{
			"guardianv3": map[string]any{
				"enabled":                true,
				"max_instruction_tokens": int64(200),
				"housekeeper": map[string]any{
					"routing_instructions": "project routing",
					"batch_timeout_ms":     int64(15000),
				},
				"reviewer": map[string]any{
					"retrieval_instructions": "project retrieval",
				},
			},
		},
	}
	ignored := sanitizeProjectConfigValues(values, CredentialBrokerProjectUnconfigured, nil)
	guardianV3 := values["features"].(map[string]any)["guardianv3"].(map[string]any)
	if _, ok := guardianV3["max_instruction_tokens"]; ok {
		t.Fatal("project config kept max_instruction_tokens")
	}
	housekeeper := guardianV3["housekeeper"].(map[string]any)
	if _, ok := housekeeper["routing_instructions"]; ok {
		t.Fatal("project config kept routing_instructions")
	}
	if housekeeper["batch_timeout_ms"] != int64(15000) {
		t.Fatalf("project batch_timeout_ms = %#v, want it retained", housekeeper["batch_timeout_ms"])
	}
	reviewer := guardianV3["reviewer"].(map[string]any)
	if _, ok := reviewer["retrieval_instructions"]; ok {
		t.Fatal("project config kept retrieval_instructions")
	}
	want := map[string]bool{
		"features.guardianv3.max_instruction_tokens":           true,
		"features.guardianv3.housekeeper.routing_instructions": true,
		"features.guardianv3.reviewer.retrieval_instructions":  true,
	}
	for _, key := range ignored {
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing ignored keys: %v (got %v)", want, ignored)
	}
}
