package main

import (
	"os"

	"codex_go/cmd/internal/codexmain"
)

func main() {
	os.Exit(codexmain.Main(os.Args[1:], os.Args[0], os.Stdin, os.Stdout, os.Stderr, codexmain.Options{}))
}
