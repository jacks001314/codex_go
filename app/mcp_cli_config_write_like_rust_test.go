package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMCPConfigSeed lays down a config.toml for the mcp CLI before a command
// runs, so the written document can be compared byte for byte.
func writeMCPConfigSeed(t *testing.T, home string, seed string) {
	t.Helper()
	if seed == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(seed), 0o600); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}
}

func readMCPConfig(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config.toml) error = %v", err)
	}
	return string(data)
}

// TestMCPAddWritesServerTableLikeTomlEdit pins the document toml_edit 0.166
// produces: fields in serialize_mcp_server_table order, `env` as its own sorted
// table, and no `[mcp_servers]` header because Rust inserts that table
// implicitly.
func TestMCPAddWritesServerTableLikeTomlEdit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	if err := Run(context.Background(), []string{
		"mcp", "add", "alpha", "--env", "B=2", "--env", "A=1", "--", "npx", "-y", "pkg",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp add returned error: %v", err)
	}

	want := "[mcp_servers.alpha]\ncommand = \"npx\"\nargs = [\"-y\", \"pkg\"]\n\n" +
		"[mcp_servers.alpha.env]\nA = \"1\"\nB = \"2\"\n"
	if got := readMCPConfig(t, home); got != want {
		t.Fatalf("config.toml = %q, want %q", got, want)
	}
}

// TestMCPAddKeepsServerInsertionOrderLikeTomlEdit: replace_mcp_servers rewrites
// every server in insertion order, so a later add does not sort the tables.
func TestMCPAddKeepsServerInsertionOrderLikeTomlEdit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	for _, name := range []string{"zeta", "alpha"} {
		if err := Run(context.Background(), []string{
			"mcp", "add", name, "--", "npx",
		}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
			t.Fatalf("mcp add %s returned error: %v", name, err)
		}
	}

	want := "[mcp_servers.zeta]\ncommand = \"npx\"\n\n[mcp_servers.alpha]\ncommand = \"npx\"\n"
	if got := readMCPConfig(t, home); got != want {
		t.Fatalf("config.toml = %q, want %q", got, want)
	}
}

// TestMCPAddAppendsTableAfterExistingSectionsLikeTomlEdit: toml_edit edits the
// document in place, so unrelated sections keep their position and the new
// server table lands at the end instead of being sorted by path.
func TestMCPAddAppendsTableAfterExistingSectionsLikeTomlEdit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeMCPConfigSeed(t, home, "model = \"o3\"\n\n[other]\nkey = 1\n")

	if err := Run(context.Background(), []string{
		"mcp", "add", "alpha", "--", "npx",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp add returned error: %v", err)
	}

	want := "model = \"o3\"\n\n[other]\nkey = 1\n\n[mcp_servers.alpha]\ncommand = \"npx\"\n"
	if got := readMCPConfig(t, home); got != want {
		t.Fatalf("config.toml = %q, want %q", got, want)
	}
}

// TestMCPAddReplacesExistingServerTableInPlaceLikeTomlEdit: an existing table
// keeps its position while its fields are rewritten from scratch.
func TestMCPAddReplacesExistingServerTableInPlaceLikeTomlEdit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeMCPConfigSeed(t, home, "[mcp_servers.alpha]\ncommand = \"old\"\nargs = [\"x\"]\n")

	if err := Run(context.Background(), []string{
		"mcp", "add", "alpha", "--", "npx",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp add returned error: %v", err)
	}

	want := "[mcp_servers.alpha]\ncommand = \"npx\"\n"
	if got := readMCPConfig(t, home); got != want {
		t.Fatalf("config.toml = %q, want %q", got, want)
	}
}

// TestMCPRemoveLastServerEmptiesDocumentLikeTomlEdit: clearing mcp_servers
// removes the implicit table, leaving an empty document.
func TestMCPRemoveLastServerEmptiesDocumentLikeTomlEdit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeMCPConfigSeed(t, home, "[mcp_servers.alpha]\nurl = \"https://a\"\n")

	if err := Run(context.Background(), []string{
		"mcp", "remove", "alpha",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp remove returned error: %v", err)
	}

	if got := readMCPConfig(t, home); got != "" {
		t.Fatalf("config.toml = %q, want empty", got)
	}
}

// TestMCPRemoveLastServerKeepsOtherSectionsLikeTomlEdit keeps the non-MCP part
// of the document intact when the last server goes away.
func TestMCPRemoveLastServerKeepsOtherSectionsLikeTomlEdit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeMCPConfigSeed(t, home, "model = \"o3\"\n\n[mcp_servers.alpha]\ncommand = \"npx\"\n\n[other]\nkey = 1\n")

	if err := Run(context.Background(), []string{
		"mcp", "remove", "alpha",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp remove returned error: %v", err)
	}

	want := "model = \"o3\"\n\n[other]\nkey = 1\n"
	if got := readMCPConfig(t, home); got != want {
		t.Fatalf("config.toml = %q, want %q", got, want)
	}
}
