package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// initTrustRepo lays out a checkout the way project discovery expects: a `.git`
// directory with a HEAD file counts as repository metadata (config.go
// gitMetadataPathExists), which is what makes repo/ the trust target.
func initTrustRepo(t *testing.T, root string) string {
	t.Helper()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.git) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(HEAD) error = %v", err)
	}
	return repo
}

func TestSetProjectTrustLevelWritesAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	key := filepath.Join(t.TempDir(), "repo")
	if err := SetProjectTrustLevel(path, key, ProjectTrustLevelTrusted); err != nil {
		t.Fatalf("SetProjectTrustLevel() error = %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	want := "[projects." + tomlBasicString(key) + "]\ntrust_level = \"trusted\"\n"
	if string(first) != want {
		t.Fatalf("document = %q, want %q", string(first), want)
	}
	if err := SetProjectTrustLevel(path, key, ProjectTrustLevelTrusted); err != nil {
		t.Fatalf("second SetProjectTrustLevel() error = %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(second) != string(first) {
		t.Fatalf("second write changed the document: %q -> %q", string(first), string(second))
	}
}

func TestSetProjectTrustLevelReplacesExistingTableAndKeepsOtherContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	// The fixture key carries backslashes on Windows so the TOML escaping path
	// is exercised there, and a POSIX spelling elsewhere. The round-trip read
	// below must use the host's own comparison rule: savedProjectTrustLevel's
	// last parameter is the Windows-only case-folding switch, so pinning it to
	// true would make the read half of this test pass only on Windows.
	windows := runtime.GOOS == "windows"
	key := "/work/repo"
	if windows {
		key = `C:\work\repo`
	}
	original := "model = \"gpt-5\"\n\n[projects." + tomlBasicString(key) + "]\ntrust_level = \"untrusted\"\n\n[mcp_servers.docs]\ncommand = \"docs\"\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := SetProjectTrustLevel(path, key, ProjectTrustLevelTrusted); err != nil {
		t.Fatalf("SetProjectTrustLevel() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(data)
	if strings.Count(text, "[projects.") != 1 {
		t.Fatalf("document has %d project tables, want 1: %q", strings.Count(text, "[projects."), text)
	}
	if !strings.Contains(text, "trust_level = \"trusted\"") {
		t.Fatalf("document = %q, want the trusted level", text)
	}
	for _, kept := range []string{"model = \"gpt-5\"", "[mcp_servers.docs]", "command = \"docs\""} {
		if !strings.Contains(text, kept) {
			t.Fatalf("document lost %q: %q", kept, text)
		}
	}
	// The rewritten table must still round-trip through the trust reader, which
	// is what the `--trust` re-check does on the next non-interactive run.
	values, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile() error = %v", err)
	}
	if key, level, ok := savedProjectTrustLevel(projectTrustTableFromValues(values), canonicalProjectPath(key), windows); !ok ||
		level != ProjectTrustLevelTrusted {
		t.Fatalf("reread trust = %q, %q, %v; want trusted", key, level, ok)
	}
}

func TestSetProjectTrustLevelRejectsUnsupportedLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SetProjectTrustLevel(path, "/repo", "maybe"); err == nil {
		t.Fatal("SetProjectTrustLevel(maybe) succeeded, want error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config file was created for an invalid level: %v", err)
	}
	if err := SetProjectTrustLevel(path, "   ", ProjectTrustLevelTrusted); err == nil {
		t.Fatal("SetProjectTrustLevel(empty key) succeeded, want error")
	}
}

func TestProjectTrustDecisionForDirPrefersSavedRepositoryDecision(t *testing.T) {
	repo := initTrustRepo(t, t.TempDir())
	child := filepath.Join(repo, "pkg", "feature")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	values := map[string]any{
		"projects": map[string]any{
			canonicalProjectPath(repo): map[string]any{"trust_level": ProjectTrustLevelTrusted},
		},
	}
	decision := ProjectTrustDecisionForDir(values, child)
	if !decision.Saved() || !decision.Trusted() {
		t.Fatalf("decision = %#v, want a saved trusted decision for the repository", decision)
	}
	if decision.TrustKey != canonicalProjectPath(repo) {
		t.Fatalf("trust key = %q, want %q", decision.TrustKey, canonicalProjectPath(repo))
	}
	if got := ProjectTrustDecisionForDir(nil, child); got.Saved() {
		t.Fatalf("decision without saved config = %#v, want unsaved", got)
	}
}

func TestProjectTrustDecisionForDirReportsExplicitlyUntrusted(t *testing.T) {
	repo := initTrustRepo(t, t.TempDir())
	values := map[string]any{
		"projects": map[string]any{
			canonicalProjectPath(repo): map[string]any{"trust_level": ProjectTrustLevelUntrusted},
		},
	}
	decision := ProjectTrustDecisionForDir(values, repo)
	if !decision.Saved() || !decision.Untrusted() || decision.Trusted() {
		t.Fatalf("decision = %#v, want a saved untrusted decision", decision)
	}
	if decision.TrustKey != canonicalProjectPath(repo) {
		t.Fatalf("trust key = %q, want %q", decision.TrustKey, canonicalProjectPath(repo))
	}
}

func TestProjectTrustDecisionForDirNamesTheKeyToGrant(t *testing.T) {
	root := t.TempDir()
	repo := initTrustRepo(t, root)
	child := filepath.Join(repo, "sub")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	// An undecided directory reports the repository root as the grant key, so
	// `--trust` grants the whole checkout rather than one subfolder.
	decision := ProjectTrustDecisionForDir(nil, child)
	if decision.Saved() {
		t.Fatalf("decision = %#v, want unsaved", decision)
	}
	if decision.TrustKey != canonicalProjectPath(repo) {
		t.Fatalf("trust key = %q, want the repository root %q", decision.TrustKey, canonicalProjectPath(repo))
	}

	// A directory with no project marker at all falls back to itself.
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatalf("MkdirAll(plain) error = %v", err)
	}
	if got, want := ProjectTrustDecisionForDir(nil, plain).TrustKey, canonicalProjectPath(plain); got != want {
		t.Fatalf("trust key = %q, want %q", got, want)
	}
	if got := ProjectTrustDecisionForDir(nil, ""); got.TrustKey != "" || got.Saved() {
		t.Fatalf("decision for empty cwd = %#v, want zero value", got)
	}
}
