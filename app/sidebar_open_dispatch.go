package app

import (
	"context"
	"errors"
	"strings"
	"sync"

	bubbletea "charm.land/bubbletea/v2"

	codextea "codex_go/tui/tea"
)

// sidebarOpenDispatcher carries `open_in_codex` requests from the task-tools
// MCP server into the running TUI (Rust #63184). The program's message channel
// exists only after the program is created, so the dispatcher waits for it and
// reports a clear error before then.
type sidebarOpenDispatcher struct {
	mu   sync.Mutex
	send func(bubbletea.Msg)
}

// onProgramReady records the program's message channel.
func (d *sidebarOpenDispatcher) onProgramReady(send func(bubbletea.Msg)) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.send = send
	d.mu.Unlock()
}

// open queues one preview for the calling thread. The TUI shows it once that
// conversation is visible, so the tool call returns as soon as the request is
// accepted.
func (d *sidebarOpenDispatcher) open(_ context.Context, threadID string, path string, line int) error {
	if d == nil {
		return errors.New("opening files in the sidebar is unavailable in this session")
	}
	threadID = strings.TrimSpace(threadID)
	path = strings.TrimSpace(path)
	if threadID == "" || path == "" {
		return errors.New("open_in_codex requires a calling thread and a path")
	}
	d.mu.Lock()
	send := d.send
	d.mu.Unlock()
	if send == nil {
		return errors.New("the terminal sidebar is not ready yet; try again shortly")
	}
	send(codextea.SidebarOpenRequestMsg{SourceThreadID: threadID, Path: path, Line: line})
	return nil
}
