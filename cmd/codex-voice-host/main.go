package main

import (
	"context"
	"fmt"
	"os"

	"codex_go/processhardening"
	"codex_go/voicehost"
)

// buildCommit is stamped by Bazel/release builders through -ldflags. An
// unstamped local build reports "dev", matching the Rust helper.
var buildCommit = "dev"

func main() {
	processhardening.ApplyPreMain()
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--build-commit" {
		fmt.Println(buildCommit)
		return
	}
	if len(args) != 0 {
		os.Exit(2)
	}
	if err := voicehost.RunHost(context.Background(), os.Stdin, os.Stdout, buildCommit); err != nil {
		os.Exit(1)
	}
}
