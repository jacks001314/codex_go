package config

import "testing"

// TestRenderTOMLKeepsOrderedTableInsertionOrderLikeTomlEdit mirrors toml_edit
// render_mcp_servers: an implicit mcp_servers table has no header of its own,
// and the server tables keep the order replace_mcp_servers inserted them in
// instead of being sorted by path.
func TestRenderTOMLKeepsOrderedTableInsertionOrderLikeTomlEdit(t *testing.T) {
	servers := NewOrderedTable()
	servers.Implicit = true
	servers.Set("zeta", map[string]any{"command": "z"})
	servers.Set("alpha", map[string]any{"command": "a"})

	values := map[string]any{"mcp_servers": servers}
	want := "[mcp_servers.zeta]\ncommand = \"z\"\n\n[mcp_servers.alpha]\ncommand = \"a\"\n"
	if got := renderTOML(values); got != want {
		t.Fatalf("renderTOML = %q, want %q", got, want)
	}
}

// TestRenderTOMLKeepsServerFieldOrderLikeTomlEdit mirrors
// serialize_mcp_server_table: the server table renders its own keys in
// declaration order, and its env sub-table sorts keys like table_from_pairs.
func TestRenderTOMLKeepsServerFieldOrderLikeTomlEdit(t *testing.T) {
	server := NewOrderedTable()
	server.Set("command", "npx")
	server.Set("args", []string{"-y", "pkg"})
	server.Set("env", map[string]any{"B": "2", "A": "1"})

	servers := NewOrderedTable()
	servers.Implicit = true
	servers.Set("alpha", server)

	want := "[mcp_servers.alpha]\ncommand = \"npx\"\nargs = [\"-y\", \"pkg\"]\n\n" +
		"[mcp_servers.alpha.env]\nA = \"1\"\nB = \"2\"\n"
	if got := renderTOML(map[string]any{"mcp_servers": servers}); got != want {
		t.Fatalf("renderTOML = %q, want %q", got, want)
	}
}

// TestRenderTOMLSortsPlainMapSections confirms the ordered-table support did not
// change how ordinary config maps render.
func TestRenderTOMLSortsPlainMapSections(t *testing.T) {
	values := map[string]any{
		"model": "o3",
		"other": map[string]any{"key": int64(1)},
		"alpha": map[string]any{"key": int64(2)},
	}
	want := "model = \"o3\"\n\n[alpha]\nkey = 2\n\n[other]\nkey = 1\n"
	if got := renderTOML(values); got != want {
		t.Fatalf("renderTOML = %q, want %q", got, want)
	}
}

// TestOrderedTableMarshalJSONMatchesMap keeps the config fingerprint and the
// JSON views independent of the rendering order.
func TestOrderedTableMarshalJSONMatchesMap(t *testing.T) {
	table := NewOrderedTable()
	table.Set("b", int64(2))
	table.Set("a", int64(1))
	encoded, err := table.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error = %v", err)
	}
	if string(encoded) != `{"a":1,"b":2}` {
		t.Fatalf("MarshalJSON = %s", encoded)
	}
}
