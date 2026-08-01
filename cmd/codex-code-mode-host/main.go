package main

import (
	"context"
	"fmt"
	"os"

	"codex_go/codemode"
)

func main() {
	if err := codemode.RunStdioHost(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
