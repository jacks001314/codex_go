package config

import "testing"

// Each want string is the body Rust 0.166 prints under
// "failed to load bootstrap configuration\n\nCaused by:\n" for the same
// document, with the four-space anyhow indentation removed.
func TestValidateConfigValuesReportsSerdeShapeErrors(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "scalar field keeps its type",
			toml: "model = 123\n",
			want: "invalid type: integer `123`, expected a string\nin `model`",
		},
		{
			name: "closed enum lists every variant",
			toml: "approval_policy = \"bogus\"\n",
			want: "unknown variant `bogus`, expected one of `untrusted`, `on-failure`, `on-request`, `granular`, `never`\nin `approval_policy`",
		},
		{
			name: "features sub-table is boolean only",
			toml: "[features]\nx = 1\n",
			want: "invalid type: integer `1`, expected a boolean\nin `features`",
		},
		{
			name: "mcp server command is a string",
			toml: "[mcp_servers.x]\ncommand = 5\n",
			want: "invalid type: integer `5`, expected a string\nin `mcp_servers.x.command`",
		},
		{
			name: "mcp server entry is a table",
			toml: "mcp_servers = { x = 5 }\n",
			want: "invalid type: integer `5`, expected struct RawMcpServerConfig\nin `mcp_servers.x`",
		},
		{
			name: "mcp server table itself is a map",
			toml: "mcp_servers = 5\n",
			want: "invalid type: integer `5`, expected a map\nin `mcp_servers`",
		},
		{
			name: "mcp server env values are strings",
			toml: "[mcp_servers.x]\ncommand = \"a\"\n[mcp_servers.x.env]\nK = 5\n",
			want: "invalid type: integer `5`, expected a string\nin `mcp_servers.x.env.K`",
		},
		{
			name: "mcp server auth is a closed enum",
			toml: "[mcp_servers.x]\ncommand = \"a\"\nauth = \"bogus\"\n",
			want: "unknown variant `bogus`, expected one of `oauth`, `chatgpt`, `ema_auth`\nin `mcp_servers.x.auth`",
		},
		{
			name: "stdio servers reject http fields",
			toml: "[mcp_servers.x]\ncommand = \"a\"\nurl = \"https://a\"\n",
			want: "url is not supported for stdio\nin `mcp_servers.x`",
		},
		{
			name: "untagged workspace id enum",
			toml: "forced_chatgpt_workspace_id = {a=1}\n",
			want: "data did not match any variant of untagged enum Repr\nin `forced_chatgpt_workspace_id`",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values, err := ParseConfigDocumentValues([]byte(tc.toml))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			err = ValidateConfigValues(values)
			if err == nil {
				t.Fatalf("expected rejection, got nil")
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("message mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// The two fields whose serde errors quote a TOML position are rendered with the
// file, line and column; `projects` keeps the inner error as a second cause.
func TestValidateConfigDocumentValuesReportsPositions(t *testing.T) {
	locator := func(text string) *ConfigDocumentLocator {
		return &ConfigDocumentLocator{Path: `C:\home\config.toml`, Text: text}
	}
	tests := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "projects keeps both causes",
			toml: "projects = 5\n",
			want: "0: C:\\home\\config.toml:1:12: invalid type: integer `5`, expected a map\n" +
				"1: invalid type: integer `5`, expected a map\n   in `projects`",
		},
		{
			name: "project_root_markers carries only the position",
			toml: "project_root_markers = 5\n",
			want: "C:\\home\\config.toml:1:24: invalid type: integer `5`, expected a sequence",
		},
		{
			name: "array of tables reports the header column",
			toml: "[[projects]]\n",
			want: "0: C:\\home\\config.toml:1:1: invalid type: sequence, expected a map\n" +
				"1: invalid type: sequence, expected a map\n   in `projects`",
		},
		{
			name: "indented array of tables reports the bracket",
			toml: "  [[projects]]\n",
			want: "0: C:\\home\\config.toml:1:3: invalid type: sequence, expected a map\n" +
				"1: invalid type: sequence, expected a map\n   in `projects`",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values, err := ParseConfigDocumentValues([]byte(tc.toml))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			err = ValidateConfigDocumentValues(values, locator(tc.toml))
			if err == nil {
				t.Fatalf("expected rejection, got nil")
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("message mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// `codex mcp remove` decodes only the server map, so the path loses the
// `mcp_servers.` prefix that the bootstrap pass reports.
func TestValidateMCPServersValuesUsesServerRelativePaths(t *testing.T) {
	values, err := ParseConfigDocumentValues([]byte("[mcp_servers.x]\ncommand = 5\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = ValidateMCPServersValues(values)
	if err == nil {
		t.Fatalf("expected rejection, got nil")
	}
	want := "invalid type: integer `5`, expected a string\nin `x.command`"
	if got := err.Error(); got != want {
		t.Fatalf("message mismatch\n got: %q\nwant: %q", got, want)
	}
}

// Top-level type errors are irrelevant to `codex mcp remove`, which only decodes
// the server map.
func TestValidateMCPServersValuesIgnoresTopLevelTypes(t *testing.T) {
	values, err := ParseConfigDocumentValues([]byte("model = 123\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := ValidateMCPServersValues(values); err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
}
