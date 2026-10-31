package main

import (
	"fmt"
	"os"

	"codex_go/windowsandboxservice"
)

func main() {
	mode, err := windowsandboxservice.ParseRunMode(os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := windowsandboxservice.Run(mode); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
