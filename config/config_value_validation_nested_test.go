package config

import "testing"

// Nested tables carry their own serde shapes; each want string is the body
// Rust 0.166 prints under "failed to load bootstrap configuration", with the
// dotted path on its own line unless the field reports a position.
func TestValidateNestedConfigTables(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "profile model is a string",
			toml: "[profiles.x]\nmodel = 1\n",
			want: "invalid type: integer `1`, expected a string\nin `profiles.x.model`",
		},
		{
			name: "profile approval policy lists variants",
			toml: "[profiles.x]\napproval_policy = \"bogus\"\n",
			want: "unknown variant `bogus`, expected one of `untrusted`, `on-failure`, `on-request`, `granular`, `never`\nin `profiles.x.approval_policy`",
		},
		{
			name: "profile entry is a struct",
			toml: "profiles = { x = 5 }\n",
			want: "invalid type: integer `5`, expected struct ConfigProfile\nin `profiles.x`",
		},
		{
			name: "profile tui picker is an enum",
			toml: "[profiles.x]\ntui = { session_picker_view = \"bogus\" }\n",
			want: "unknown variant `bogus`, expected `comfortable` or `dense`\nin `profiles.x.tui.session_picker_view`",
		},
		{
			name: "profile features are booleans",
			toml: "[profiles.x]\nfeatures = { y = 1 }\n",
			want: "invalid type: integer `1`, expected a boolean\nin `profiles.x.features`",
		},
		{
			name: "profile unknown keys stay allowed",
			toml: "[profiles.x]\nunknown_key = 1\n",
			want: "",
		},
		{
			name: "provider name type",
			toml: "[model_providers.y]\nname = 5\nbase_url = \"https://e.com\"\n",
			want: "invalid type: integer `5`, expected a string\nin `model_providers.y.name`",
		},
		{
			name: "provider requires a name",
			toml: "[model_providers.y]\nbase_url = \"https://e.com\"\n",
			want: "model_providers.y: provider name must not be empty\nin `model_providers`",
		},
		{
			name: "provider chat wire api is retired",
			toml: "[model_providers.x]\nname = \"M\"\nwire_api = \"chat\"\n",
			want: "`wire_api = \"chat\"` is no longer supported.\n" +
				"How to fix: set `wire_api = \"responses\"` in your provider config.\n" +
				"More info: https://github.com/openai/codex/discussions/7782\n" +
				"in `model_providers.x.wire_api`",
		},
		{
			name: "provider wire api has one variant",
			toml: "[model_providers.x]\nname = \"M\"\nwire_api = \"bogus\"\n",
			want: "unknown variant `bogus`, expected `responses`\nin `model_providers.x.wire_api`",
		},
		{
			name: "built-in providers cannot be overridden",
			toml: "[model_providers.openai]\nbase_url = \"https://e.com\"\n",
			want: "model_providers contains reserved built-in provider IDs: `openai`. " +
				"Built-in providers cannot be overridden. Rename your custom provider (for example, `openai-custom`).\n" +
				"in `model_providers`",
		},
		{
			name: "windows sandbox is an enum",
			toml: "[windows]\nsandbox = \"bogus\"\n",
			want: "unknown variant `bogus`, expected one of `elevated`, `unelevated`, `mxc`\nin `windows.sandbox`",
		},
		{
			name: "history persistence is an enum",
			toml: "[history]\npersistence = \"bogus\"\n",
			want: "unknown variant `bogus`, expected `save-all` or `none`\nin `history.persistence`",
		},
		{
			name: "history max bytes is unsigned",
			toml: "[history]\nmax_bytes = -1\n",
			want: "invalid value: integer `-1`, expected usize\nin `history.max_bytes`",
		},
		{
			name: "shell policy inherit is an enum",
			toml: "[shell_environment_policy]\ninherit = \"bogus\"\n",
			want: "unknown variant `bogus`, expected one of `core`, `all`, `none`\nin `shell_environment_policy.inherit`",
		},
		{
			name: "shell policy env set is a string map",
			toml: "[shell_environment_policy]\n[shell_environment_policy.set]\nK = 5\n",
			want: "invalid type: integer `5`, expected a string\nin `shell_environment_policy.set.K`",
		},
		{
			name: "shell policy exclude reports the table header",
			toml: "x = 1\n[shell_environment_policy]\nexclude = [1]\n",
			want: "C:\\home\\config.toml:2:1: invalid type: integer `1`, expected a string",
		},
		{
			name: "shell policy filters report the table header",
			toml: "[shell_environment_policy]\nfilters = { a = 1 }\n",
			want: "C:\\home\\config.toml:1:1: invalid type: unit variant, expected string only",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values, err := ParseConfigDocumentValues([]byte(tc.toml))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			locator := &ConfigDocumentLocator{Path: `C:\home\config.toml`, Text: tc.toml}
			err = ValidateConfigDocumentValues(values, locator)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected rejection: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected rejection, got nil")
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("message mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}
