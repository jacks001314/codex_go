package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/cli"
)

const gitCredentialSitesRequest = "capability[]=authtype\nprotocol=https\nhost=chatgpt.com\npath=backend-api/codex/sites/git/fixture.git\n\n"

// TestGitCredentialCapabilityLikeRust pins Rust #60746: the capability
// operation answers Git's authtype probe before any login lookup.
func TestGitCredentialCapabilityLikeRust(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runGitCredential(context.Background(), cli.GitCredentialOptions{Operation: "capability"}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("runGitCredential: %v", err)
	}
	if got, want := stdout.String(), "version 0\ncapability authtype\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

// TestGitCredentialReadsOnlyFirstRecordLikeRust checks the helper answers the
// first record and ignores a second one piped behind it.
func TestGitCredentialReadsOnlyFirstRecordLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	input := "protocol=https\nhost=chatgpt.com\n\n" + gitCredentialSitesRequest
	if err := runGitCredential(context.Background(), cli.GitCredentialOptions{Operation: "get"}, strings.NewReader(input), &stdout, &stderr); err != nil {
		t.Fatalf("runGitCredential: %v", err)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

// TestGitCredentialDeclinesWithoutFileLoginLikeRust covers the file-backed
// login gate: a keyring/auto store never feeds Git, so the helper answers
// quit=true and reports the reason on stderr.
func TestGitCredentialDeclinesWithoutFileLoginLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("cli_auth_credentials_store = \"keyring\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := runGitCredential(context.Background(), cli.GitCredentialOptions{Operation: "get"}, strings.NewReader(gitCredentialSitesRequest), &stdout, &stderr); err != nil {
		t.Fatalf("runGitCredential: %v", err)
	}
	if got, want := stdout.String(), "quit=true\n\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "requires a file-backed ChatGPT login") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestGitCredentialQuitsWithoutStoredLoginLikeRust keeps git from prompting
// when the file store simply has no login yet.
func TestGitCredentialQuitsWithoutStoredLoginLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("cli_auth_credentials_store = \"file\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := runGitCredential(context.Background(), cli.GitCredentialOptions{Operation: "get"}, strings.NewReader(gitCredentialSitesRequest), &stdout, &stderr); err != nil {
		t.Fatalf("runGitCredential: %v", err)
	}
	if got, want := stdout.String(), "quit=true\n\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

// TestGitCredentialRejectsForeignRepositoryURLsLikeRust checks the destination
// allow-list: any host/path the sites endpoint cannot serve is ignored without
// output.
func TestGitCredentialRejectsForeignRepositoryURLsLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	for _, request := range []string{
		"capability[]=authtype\nprotocol=https\nhost=evil.example\npath=backend-api/codex/sites/git/fixture.git\n\n",
		"capability[]=authtype\nprotocol=https\nhost=chatgpt.com\npath=backend-api/codex/sites/git/../etc\n\n",
		"capability[]=authtype\nprotocol=http\nhost=chatgpt.com\npath=backend-api/codex/sites/git/fixture.git\n\n",
	} {
		var stdout, stderr bytes.Buffer
		if err := runGitCredential(context.Background(), cli.GitCredentialOptions{Operation: "get"}, strings.NewReader(request), &stdout, &stderr); err != nil {
			t.Fatalf("runGitCredential: %v", err)
		}
		if stdout.String() != "" {
			t.Fatalf("request %q emitted %q", request, stdout.String())
		}
	}
}
