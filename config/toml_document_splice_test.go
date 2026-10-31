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

// TestSpliceMCPTablesConvertsInlineValuesLikeTomlEdit pins what the Rust 0.166
// binary writes when the seed document spells `mcp_servers` as an inline table:
// toml_edit turns that value into a regular table
// (document_helpers::table_from_inline), so the surviving servers keep their
// inline spelling under a header that carries the key's decor, the key is
// printed after the other root values, and only new servers become tables.
func TestSpliceMCPTablesConvertsInlineValuesLikeTomlEdit(t *testing.T) {
	withServer := func(table *OrderedTable, name string, fields ...any) *OrderedTable {
		value := NewOrderedTable()
		for i := 0; i+1 < len(fields); i += 2 {
			value.Set(fields[i].(string), fields[i+1])
		}
		table.Set(name, value)
		return table
	}
	inline := func(name string, fields ...any) *OrderedTable {
		return withServer(NewOrderedTable(), name, fields...)
	}
	cases := []struct {
		name     string
		document string
		servers  *OrderedTable
		want     string
	}{
		{
			name:     "the key decor becomes the header of the converted table",
			document: "mcp_servers = { alpha = { command = \"a\" } }\n",
			servers:  inline("alpha", "command", "cmd"),
			want:     "[mcp_servers ]\nalpha = { command = \"cmd\" }\n",
		},
		{
			name:     "new servers follow the converted table as their own tables",
			document: "mcp_servers = { alpha = { command = \"a\" } }\n",
			servers:  withServer(inline("alpha", "command", "a"), "gamma", "command", "cmd"),
			want:     "[mcp_servers ]\nalpha = { command = \"a\" }\n\n[mcp_servers.gamma]\ncommand = \"cmd\"\n",
		},
		{
			name:     "a comment above the key moves inside the header",
			document: "# c\nmcp_servers = { alpha = { command = \"a\" } } # trailing\ntop = 1\n",
			servers:  withServer(inline("alpha", "command", "a"), "gamma", "command", "cmd"),
			want:     "top = 1\n\n[# c\nmcp_servers ]\nalpha = { command = \"a\" }\n\n[mcp_servers.gamma]\ncommand = \"cmd\"\n",
		},
		{
			name:     "an empty inline table prints no header",
			document: "x = 1\nmcp_servers = {}\n",
			servers:  inline("gamma", "command", "cmd"),
			want:     "x = 1\n\n[mcp_servers.gamma]\ncommand = \"cmd\"\n",
		},
		{
			name:     "removing every server removes the inline key",
			document: "model = \"x\"\nmcp_servers = { alpha = { command = \"a\", args = [\"-q\"] } }\napproval_policy = \"never\"\n",
			servers:  nil,
			want:     "model = \"x\"\napproval_policy = \"never\"\n",
		},
		{
			name:     "a merge keeps the pair decor and drops the stale field",
			document: "model = \"x\"\nmcp_servers = { alpha = { command = \"a\", args = [\"-q\"] } }\napproval_policy = \"never\"\n",
			servers:  inline("alpha", "command", "aaa"),
			want:     "model = \"x\"\napproval_policy = \"never\"\n\n[mcp_servers ]\nalpha = { command = \"aaa\"}\n",
		},
		{
			name:     "a new field keeps the separator the merge leaves behind",
			document: "mcp_servers = { alpha = { command = \"a\" } }\n",
			servers:  inline("alpha", "command", "a", "args", []string{"x", "y"}),
			want:     "[mcp_servers ]\nalpha = { command = \"a\" , args = [\"x\", \"y\"] }\n",
		},
		{
			name:     "an explicit header with inline servers keeps both",
			document: "[mcp_servers]\nalpha = { command = \"a\" }\n",
			servers:  withServer(inline("alpha", "command", "a"), "gamma", "command", "cmd"),
			want:     "[mcp_servers]\nalpha = { command = \"a\" }\n\n[mcp_servers.gamma]\ncommand = \"cmd\"\n",
		},
		{
			name:     "an empty explicit header stays above the new tables",
			document: "[mcp_servers]\n",
			servers:  inline("gamma", "command", "cmd"),
			want:     "[mcp_servers]\n\n[mcp_servers.gamma]\ncommand = \"cmd\"\n",
		},
		{
			name:     "an empty document still accepts a new server",
			document: "",
			servers:  inline("gamma", "command", "cmd"),
			want:     "[mcp_servers.gamma]\ncommand = \"cmd\"\n",
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
