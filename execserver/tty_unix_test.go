//go:build !windows

package execserver

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestExecServerTTYUsesRealTerminalAndPTYStreamLikeRust(t *testing.T) {
	server := NewServer()
	if _, err := server.startProcess(context.Background(), &ExecParams{
		ProcessID: "real-tty",
		Argv:      []string{"sh", "-c", `if [ -t 0 ] && [ -t 1 ]; then printf tty; else printf pipe; fi`},
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{},
		TTY:       true,
		PipeStdin: false,
	}); err != nil {
		t.Fatalf("startProcess() error = %v", err)
	}
	waitMS := uint64(3_000)
	response, err := server.readProcess(&ReadParams{ProcessID: "real-tty", WaitMS: &waitMS})
	if err != nil {
		t.Fatalf("readProcess() error = %v", err)
	}
	var output strings.Builder
	for _, chunk := range response.Chunks {
		if chunk.Stream != "pty" {
			t.Fatalf("TTY chunk stream = %q", chunk.Stream)
		}
		data, decodeErr := base64.StdEncoding.DecodeString(chunk.Chunk)
		if decodeErr != nil {
			t.Fatalf("DecodeString() error = %v", decodeErr)
		}
		output.Write(data)
	}
	if !strings.Contains(output.String(), "tty") {
		t.Fatalf("TTY output = %q", output.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response, err = server.readProcess(&ReadParams{ProcessID: "real-tty"})
		if err == nil && response.Closed {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("TTY process did not close")
}
