package cli

import (
	"embed"
	"strings"
)

// The completion scripts below are clap_complete's output for codex-cli
// 0.166.0, captured verbatim from `codex completion <shell>`. Rust generates
// them from its clap command tree, which includes the hidden subcommands
// (`execpolicy`, `responses-api-proxy`, `stdio-to-uds`, ...) and every flag of
// every nested command - data this package's hand-written completionNode tree
// does not carry. Reusing the generated artefact keeps `codex completion`
// byte-identical to the reference binary; regenerate the files with
//
//	for shell in bash zsh fish powershell elvish; do
//		codex completion "$shell" > cli/completiondata/"$shell".txt
//	done
//
//go:embed completiondata/bash.txt completiondata/zsh.txt completiondata/fish.txt completiondata/powershell.txt completiondata/elvish.txt
var completionScripts embed.FS

// completionScript returns the embedded clap_complete script for shell. It only
// applies to the canonical binary name: the scripts spell the command name out
// in their function, case labels and `complete` invocations, so any other name
// falls back to the hand-written generators in completion.go.
func completionScript(shell string, commandName string) (string, bool) {
	if strings.TrimSpace(commandName) != "codex" {
		return "", false
	}
	data, err := completionScripts.ReadFile("completiondata/" + shell + ".txt")
	if err != nil {
		return "", false
	}
	return string(data), true
}
