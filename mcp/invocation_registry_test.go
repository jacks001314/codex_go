package mcp

import (
	"fmt"
	"sync"
	"testing"
)

func TestMCPInvocationRegistryTracksLiveCalls(t *testing.T) {
	service := NewMCPService(nil)
	release := service.RegisterToolInvocation(" codex_apps ", " call-1 ", MCPInvocationMetadata{
		ServerName:  "codex_apps",
		ToolName:    "_js",
		ConnectorID: BrowserConnectorID,
		Arguments:   map[string]any{"code": "write_record()"},
	})
	live, ok := service.LiveToolInvocation("codex_apps", "call-1")
	if !ok {
		t.Fatal("LiveToolInvocation() = missing, want the registered call")
	}
	if live.ToolName != "_js" || live.ConnectorID != BrowserConnectorID {
		t.Fatalf("LiveToolInvocation() = %#v", live)
	}
	if _, ok := service.LiveToolInvocation("codex_apps", ""); ok {
		t.Fatal("an empty call id must not resolve a live invocation")
	}
	if _, ok := service.LiveToolInvocation("node_repl", "call-1"); ok {
		t.Fatal("a different server must not resolve the live invocation")
	}
	release()
	if _, ok := service.LiveToolInvocation("codex_apps", "call-1"); ok {
		t.Fatal("a released invocation is still live")
	}
	// Releasing twice is a no-op, so deferred and explicit releases can coexist.
	release()
}

func TestMCPInvocationRegistryConcurrentAccess(t *testing.T) {
	service := NewMCPService(nil)
	var wg sync.WaitGroup
	for index := 0; index < 32; index++ {
		callID := fmt.Sprintf("call-%d", index)
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := service.RegisterToolInvocation("codex_apps", callID, MCPInvocationMetadata{ServerName: "codex_apps", ToolName: "_js"})
			if _, ok := service.LiveToolInvocation("codex_apps", callID); !ok {
				t.Error("LiveToolInvocation() = missing during concurrent registration")
			}
			release()
		}()
	}
	wg.Wait()
	for index := 0; index < 32; index++ {
		if _, ok := service.LiveToolInvocation("codex_apps", fmt.Sprintf("call-%d", index)); ok {
			t.Fatal("the registry retained an invocation after every release")
		}
	}
}

func TestNilMCPServiceInvocationCallsAreNoOps(t *testing.T) {
	var service *MCPService
	release := service.RegisterToolInvocation("codex_apps", "call-1", MCPInvocationMetadata{})
	release()
	if _, ok := service.LiveToolInvocation("codex_apps", "call-1"); ok {
		t.Fatal("a nil service reported a live invocation")
	}
}
