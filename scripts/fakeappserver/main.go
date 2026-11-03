// Command fakeappserver runs the deterministic app-server stand-in used by the
// Linux real-terminal fork/resume tests.
//
//	go run ./scripts/fakeappserver -socket /tmp/fs.sock -log /tmp/fs.log -forkdelay 2s
//
// It prints the socket path and the thread id it serves on stdout, then serves
// until it is interrupted. Point a real TUI at it with:
//
//	codex --remote unix:///tmp/fs.sock
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"codex_go/internal/fakeappserver"
)

func main() {
	socket := flag.String("socket", "/tmp/fakeappserver.sock", "unix socket path to listen on")
	logPath := flag.String("log", "/tmp/fakeappserver.log", "request log path")
	forkDelay := flag.Duration("forkdelay", 0, "delay before answering thread/fork (e.g. 2s)")
	forkFail := flag.Bool("forkfail", false, "answer thread/fork with an error")
	forkClose := flag.Bool("forkclose", false, "drop the connection instead of answering thread/fork")
	readOnly := flag.Bool("readonly", true, "answer thread/resume with the external-writer error (locked view)")
	threadID := flag.String("threadid", "", "thread id served by thread/list (default 01910000-0000-7000-8000-000000000001)")
	forkID := flag.String("forkid", "", "thread id returned by thread/fork (default 01910000-0000-7000-8000-000000000002)")
	flag.Parse()

	server, err := fakeappserver.New(fakeappserver.Options{
		SocketPath: *socket,
		LogPath:    *logPath,
		ForkDelay:  *forkDelay,
		ForkFail:   *forkFail,
		ForkClose:  *forkClose,
		ReadOnly:   *readOnly,
		ThreadID:   *threadID,
		ForkID:     *forkID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeappserver: %v\n", err)
		os.Exit(1)
	}
	defer server.Close()
	fmt.Printf("socket=%s\n", server.SocketPath())
	fmt.Printf("log=%s\n", *logPath)
	fmt.Printf("forkdelay=%s\n", *forkDelay)
	if *forkFail {
		fmt.Printf("forkfail=true\n")
	}
	if *forkClose {
		fmt.Printf("forkclose=true\n")
	}
	fmt.Printf("readonly=%v\n", *readOnly)
	os.Stdout.Sync()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	// Give the deferred Close a moment to unlink the socket before exiting.
	time.Sleep(10 * time.Millisecond)
}
