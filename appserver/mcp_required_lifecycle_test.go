package appserver

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
)

func TestRuntimeRouterRequiredMCPThreadStartRollsBackInitialization(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ephemeral bool
	}{
		{name: "persisted"},
		{name: "ephemeral", ephemeral: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := session.NewStore(t.TempDir())
			threadRouter := NewRouter(store)
			sink := NewNotificationBuffer()
			router := NewRuntimeRouter(RuntimeServices{
				ThreadRouter: threadRouter,
				Config:       config.NewConfigService(t.TempDir()),
			})
			router.SetNotificationSink(sink)
			t.Cleanup(func() { _ = router.Close() })

			response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
				CWD:         t.TempDir(),
				Ephemeral:   tc.ephemeral,
				HistoryMode: ThreadHistoryPaginated,
				Config:      requiredBrokenMCPConfig(),
			}))
			assertRequiredMCPInitializationError(t, response, "required_broken")
			if sinkHasMethod(sink, NotificationThreadStarted) {
				t.Fatalf("thread/started emitted after failed initialization: %+v", sink.List())
			}
			if loaded := router.requireThreadStatus().LoadedThreadIDs(); len(loaded) != 0 {
				t.Fatalf("loaded threads after failed initialization = %#v", loaded)
			}
			if records, err := store.AllRecords(); err != nil {
				t.Fatalf("AllRecords() error = %v", err)
			} else if len(records) != 0 {
				t.Fatalf("persisted records after failed initialization = %#v", records)
			}
			assertNoThreadInitializationResources(t, router)
		})
	}
}

func TestRuntimeRouterRequiredMCPColdResumeRollsBackWithoutChangingRecord(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	now := fixedTime()
	record := &session.Record{
		ID:        "thread-required-resume",
		SessionID: "thread-required-resume",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:         t.TempDir(),
			HistoryMode: string(ThreadHistoryPaginated),
			Extra: map[string]any{
				"config": map[string]any{"existing": "preserved"},
			},
		},
		Items: []session.Item{{
			ID: "user-1", Type: "message", Role: "user", Text: "existing history", CreatedAt: now,
			Metadata: map[string]any{"turnId": "turn-1"},
		}},
	}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := threadRouter.createThreadRollout(record, now); err != nil {
		t.Fatalf("createThreadRollout() error = %v", err)
	}
	before, err := store.Read(record.ID, true, true)
	if err != nil {
		t.Fatalf("Read(before) error = %v", err)
	}

	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: threadRouter,
		Config:       config.NewConfigService(t.TempDir()),
	})
	router.SetNotificationSink(sink)
	t.Cleanup(func() { _ = router.Close() })

	newCWD := t.TempDir()
	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
		ThreadID: string(record.ID),
		CWD:      &newCWD,
		Config:   requiredBrokenMCPConfig(),
	}))
	assertRequiredMCPInitializationError(t, response, "required_broken")
	if loaded := router.requireThreadStatus().LoadedThreadIDs(); len(loaded) != 0 {
		t.Fatalf("loaded threads after failed resume = %#v", loaded)
	}
	if sinkHasMethod(sink, NotificationThreadStarted) || sinkHasMethod(sink, NotificationThreadSettingsUpdated) {
		t.Fatalf("lifecycle/settings notification emitted after failed resume: %+v", sink.List())
	}
	after, err := store.Read(record.ID, true, true)
	if err != nil {
		t.Fatalf("historical record was removed after failed resume: %v", err)
	}
	if !reflect.DeepEqual(before.Metadata, after.Metadata) {
		t.Fatalf("record metadata changed after failed resume\nbefore=%#v\nafter=%#v", before.Metadata, after.Metadata)
	}
	assertNoThreadInitializationResources(t, router)
}

