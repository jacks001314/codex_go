package app

import (
	"context"
	"strings"

	"codex_go/session"
	"codex_go/state"
	codextui "codex_go/tui"
)

// sessionThreadReadStates reads the durable read receipts the embedded app
// server exposes through thread/list and thread/read (Rust #58263/#58264). It
// is best-effort: an unavailable state database leaves every thread read.
func sessionThreadReadStates(ctx context.Context, store *session.Store, threadIDs []string) map[string]state.ThreadReadState {
	if store == nil || len(threadIDs) == 0 {
		return nil
	}
	codexHome := sessionCodexHome(store)
	if codexHome == "" {
		return nil
	}
	sqliteConfig, err := state.SqliteConfigForCodexHome(codexHome)
	if err != nil {
		return nil
	}
	runtime, err := state.InitStateRuntime(ctx, sqliteConfig, "openai")
	if err != nil {
		return nil
	}
	defer runtime.Close()
	states, err := runtime.ThreadReadStates(ctx, threadIDs)
	if err != nil {
		return nil
	}
	return states
}

// sessionThreadIDs lists the non-empty thread ids in picker order.
func sessionThreadIDs(items []codextui.SessionSummary) []string {
	ids := make([]string, 0, len(items))
	for i := range items {
		if id := strings.TrimSpace(items[i].ThreadID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
