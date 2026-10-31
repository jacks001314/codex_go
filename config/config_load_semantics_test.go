package config

import "testing"

// Each want string is what Rust 0.166 reports under
// "failed to load configuration\n\nCaused by:\n" for the same document.
func TestValidateLoadedConfigSemantics(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "legacy profile selector",
			toml: "profile = \"x\"\n",
			want: "legacy `profile = \"x\"` config is no longer supported; use `--profile x` with `x.config.toml` instead",
		},
		{
			name: "empty legacy profile selector",
			toml: "profile = \"\"\n",
			want: "legacy `profile = \"\"` config is no longer supported; use `--profile ` with `.config.toml` instead",
		},
		{
			name: "custom permission profile needs a table",
			toml: "default_permissions = \"x\"\n",
			want: "default_permissions requires a `[permissions]` table",
		},
		{
			name: "built-in name without colon needs a table",
			toml: "default_permissions = \"read-only\"\n",
			want: "default_permissions requires a `[permissions]` table",
		},
		{
			name: "permission table without the profile",
			toml: "default_permissions = \"x\"\n[permissions]\n",
			want: "default_permissions refers to undefined profile `x`",
		},
		{
			name: "profiles without a default",
			toml: "[permissions.x]\n",
			want: "config defines `[permissions]` profiles but does not set `default_permissions`",
		},
		{
			name: "unknown built-in profile",
			toml: "default_permissions = \":workspace-write\"\n",
			want: "default_permissions refers to unknown built-in profile `:workspace-write`",
		},
		{
			name: "unknown model provider",
			toml: "model_provider = \"x\"\n",
			want: "Model provider `x` not found",
		},
		{
			name: "empty model provider",
			toml: "model_provider = \"\"\n",
			want: "Model provider `` not found",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values, err := ParseConfigDocumentValues([]byte(tc.toml))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			err = ValidateLoadedConfigSemantics(values)
			if err == nil {
				t.Fatalf("expected rejection, got nil")
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("message mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestValidateLoadedConfigSemanticsAcceptsValidSelectors(t *testing.T) {
	tests := []struct {
		name string
		toml string
	}{
		{"built-in permission profile", "default_permissions = \":read-only\"\n"},
		{"defined permission profile", "default_permissions = \"x\"\n[permissions.x]\n"},
		{"built-in provider", "model_provider = \"gcp\"\n"},
		{"configured provider", "model_provider = \"my\"\n[model_providers.my]\nname = \"M\"\nbase_url = \"https://e.com\"\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values, err := ParseConfigDocumentValues([]byte(tc.toml))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := ValidateLoadedConfigSemantics(values); err != nil {
				t.Fatalf("unexpected rejection: %v", err)
			}
		})
	}
}

// The retired ollama-chat ID keeps its own multi-line migration message.
func TestValidateLoadedConfigSemanticsRejectsLegacyOllamaChat(t *testing.T) {
	values, err := ParseConfigDocumentValues([]byte("model_provider = \"ollama-chat\"\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = ValidateLoadedConfigSemantics(values)
	if err == nil {
		t.Fatalf("expected rejection, got nil")
	}
	want := "`ollama-chat` is no longer supported.\n" +
		"How to fix: replace `ollama-chat` with `ollama` in `model_provider`, `oss_provider`, or `--local-provider`.\n" +
		"More info: https://github.com/openai/codex/discussions/7782"
	if got := err.Error(); got != want {
		t.Fatalf("message mismatch\n got: %q\nwant: %q", got, want)
	}
}
