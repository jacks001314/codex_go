package appserver

// Like-Rust coverage for `thread/memory/read` (Rust #56465): the summary comes
// from the loaded thread's resolved memory version, with no fallback to the
// other version, and is null when memory use is disabled, the feature is off,
// or the selected summary is missing or blank.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/memories"
	"codex_go/session"
)

func newThreadMemoryReadRouter(t *testing.T, codexHome string) *RuntimeRouter {
	t.Helper()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Config:       config.NewConfigService(codexHome),
	})
	router.SetNotificationSink(NewNotificationBuffer())
	return router
}

func writeMemoryReadConfig(t *testing.T, codexHome, contents string) {
	t.Helper()
	if contents == "" {
		return
	}
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatalf("MkdirAll codex home: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(codexHome), []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
}

func writeMemorySummary(t *testing.T, codexHome string, version config.MemoryVersion, summary string) {
	t.Helper()
	root := memories.RootForVersion(codexHome, version)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", root, err)
	}
	if err := os.WriteFile(filepath.Join(root, memories.MemorySummaryFilename), []byte(summary), 0o600); err != nil {
		t.Fatalf("WriteFile summary: %v", err)
	}
}

func startThreadForMemoryRead(t *testing.T, router *RuntimeRouter, startID int64, params ThreadStartParams) string {
	t.Helper()
	if params.CWD == "" {
		params.CWD = t.TempDir()
	}
	started := router.Handle(requestWithParams(t, IntID(startID), MethodThreadStart, params))
	if started.Error != nil {
		t.Fatalf("thread start error: %+v", started.Error)
	}
	return started.Result.(*ThreadStartResponse).Thread.ID
}

func readThreadMemory(t *testing.T, router *RuntimeRouter, id int64, threadID string) *Response {
	t.Helper()
	return router.Handle(requestWithParams(t, IntID(id), MethodThreadMemoryRead, ThreadMemoryReadParams{ThreadID: threadID}))
}

func requireMemorySummary(t *testing.T, response *Response, want *string) {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("thread/memory/read error: %+v", response.Error)
	}
	result, ok := response.Result.(*ThreadMemoryReadResponse)
	if !ok || result == nil {
		t.Fatalf("result = %#v, want *ThreadMemoryReadResponse", response.Result)
	}
	if want == nil {
		if result.Summary != nil {
			t.Fatalf("summary = %q, want null", *result.Summary)
		}
		return
	}
	if result.Summary == nil {
		t.Fatalf("summary = null, want %q", *want)
	}
	if *result.Summary != *want {
		t.Fatalf("summary = %q, want %q", *result.Summary, *want)
	}
}

func TestThreadMemoryReadReturnsSelectedVersionSummaryLikeRust(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, "codex")
	v1 := "v1\n# Summary\nconventions: tabs\n"
	writeMemoryReadConfig(t, codexHome, "[features]\nmemories = true\n[memories]\nuse_memories = true\n")
	writeMemorySummary(t, codexHome, config.MemoryVersionV1, v1)
	writeMemorySummary(t, codexHome, config.MemoryVersionV2, "v1\n# v2 summary\n")

	router := newThreadMemoryReadRouter(t, codexHome)
	threadID := startThreadForMemoryRead(t, router, 1, ThreadStartParams{})
	// The raw summary text is returned, not a trimmed copy.
	requireMemorySummary(t, readThreadMemory(t, router, 2, threadID), &v1)
}

func TestThreadMemoryReadHonorsThreadVersionWithoutFallbackLikeRust(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, "codex")
	writeMemoryReadConfig(t, codexHome, "[features]\nmemories = true\n[memories]\nversion = \"v1\"\nuse_memories = true\n")
	writeMemorySummary(t, codexHome, config.MemoryVersionV1, "v1\n# v1 summary\n")
	v2 := "v1\n# v2 summary\n"
	writeMemorySummary(t, codexHome, config.MemoryVersionV2, v2)

	router := newThreadMemoryReadRouter(t, codexHome)
	threadID := startThreadForMemoryRead(t, router, 1, ThreadStartParams{
		Config: map[string]any{"memories": map[string]any{"version": "v2"}},
	})
	requireMemorySummary(t, readThreadMemory(t, router, 2, threadID), &v2)

	// A v2-selected thread does not fall back to the v1 summary.
	missingHome := filepath.Join(t.TempDir(), "codex")
	writeMemoryReadConfig(t, missingHome, "[features]\nmemories = true\n[memories]\nuse_memories = true\n")
	writeMemorySummary(t, missingHome, config.MemoryVersionV1, "v1\n# v1 summary\n")
	v2OnlyRouter := newThreadMemoryReadRouter(t, missingHome)
	v2OnlyThread := startThreadForMemoryRead(t, v2OnlyRouter, 3, ThreadStartParams{
		Config: map[string]any{"memories": map[string]any{"version": "v2"}},
	})
	requireMemorySummary(t, readThreadMemory(t, v2OnlyRouter, 4, v2OnlyThread), nil)
}

func TestThreadMemoryReadGatesLikeRust(t *testing.T) {
	cases := []struct {
		name       string
		configTOML string
		summary    string
	}{
		{name: "feature-disabled", configTOML: "[memories]\nuse_memories = true\n", summary: "v1\n# summary\n"},
		{name: "use-memories-false", configTOML: "[features]\nmemories = true\n[memories]\nuse_memories = false\n", summary: "v1\n# summary\n"},
		{name: "summary-missing", configTOML: "[features]\nmemories = true\n[memories]\nuse_memories = true\n"},
		{name: "summary-blank", configTOML: "[features]\nmemories = true\n[memories]\nuse_memories = true\n", summary: "  \n\t\n"},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codexHome := filepath.Join(t.TempDir(), "codex")
			writeMemoryReadConfig(t, codexHome, tc.configTOML)
			if tc.summary != "" {
				writeMemorySummary(t, codexHome, config.MemoryVersionV1, tc.summary)
			}
			router := newThreadMemoryReadRouter(t, codexHome)
			threadID := startThreadForMemoryRead(t, router, int64(index*2+1), ThreadStartParams{})
			requireMemorySummary(t, readThreadMemory(t, router, int64(index*2+2), threadID), nil)
		})
	}
}

func TestThreadMemoryReadRequiresLoadedThreadLikeRust(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "codex")
	writeMemoryReadConfig(t, codexHome, "[features]\nmemories = true\n[memories]\nuse_memories = true\n")
	writeMemorySummary(t, codexHome, config.MemoryVersionV1, "v1\n# summary\n")
	router := newThreadMemoryReadRouter(t, codexHome)

	response := readThreadMemory(t, router, 1, "missing-thread")
	if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "thread not found: missing-thread" {
		t.Fatalf("missing thread response = %+v, want -32600 thread not found", response.Error)
	}

	// A persisted thread that was never started or resumed in this runtime has
	// no loaded conversation handle in Rust either.
	store := router.services.ThreadRouter.store
	if err := store.Create(&session.Record{ID: "persisted-thread", SessionID: "persisted-thread"}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	response = readThreadMemory(t, router, 2, "persisted-thread")
	if response.Error == nil || response.Error.Code != -32600 || response.Error.Message != "thread not found: persisted-thread" {
		t.Fatalf("persisted thread response = %+v, want -32600 thread not found", response.Error)
	}
}

func TestThreadMemoryReadParamsRequireThreadID(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadMemoryRead, map[string]any{}))
	if response.Error == nil || response.Error.Code != -32602 || !strings.Contains(response.Error.Message, "threadId is required") {
		t.Fatalf("missing threadId response = %+v, want invalid params", response.Error)
	}
}
