package appserver

import (
	"testing"

	"codex_go/session"
)

func TestThreadAllowedToolsStartAndResumeShapeLikeRust(t *testing.T) {
	empty := []string{}
	extra := threadStartExtra(&ThreadStartParams{AllowedTools: &empty})
	if values, ok := extra["allowed_tools"].([]string); !ok || len(values) != 0 {
		t.Fatalf("empty allowed_tools extra = %#v", extra["allowed_tools"])
	}
	record := &session.Record{Metadata: session.Metadata{Extra: extra}}
	allowed := threadAllowedToolsFromRecord(record)
	if allowed == nil || len(*allowed) != 0 {
		t.Fatalf("empty allowed tools round trip = %#v", allowed)
	}

	selected := []string{"exec", "wait", "exec"}
	extra = threadStartExtra(&ThreadStartParams{AllowedTools: &selected})
	record.Metadata.Extra = extra
	allowed = threadAllowedToolsFromRecord(record)
	if allowed == nil || len(*allowed) != 2 || (*allowed)[0] != "exec" || (*allowed)[1] != "wait" {
		t.Fatalf("selected allowed tools = %#v", allowed)
	}

	record.Metadata.Extra = map[string]any{"allowed_tools": []string{"old"}}
	applyThreadAllowedToolsExtra(record.Metadata.Extra, nil)
	if threadAllowedToolsFromRecord(record) != nil {
		t.Fatal("omitted resume allowlist must remove the captured restriction")
	}
}
