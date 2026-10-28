package appserver

import (
	"strings"
	"testing"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

func dynamicToolForAppServerTest(name string) turn.DynamicToolSpec {
	return turn.DynamicToolSpec{
		Type: "function",
		Function: &turn.DynamicToolFunctionSpec{
			Name:        name,
			Description: "Check " + name + ".",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

// dynamicToolNameForTest accepts both the nested and flattened shapes the
// record round-trip can produce.
func dynamicToolNameForTest(spec turn.DynamicToolSpec) string {
	if spec.Function != nil && spec.Function.Name != "" {
		return spec.Function.Name
	}
	if spec.Namespace != nil && spec.Namespace.Name != "" {
		return spec.Namespace.Name
	}
	return spec.Name
}

func TestReplaceThreadDynamicToolsRequiresHostOptInLikeRust(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, managedProviderRequirements)
	router := newManagedProviderRouter(t, home)
	threadID := startManagedProviderThread(t, router)

	err := router.ReplaceThreadDynamicTools(threadID, []turn.DynamicToolSpec{dynamicToolForAppServerTest("release")})
	if err == nil || !strings.Contains(err.Error(), "The host has disabled dynamic tool updates for persistent threads.") {
		t.Fatalf("ReplaceThreadDynamicTools() error = %v", err)
	}
	record, recordErr := router.threadRecord(session.ThreadID(threadID), true, false)
	if recordErr != nil {
		t.Fatalf("threadRecord() error = %v", recordErr)
	}
	if len(record.Metadata.DynamicTools) != 0 {
		t.Fatalf("catalog changed without opt-in: %s", record.Metadata.DynamicTools)
	}
}

func TestReplaceThreadDynamicToolsPersistsAndClearsLikeRust(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, managedProviderRequirements)
	router := newManagedProviderRouter(t, home)
	router.services.AllowPersistentDynamicToolUpdates = true
	threadID := startManagedProviderThread(t, router)

	if err := router.ReplaceThreadDynamicTools(threadID, []turn.DynamicToolSpec{dynamicToolForAppServerTest("release")}); err != nil {
		t.Fatalf("ReplaceThreadDynamicTools() error = %v", err)
	}
	record, err := router.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil {
		t.Fatalf("threadRecord() error = %v", err)
	}
	if len(record.Metadata.DynamicTools) != 1 || !strings.Contains(string(record.Metadata.DynamicTools[0]), `"release"`) {
		t.Fatalf("record dynamic tools = %s", record.Metadata.DynamicTools)
	}

	// The catalog applies to later turns without a caller-supplied override.
	params := &turn.TurnStartParams{ThreadID: threadID}
	if err := router.prepareTurnStartParams(params); err != nil {
		t.Fatalf("prepareTurnStartParams() error = %v", err)
	}
	if len(params.DynamicTools) != 1 || dynamicToolNameForTest(params.DynamicTools[0]) != "release" {
		t.Fatalf("turn params dynamic tools = %#v", params.DynamicTools)
	}

	if err := router.ReplaceThreadDynamicTools(threadID, nil); err != nil {
		t.Fatalf("ReplaceThreadDynamicTools(clear) error = %v", err)
	}
	record, err = router.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil {
		t.Fatalf("threadRecord() after clear error = %v", err)
	}
	if len(record.Metadata.DynamicTools) != 0 {
		t.Fatalf("record dynamic tools after clear = %s", record.Metadata.DynamicTools)
	}
	path, err := router.services.ThreadRouter.findThreadRolloutPath(session.ThreadID(threadID), false)
	if err != nil {
		t.Fatalf("findThreadRolloutPath() error = %v", err)
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("rollout.Load() error = %v", err)
	}
	tools, ok := rollout.LatestDynamicTools(lines)
	if !ok || len(tools) != 0 {
		t.Fatalf("persisted latest catalog = %#v, %t", tools, ok)
	}
}

func TestReplaceThreadDynamicToolsEphemeralSkipsOptInLikeRust(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, managedProviderRequirements)
	router := newManagedProviderRouter(t, home)
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{Ephemeral: true}))
	if response.Error != nil {
		t.Fatalf("thread/start error = %+v", response.Error)
	}
	started, ok := response.Result.(*ThreadStartResponse)
	if !ok || started.Thread == nil {
		t.Fatalf("thread/start result = %#v", response.Result)
	}
	if record, ok := router.ephemeralThreadRecord(session.ThreadID(started.Thread.ID), false); !ok || record == nil {
		t.Fatalf("ephemeral record missing after thread/start: %s (ephemeral=%t)", started.Thread.ID, started.Thread.Ephemeral)
	}
	if err := router.ReplaceThreadDynamicTools(started.Thread.ID, []turn.DynamicToolSpec{dynamicToolForAppServerTest("release")}); err != nil {
		t.Fatalf("ReplaceThreadDynamicTools() error = %v", err)
	}
	record, err := router.threadRecord(session.ThreadID(started.Thread.ID), true, false)
	if err != nil {
		t.Fatalf("threadRecord() error = %v", err)
	}
	if len(record.Metadata.DynamicTools) != 1 {
		t.Fatalf("ephemeral record dynamic tools = %s", record.Metadata.DynamicTools)
	}
}
