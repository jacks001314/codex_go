package cli

import (
	"strings"
	"testing"
)

func TestGenerateCompletionBash(t *testing.T) {
	script, err := GenerateCompletion("bash", "codex")
	if err != nil {
		t.Fatalf("GenerateCompletion returned error: %v", err)
	}
	if !strings.Contains(script, "complete -F") || !strings.Contains(script, "--bearer-token-env-var") || !strings.Contains(script, "generate-json-schema") {
		t.Fatalf("bash completion = %q", script)
	}
}

func TestGenerateCompletionRejectsUnknownShell(t *testing.T) {
	_, err := GenerateCompletion("tcsh", "codex")
	if err == nil {
		t.Fatal("GenerateCompletion returned nil error, want failure")
	}
}

func TestCompletionSpecMatchesRustVisibleSurface(t *testing.T) {
	words := flattenedCompletionWords(codexCompletionSpec())
	for _, want := range []string{
		"exec", "e", "review", "mcp", "plugin", "app-server", "remote-control",
		"completion", "update", "doctor", "sandbox", "apply", "a", "resume",
		"archive", "delete", "unarchive", "fork", "cloud", "exec-server",
		"features", "generate-json-schema", "enable-remote-control",
		"--ws-shared-secret-file", "--sandbox-state-readable-root",
	} {
		if !hasCompletionWord(words, want) {
			t.Fatalf("completion words missing %q in %#v", want, words)
		}
	}
	for _, hidden := range []string{
		"execpolicy",
		"responses-api-proxy",
		"stdio-to-uds",
		"generate-internal-json-schema",
		"pid-update-loop",
		"--history-mode",
		"--last-n",
		"--last-turn-id",
	} {
		if hasCompletionWord(words, hidden) {
			t.Fatalf("completion words include hidden Rust command %q in %#v", hidden, words)
		}
	}
}

func TestGenerateCompletionAllShellsIncludeNestedSurface(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell", "elvish"} {
		t.Run(shell, func(t *testing.T) {
			script, err := GenerateCompletion(shell, "codex")
			if err != nil {
				t.Fatalf("GenerateCompletion returned error: %v", err)
			}
			for _, want := range []string{"cloud", "exec-server", "mcp", "add", "--oauth-client-id", "marketplace", "--sparse", "daemon", "bootstrap", "--remote-control"} {
				if !strings.Contains(script, want) {
					t.Fatalf("%s completion missing %q: %q", shell, want, script)
				}
			}
			for _, hidden := range []string{"execpolicy", "responses-api-proxy", "stdio-to-uds"} {
				if strings.Contains(script, hidden) {
					t.Fatalf("%s completion includes hidden command %q: %q", shell, hidden, script)
				}
			}
		})
	}
}

func hasCompletionWord(words []string, want string) bool {
	for _, word := range words {
		if word == want {
			return true
		}
	}
	return false
}
