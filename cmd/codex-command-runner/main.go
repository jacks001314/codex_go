package main

import (
	"os"

	commandrunner "codex_go/sandbox/windowssandbox/bin/command_runner"
)

func main() {
	os.Exit(commandrunner.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