func TestRuntimeRouterRequiredMCPHistoryResumeDeletesNewThreadArtifacts(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: threadRouter,
		Config:       config.NewConfigService(t.TempDir()),
	})
	router.SetNotificationSink(sink)
	t.Cleanup(func() { _ = router.Close() })

	response := router.Handle(requestWithParams(t, IntID(9), MethodThreadResume, ThreadResumeParams{
		ThreadID: "ignored-history-thread",
		History: []ThreadResumeHistoryItem{
			ThreadResumeHistoryItem(json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"history resume"}]}`)),
		},
		Config: requiredBrokenMCPConfig(),
	}))
	assertRequiredMCPInitializationError(t, response, "required_broken")
	if records, err := store.AllRecords(); err != nil {
		t.Fatalf("AllRecords() error = %v", err)
	} else if len(records) != 0 {
		t.Fatalf("history-resume records after failed initialization = %#v", records)
	}
	if page, err := rolloutListForRequiredMCPTest(store); err != nil {
		t.Fatalf("rollout list error = %v", err)
	} else if len(page) != 0 {
		t.Fatalf("history-resume rollouts after failed initialization = %#v", page)
	}
	if sinkHasMethod(sink, NotificationThreadStarted) || len(router.requireThreadStatus().LoadedThreadIDs()) != 0 {
		t.Fatalf("history-resume was loaded/notified after failure: loaded=%#v notifications=%+v", router.requireThreadStatus().LoadedThreadIDs(), sink.List())
	}
	assertNoThreadInitializationResources(t, router)
}

func TestRuntimeRouterOptionalBrokenMCPDoesNotBlockThreadStartOrColdResume(t *testing.T) {
	t.Run("start", func(t *testing.T) {
		store := session.NewStore(t.TempDir())
		router := NewRuntimeRouter(RuntimeServices{
			ThreadRouter: NewRouter(store),
			Config:       config.NewConfigService(t.TempDir()),
		})
		t.Cleanup(func() { _ = router.Close() })
		response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
			CWD:    t.TempDir(),
			Config: optionalBrokenMCPConfig(),
		}))
		if response.Error != nil {
			t.Fatalf("optional MCP blocked thread/start: %+v", response.Error)
		}
		started := response.Result.(*ThreadStartResponse)
		if started.Thread == nil || !router.threadIsLoaded(started.Thread.ID) {
			t.Fatalf("started thread was not loaded: %+v", started.Thread)
		}
	})

	t.Run("cold resume", func(t *testing.T) {
		store := session.NewStore(t.TempDir())
		threadRouter := NewRouter(store)
		now := fixedTime()
		record := &session.Record{
			ID:        "thread-optional-resume",
			SessionID: "thread-optional-resume",
			CreatedAt: now,
			UpdatedAt: now,
			RecencyAt: now,
			Metadata:  session.Metadata{CWD: t.TempDir(), HistoryMode: string(ThreadHistoryPaginated)},
			Items: []session.Item{{
				ID: "user-1", Type: "message", Role: "user", Text: "existing history", CreatedAt: now,
				Metadata: map[string]any{"turnId": "turn-1"},
			}},
		}
		if err := store.Save(record); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		if err := threadRouter.createThreadRollout(record, now); err != nil {
			t.Fatalf("createThreadRollout() error = %v", err)
		}
		router := NewRuntimeRouter(RuntimeServices{
			ThreadRouter: threadRouter,
			Config:       config.NewConfigService(t.TempDir()),
		})
		t.Cleanup(func() { _ = router.Close() })
		response := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
			ThreadID: string(record.ID),
			Config:   optionalBrokenMCPConfig(),
		}))
		if response.Error != nil {
			t.Fatalf("optional MCP blocked thread/resume: %+v", response.Error)
		}
		resumed := response.Result.(*ThreadResumeResponse)
		if resumed.Thread == nil || !router.threadIsLoaded(resumed.Thread.ID) {
			t.Fatalf("resumed thread was not loaded: %+v", resumed.Thread)
		}
		persisted, err := store.Read(record.ID, true, false)
		if err != nil {
			t.Fatalf("Read(resumed) error = %v", err)
		}
		if configSnapshot := threadRecordConfigOverrides(persisted); configSnapshot["mcp_servers"] == nil {
			t.Fatalf("resume request config was not persisted: %#v", configSnapshot)
		}
	})
}

func TestRuntimeRouterThreadStartPrewarmReusesValidatedMCPBinding(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(t.TempDir()),
	})
	t.Cleanup(func() { _ = router.Close() })
	autoReview := "auto_review"
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:               t.TempDir(),
		ApprovalPolicy:    "on-request",
		ApprovalsReviewer: &autoReview,
		Sandbox:           "workspace-write",
		Config:            optionalBrokenMCPConfig(),
	}))
	if response.Error != nil {
		t.Fatalf("thread/start error = %+v", response.Error)
	}
	threadID := response.Result.(*ThreadStartResponse).Thread.ID
	service, revision := currentMCPBindingForTest(t, router, threadID)
	waitForMCPPrewarmIdle(t, router, threadID)
	afterService, afterRevision := currentMCPBindingForTest(t, router, threadID)
	if afterService != service || afterRevision != revision {
		t.Fatalf("prewarm republished validated binding: service %p/%p revision %d/%d", service, afterService, revision, afterRevision)
	}
	record, err := store.Read(session.ThreadID(threadID), true, false)
	if err != nil {
		t.Fatalf("Read(started) error = %v", err)
	}
	snapshot := threadRecordConfigOverrides(record)
	for key, want := range map[string]any{
		"approval_policy":    "on-request",
		"approvals_reviewer": autoReview,
		"sandbox_policy":     "workspace-write",
	} {
		if !reflect.DeepEqual(snapshot[key], want) {
			t.Fatalf("config snapshot[%q] = %#v, want %#v; snapshot=%#v", key, snapshot[key], want, snapshot)
		}
	}
}

func TestRuntimeRouterLoadedResumeDoesNotReinitializeRequiredMCP(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	now := fixedTime()
	record := &session.Record{
		ID:        "thread-loaded-resume",
		SessionID: "thread-loaded-resume",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: t.TempDir(), HistoryMode: string(ThreadHistoryPaginated)},
		Items: []session.Item{{
			ID: "user-1", Type: "message", Role: "user", Text: "existing history", CreatedAt: now,
			Metadata: map[string]any{"turnId": "turn-1"},
		}},
	}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := threadRouter.createThreadRollout(record, now); err != nil {
		t.Fatalf("createThreadRollout() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: threadRouter,
		Config:       config.NewConfigService(t.TempDir()),
	})
	t.Cleanup(func() { _ = router.Close() })
	first := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID: string(record.ID),
		Config:   optionalBrokenMCPConfig(),
	}))
	if first.Error != nil {
		t.Fatalf("cold thread/resume error = %+v", first.Error)
	}
	service, revision := currentMCPBindingForTest(t, router, string(record.ID))
	second := router.Handle(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
		ThreadID: string(record.ID),
		Config:   requiredBrokenMCPConfig(),
	}))
	if second.Error != nil {
		t.Fatalf("loaded thread/resume reinitialized required MCP: %+v", second.Error)
	}
	afterService, afterRevision := currentMCPBindingForTest(t, router, string(record.ID))
	if afterService != service || afterRevision != revision {
		t.Fatalf("loaded resume replaced MCP binding: service %p/%p revision %d/%d", service, afterService, revision, afterRevision)
	}
}

func requiredBrokenMCPConfig() map[string]any {
	return map[string]any{
		"mcp_servers": map[string]any{
			"required_broken": map[string]any{
				"command":  "codex-required-mcp-does-not-exist",
				"enabled":  true,
				"required": true,
			},
		},
	}
}

func optionalBrokenMCPConfig() map[string]any {
	return map[string]any{
		"mcp_servers": map[string]any{
			"optional_broken": map[string]any{
				"command": "codex-optional-mcp-does-not-exist",
				"enabled": true,
			},
		},
	}
}

func assertRequiredMCPInitializationError(t *testing.T, response *Response, serverName string) {
	t.Helper()
	if response == nil || response.Error == nil {
		t.Fatalf("response = %+v, want required MCP initialization error", response)
	}
	if !strings.Contains(response.Error.Message, "required MCP servers failed to initialize") || !strings.Contains(response.Error.Message, serverName) {
		t.Fatalf("error = %+v, want required MCP aggregate containing %q", response.Error, serverName)
	}
}

func assertNoThreadInitializationResources(t *testing.T, router *RuntimeRouter) {
	t.Helper()
	if router == nil {
		t.Fatal("router is nil")
	}
	if liveThreads := router.threads.LiveThreadCount(); liveThreads != 0 {
		t.Fatalf("retained live threads after failed initialization = %d", liveThreads)
	}
	router.threads.ephemeralMu.RLock()
	ephemeral := len(router.threads.ephemeral)
	router.threads.ephemeralMu.RUnlock()
	if ephemeral != 0 {
		t.Fatalf("ephemeral records after failed initialization = %d", ephemeral)
	}
	if router.mcpRuntimes != nil {
		router.mcpRuntimes.mu.Lock()
		bindings := len(router.mcpRuntimes.bindings)
		router.mcpRuntimes.mu.Unlock()
		if bindings != 0 {
			t.Fatalf("MCP bindings after failed initialization = %d", bindings)
		}
	}
}

func currentMCPBindingForTest(t *testing.T, router *RuntimeRouter, threadID string) (any, uint64) {
	t.Helper()
	if router == nil || router.mcpRuntimes == nil {
		t.Fatal("MCP runtime coordinator is nil")
	}
	router.mcpRuntimes.mu.Lock()
	defer router.mcpRuntimes.mu.Unlock()
	binding := router.mcpRuntimes.bindings[threadID]
	if binding == nil || binding.service == nil {
		t.Fatalf("MCP binding for %s is missing", threadID)
	}
	return binding.service, binding.revision
}

func waitForMCPPrewarmIdle(t *testing.T, router *RuntimeRouter, threadID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		router.mcpRuntimes.mu.Lock()
		busy := router.mcpRuntimes.prewarming[threadID] || router.mcpRuntimes.prewarmPending[threadID]
		router.mcpRuntimes.mu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("MCP prewarm for %s did not become idle", threadID)
		}
		time.Sleep(time.Millisecond)
	}
}

func rolloutListForRequiredMCPTest(store *session.Store) ([]string, error) {
	if store == nil {
		return nil, nil
	}
	root := store.Root()
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry != nil && !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}
