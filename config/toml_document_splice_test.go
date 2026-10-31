package config

import "testing"

// The expected documents below come from the Rust 0.166 binary editing the same
// seed config with `codex mcp add`/`codex mcp remove`; toml_edit keeps every
// byte outside the mcp_servers region, including comments, multi-line strings
// and multi-line arrays, and renders the replaced region with its default decor.
func TestSpliceMCPTablesKeepsDocumentTriviaLikeTomlEdit(t *testing.T) {
	server := func(name string, command string) *OrderedTable {
		value := NewOrderedTable()
		value.Set("command", command)
		table := NewOrderedTable()
		table.Set(name, value)
		return table
	}
	cases := []struct {
		name     string
		document string
		servers  *OrderedTable
		want     string
	}{
		{
			name: "comments above and inside the replaced table go with it",
			document: "# leading file comment\n\n# comment above model\nmodel = \"o3\"\n\n" +
				"# comment above other\n[other]\n# comment inside other\nkey = 1\n\n\n" +
				"# comment above servers\n[mcp_servers.alpha]\n# comment inside alpha\n" +
				"command = \"old\"\nargs = [\"x\"]\n\n# trailing comment at end\n",
			servers: server("alpha", "npx"),
			want: "# leading file comment\n\n# comment above model\nmodel = \"o3\"\n\n" +
				"# comment above other\n[other]\n# comment inside other\nkey = 1\n\n" +
				"[mcp_servers.alpha]\ncommand = \"npx\"\n\n# trailing comment at end\n",
		},
		{
			name:     "multi-line string and array stay verbatim",
			document: "instructions = \"\"\"\nline one\n[not-a-table]\n\"\"\"\n\nnotify = [\n  \"one\",\n  \"two\",\n]\n\n[other]\nkey = 1\n",
			servers:  server("beta", "npx"),
			want: "instructions = \"\"\"\nline one\n[not-a-table]\n\"\"\"\n\nnotify = [\n  \"one\",\n  \"two\",\n]\n\n[other]\nkey = 1\n" +
				"\n[mcp_servers.beta]\ncommand = \"npx\"\n",
		},
		{
			name:     "removed region takes its blank lines and comments",
			document: "[alpha]\nx = 1\n\n# keep me?\n[mcp_servers.docs]\nurl = \"https://a/mcp\"\n\n[omega]\ny = 2\n",
			servers:  nil,
			want:     "[alpha]\nx = 1\n\n[omega]\ny = 2\n",
		},
		{
			name:     "explicit mcp_servers header survives",
			document: "[mcp_servers]\n\n[mcp_servers.alpha]\ncommand = \"a\"\n",
			servers:  server("alpha", "npx"),
			want:     "[mcp_servers]\n\n[mcp_servers.alpha]\ncommand = \"npx\"\n",
		},
		{
			name:     "CRLF documents are re-emitted with bare newlines",
			document: "[alpha]\r\nx = 1\r\n\r\n[other]\r\nkey = 1\r\n",
			servers:  server("alpha", "npx"),
			want:     "[alpha]\nx = 1\n\n[other]\nkey = 1\n\n[mcp_servers.alpha]\ncommand = \"npx\"\n",
		},
		{
			name:     "a file without a trailing newline still closes the last line",
			document: "[alpha]\nx = 1",
			servers:  server("alpha", "npx"),
			want:     "[alpha]\nx = 1\n\n[mcp_servers.alpha]\ncommand = \"npx\"\n",
		},
		{
			name:     "first table of a document gets no leading blank line",
			document: "\n\n[mcp_servers.docs]\nurl = \"https://a/mcp\"\n",
			servers:  server("docs", "npx"),
			want:     "[mcp_servers.docs]\ncommand = \"npx\"\n",
		},
		{
			name:     "trailing trivia stays at the end of the file",
			document: "model = \"o3\"\n# tail comment\n",
			servers:  server("alpha", "npx"),
			want:     "model = \"o3\"\n\n[mcp_servers.alpha]\ncommand = \"npx\"\n# tail comment\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := spliceMCPTablesIntoDocument(tc.document, tc.servers)
			if !ok {
				t.Fatalf("splice refused document %q", tc.document)
			}
			if got != tc.want {
				t.Fatalf("document mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// TestSpliceMCPTablesRefusesInlineTablesLikeTomlEdit pins the shape this writer
// still leaves to the whole-document renderer: toml_edit turns an inline
// `mcp_servers = { .. }` value into a `[mcp_servers ]` header.
func TestSpliceMCPTablesRefusesInlineTablesLikeTomlEdit(t *testing.T) {
	document := "mcp_servers = { docs = { url = \"https://a/mcp\" } }\n\n[other]\nkey = 1\n"
	if _, ok := spliceMCPTablesIntoDocument(document, NewOrderedTable()); ok {
		t.Fatalf("inline mcp_servers should fall back to the whole-document renderer")
	}
}
