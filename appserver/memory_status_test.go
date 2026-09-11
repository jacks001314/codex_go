package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/memories"
)

func v2SummaryForTest() string {
	return "v1\n" +
		"## User Profile\nprofile\n" +
		"## User preferences\nprefs\n" +
		"## General Tips\ntips\n" +
		"## What's in Memory\nmemory\n"
}

func TestMemoryStatusReportsV2Readiness(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})

	// No consolidation progress and no summary: not ready.
	response := router.Handle(requestWithParams(t, IntID(1), MethodMemoryStatus, MemoryStatusParams{}))
	if response.Error != nil {
		t.Fatalf("memory/status error = %#v", response.Error)
	}
	empty := response.Result.(*MemoryStatusResponse)
	if empty.V2ConsolidatedThreads != 0 || empty.V2Ready {
		t.Fatalf("empty memory status = %#v", empty)
	}

	// Enough consolidated threads plus a valid summary: ready.
	if err := memories.RecordConsolidatedThreadCount(home, 20); err != nil {
		t.Fatalf("record progress: %v", err)
	}
	summaryDir := memories.V2Root(home)
	if err := os.MkdirAll(summaryDir, 0o755); err != nil {
		t.Fatalf("mkdir v2 root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(summaryDir, memories.MemorySummaryFilename), []byte(v2SummaryForTest()), 0o600); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	ready := router.Handle(requestWithParams(t, IntID(2), MethodMemoryStatus, MemoryStatusParams{}))
	if ready.Error != nil {
		t.Fatalf("memory/status error = %#v", ready.Error)
	}
	if got := ready.Result.(*MemoryStatusResponse); got.V2ConsolidatedThreads != 20 || !got.V2Ready {
		t.Fatalf("ready memory status = %#v", got)
	}

	// A higher threshold makes the same state not ready.
	high := uint32(21)
	notReady := router.Handle(requestWithParams(t, IntID(3), MethodMemoryStatus, MemoryStatusParams{MinConsolidatedThreads: &high}))
	if notReady.Error != nil {
		t.Fatalf("memory/status error = %#v", notReady.Error)
	}
	if got := notReady.Result.(*MemoryStatusResponse); got.V2Ready {
		t.Fatalf("status with threshold 21 = %#v, want not ready", got)
	}
}

func TestMemoryStatusValidatesThresholdAndExperimentalAPI(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})

	for _, invalid := range []uint32{0, 4097} {
		threshold := invalid
		response := router.Handle(requestWithParams(t, IntID(1), MethodMemoryStatus, MemoryStatusParams{MinConsolidatedThreads: &threshold}))
		if response.Error == nil || response.Error.Code != JSONRPCInvalidParamsErrorCode {
			t.Fatalf("threshold %d error = %#v, want invalid params", invalid, response.Error)
		}
	}

	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	initResponse := router.Handle(requestWithParams(t, IntID(2), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-test", Version: "1.0.0"},
	}))
	if initResponse.Error != nil {
		t.Fatalf("initialize = %+v", initResponse)
	}
	gated := router.Handle(requestWithParams(t, IntID(3), MethodMemoryStatus, MemoryStatusParams{}))
	if gated.Error == nil {
		t.Fatal("memory/status without experimentalApi must be rejected")
	}
}
