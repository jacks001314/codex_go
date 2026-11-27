package appserver

import (
	"testing"
	"time"

	"codex_go/session"
)

func TestSessionEndRunsForRootThreadOnlyLikeRust(t *testing.T) {
	cwd := t.TempDir()
	hooks := NewHookRegistry()
	if err := hooks.Add(cwd, hookRunnerMetadata("session-end-root", HookEventSessionEnd, "", 0)); err != nil {
		t.Fatal(err)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Hooks: hooks, HookRunner: NewHookRunner()})
	router.SetNotificationSink(sink)
	now := time.Now().UTC()
	records := []*session.Record{
		{ID: "root", SessionID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: cwd, Source: "cli"}},
		{ID: "hosted-child", SessionID: "root", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: cwd, ThreadSource: "subagent", Source: "subagent_review"}},
		{ID: "spawned-child", SessionID: "spawned-child", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now, Metadata: session.Metadata{CWD: cwd, Source: "subagent_thread_spawn"}},
	}
	for _, record := range records {
		router.runSessionEndHookOnce(record, "other")
	}
	started := 0
	completed := 0
	for _, notification := range sink.List() {
		switch notification.Method {
		case NotificationHookStarted:
			started++
		case NotificationHookCompleted:
			completed++
		}
	}
	if started != 1 || completed != 1 {
		t.Fatalf("SessionEnd notifications started=%d completed=%d, want one root run", started, completed)
	}
}
