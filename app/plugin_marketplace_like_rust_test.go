package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestPluginMarketplaceListSkipsUnnamedEntriesLikeRust pins Rust's listing,
// which only reports configured and installed marketplaces; the plugin service
// also emits a synthetic unnamed entry for unattributed plugins that must not
// surface here.
func TestPluginMarketplaceListSkipsUnnamedEntriesLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"plugin", "marketplace", "list", "--json"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("plugin marketplace list --json returned error: %v", err)
	}
	if got, want := stdout.String(), "{\n  \"marketplaces\": []\n}\n"; got != want {
		t.Fatalf("json stdout = %q, want %q", got, want)
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"plugin", "marketplace", "list"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("plugin marketplace list returned error: %v", err)
	}
	if got, want := stdout.String(), "No plugin marketplaces in scope.\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

// TestPluginMarketplaceRemoveUnknownErrorsLikeRust pins the rejection of a
// marketplace that is neither configured nor installed.
func TestPluginMarketplaceRemoveUnknownErrorsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"plugin", "marketplace", "remove", "nosuch"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err == nil {
		t.Fatalf("plugin marketplace remove nosuch returned nil error")
	}
	want := "marketplace `nosuch` is not configured or installed"
	if err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}
