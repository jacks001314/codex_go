package appserver

import (
	"testing"

	"codex_go/mcp"
)

// Mirrors Rust #58395's thread extension data: a host installs one scoped MCP
// request metadata hook per thread, and a thread without an attachment keeps
// the router default (or none at all).
func TestRuntimeRouterThreadMCPToolRequestMetaHookLikeRust(t *testing.T) {
	defaultHook := &mcp.MCPToolRequestMetaHook{ServerName: "default", Key: "x-default"}
	threadHook := &mcp.MCPToolRequestMetaHook{ServerName: "thread", Key: "x-thread"}
	router := &RuntimeRouter{mcpToolRequestMetaDefault: defaultHook}
	if got := router.threadMCPToolRequestMetaHook("thread-a"); got != defaultHook {
		t.Fatalf("unattached thread hook = %#v, want the router default", got)
	}
	router.SetThreadMCPToolRequestMetaHook("thread-a", threadHook)
	if got := router.threadMCPToolRequestMetaHook("thread-a"); got != threadHook {
		t.Fatalf("thread hook = %#v, want the installed hook", got)
	}
	if got := router.threadMCPToolRequestMetaHook("thread-b"); got != defaultHook {
		t.Fatalf("other thread hook = %#v, want the router default", got)
	}
	router.SetThreadMCPToolRequestMetaHook("thread-a", nil)
	if got := router.threadMCPToolRequestMetaHook("thread-a"); got != defaultHook {
		t.Fatalf("hook after removal = %#v, want the router default", got)
	}
	router.SetThreadMCPToolRequestMetaHook("", threadHook)
	if got := router.threadMCPToolRequestMetaHook(""); got != defaultHook {
		t.Fatalf("empty thread hook = %#v, want the router default", got)
	}
}
