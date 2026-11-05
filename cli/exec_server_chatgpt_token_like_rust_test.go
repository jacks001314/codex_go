package cli

import (
	"strings"
	"testing"
)

var chatGPTTokenRegistryArgs = []string{"exec-server", "--remote", "https://registry.example.com", "--environment-id", "env-1"}

func TestExecServerChatGPTTokenStdinFlagsLikeRust(t *testing.T) {
	args := append(append([]string{}, chatGPTTokenRegistryArgs...), "--chatgpt-token-stdin", "--chatgpt-account-id", "endor")
	parsed, err := Parse(args)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !parsed.ExecServer.ChatGPTTokenStdin || parsed.ExecServer.ChatGPTAccountID != "endor" {
		t.Fatalf("parsed token flags = %t, %q", parsed.ExecServer.ChatGPTTokenStdin, parsed.ExecServer.ChatGPTAccountID)
	}

	if _, err := Parse([]string{"exec-server", "--chatgpt-token-stdin"}); err == nil || !strings.Contains(err.Error(), "required arguments were not provided") {
		t.Fatalf("bare --chatgpt-token-stdin error = %v", err)
	}
	if _, err := Parse(append(append([]string{}, chatGPTTokenRegistryArgs...), "--chatgpt-account-id", "endor")); err == nil || !strings.Contains(err.Error(), "--chatgpt-token-stdin") {
		t.Fatalf("--chatgpt-account-id without token error = %v", err)
	}
	for _, conflict := range []string{"--use-agent-identity-auth", "--executor-credential-stdin", "--registration-bearer-stdin", "--aws-sigv4"} {
		args := append(append([]string{}, chatGPTTokenRegistryArgs...), "--chatgpt-token-stdin", conflict)
		if _, err := Parse(args); err == nil || !strings.Contains(err.Error(), "cannot be used with") {
			t.Fatalf("--chatgpt-token-stdin with %s error = %v", conflict, err)
		}
	}
}

func TestExecServerCredentialConflictsAcrossForwardLikeRust(t *testing.T) {
	remote := []string{"--remote", "https://registry.example.com", "--environment-id", "env-1"}
	for _, pair := range [][2]string{{"--registration-bearer-stdin", "--executor-credential-stdin"}, {"--executor-credential-stdin", "--registration-bearer-stdin"}} {
		args := []string{"exec-server"}
		args = append(args, remote...)
		args = append(args, pair[0], "forward", "--connect", "ws://127.0.0.1:8765")
		args = append(args, remote...)
		args = append(args, pair[1])
		_, err := Parse(args)
		if err == nil || !strings.Contains(err.Error(), "--registration-bearer-stdin cannot be used with --executor-credential-stdin") {
			t.Fatalf("cross-scope credentials %v error = %v", pair, err)
		}
	}

	args := []string{"exec-server"}
	args = append(args, remote...)
	args = append(args, "--chatgpt-token-stdin", "forward", "--connect", "ws://127.0.0.1:8765")
	args = append(args, remote...)
	if _, err := Parse(args); err == nil || !strings.Contains(err.Error(), "--chatgpt-token-stdin cannot be used with forward") {
		t.Fatalf("--chatgpt-token-stdin with forward error = %v", err)
	}
}

func TestExecServerChatGPTTokenHelpAndCompletionLikeRust(t *testing.T) {
	help := SubcommandHelpText(&SubcommandHelpRequest{Path: "exec-server"})
	for _, token := range []string{"--chatgpt-token-stdin", "--chatgpt-account-id <ID>"} {
		if !strings.Contains(help, token) {
			t.Fatalf("exec-server help missing %q", token)
		}
	}
	words := flattenedCompletionWords(codexCompletionSpec())
	for _, token := range []string{"--chatgpt-token-stdin", "--chatgpt-account-id"} {
		if !hasCompletionWord(words, token) {
			t.Fatalf("completion words missing %q", token)
		}
	}
}
