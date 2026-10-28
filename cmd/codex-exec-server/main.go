// Command codex-exec-server is the standalone executor entry point. Startup and
// helper dispatch are shared with the full Codex CLI (Rust #57589).
package main

import (
	"io"
	"os"

	"codex_go/cli"
	"codex_go/cmd/internal/codexmain"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func runCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return codexmain.Main(args, os.Args[0], stdin, stdout, stderr, codexmain.Options{ExecServerStandalone: true})
}

// parseStandaloneArguments parses executor options without requiring the
// `exec-server` subcommand, matching Rust's standalone clap definition.
func parseStandaloneArguments(args []string) (*cli.Parsed, error) {
	return cli.Parse(append([]string{string(cli.CommandExecServer)}, args...))
}
