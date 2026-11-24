package config

// Rust #62372: features.guardianv3 accepts the optional instruction cap and the
// configurable housekeeper routing and reviewer retrieval guidance, rejects
// unknown nested fields and bad types, and project configuration may not supply
// the prompts or the cap.

import (
	"os"
	"strings"
	"testing"
	"time"
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

// TestGuardianV3GuidanceAccessorsLikeRust keeps the validated prompts readable
// by the consumers that build the housekeeper and reviewer prompts (#62372).
func TestGuardianV3GuidanceAccessorsLikeRust(t *testing.T) {
	body := `
[features.guardianv3]
enabled = true

[features.guardianv3.housekeeper]
routing_instructions = "Use my routing"

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
	if got, ok := cfg.GuardianV3RoutingInstructions(); !ok || got != "Use my routing" {
		t.Fatalf("routing instructions = %q, %v", got, ok)
	}
	if got, ok := cfg.GuardianV3RetrievalInstructions(); !ok || got != "Use my retrieval" {
		t.Fatalf("retrieval instructions = %q, %v", got, ok)
	}
	// An unset section reports absence instead of an empty value.
	if got, ok := (&Config{Values: map[string]any{}}).GuardianV3RoutingInstructions(); ok || got != "" {
		t.Fatalf("missing routing instructions = %q, %v", got, ok)
	}
	if got, ok := (*Config)(nil).GuardianV3RetrievalInstructions(); ok || got != "" {
		t.Fatalf("nil config retrieval instructions = %q, %v", got, ok)
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

// TestGuardianV3InstructionTokenCapLikeRust checks
// features.guardianv3.max_instruction_tokens: each custom prompt is trimmed,
// estimated with the shared token count and rejected above the cap (#62372).
func TestGuardianV3InstructionTokenCapLikeRust(t *testing.T) {
	long := strings.Repeat("word ", 100)
	body := "[features.guardianv3]\nmax_instruction_tokens = 10\n\n[features.guardianv3.housekeeper]\nrouting_instructions = \"" + long + "\"\n"
	dir := t.TempDir()
	if err := os.WriteFile(ConfigPath(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEffectiveWithOptions(dir, &EffectiveOptions{StrictConfig: true}); err == nil {
		t.Fatal("an oversized routing prompt loaded without an error")
	} else if !strings.Contains(err.Error(), "features.guardianv3.housekeeper.routing_instructions is") ||
		!strings.Contains(err.Error(), "exceeding max_instruction_tokens = 10") {
		t.Fatalf("error = %v, want the token-cap message", err)
	}

	retrieval := "[features.guardianv3]\nmax_instruction_tokens = 10\n\n[features.guardianv3.reviewer]\nretrieval_instructions = \"" + long + "\"\n"
	dir = t.TempDir()
	if err := os.WriteFile(ConfigPath(dir), []byte(retrieval), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEffectiveWithOptions(dir, &EffectiveOptions{StrictConfig: true}); err == nil {
		t.Fatal("an oversized retrieval prompt loaded without an error")
	} else if !strings.Contains(err.Error(), "features.guardianv3.reviewer.retrieval_instructions is") {
		t.Fatalf("error = %v, want the retrieval key", err)
	}

	// A prompt at or under the cap loads, and whitespace does not count.
	fits := "[features.guardianv3]\nmax_instruction_tokens = 10\n\n[features.guardianv3.housekeeper]\nrouting_instructions = \"   keep evidence routed   \"\n"
	dir = t.TempDir()
	if err := os.WriteFile(ConfigPath(dir), []byte(fits), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadEffectiveWithOptions(dir, &EffectiveOptions{StrictConfig: true})
	if err != nil {
		t.Fatalf("a capped prompt was rejected: %v", err)
	}
	if limit, ok := cfg.GuardianV3MaxInstructionTokens(); !ok || limit != 10 {
		t.Fatalf("max instruction tokens = %d, %v", limit, ok)
	}
}

// TestGuardianV3HousekeeperBatchTimeoutLikeRust keeps Rust's 90 second default
// and applies a configured positive batch_timeout_ms.
func TestGuardianV3HousekeeperBatchTimeoutLikeRust(t *testing.T) {
	if got := (&Config{Values: map[string]any{}}).GuardianV3HousekeeperBatchTimeout(); got != 90*time.Second {
		t.Fatalf("default batch timeout = %v, want 90s", got)
	}
	cfg := &Config{Values: map[string]any{"features": map[string]any{"guardianv3": map[string]any{
		"housekeeper": map[string]any{"batch_timeout_ms": int64(15000)},
	}}}}
	if got := cfg.GuardianV3HousekeeperBatchTimeout(); got != 15*time.Second {
		t.Fatalf("configured batch timeout = %v, want 15s", got)
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
