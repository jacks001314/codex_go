package main

import (
	"os"

	setupmain "codex_go/sandbox/windowssandbox/bin/setup_main"
)

func main() {
	os.Exit(setupmain.Run(os.Args[1:], os.Stdout, os.Stderr))
}
