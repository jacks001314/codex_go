package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// poolScopeAuthSource is an auth-change source that also names the credential
// scope of a pooled stdio connection, like the router bridge in production.
type poolScopeAuthSource struct {
	state MCPAuthChangeState
	scope string
}

func (s *poolScopeAuthSource) AuthChangeState() MCPAuthChangeState { return s.state }

func (s *poolScopeAuthSource) AuthChanged() <-chan struct{} { return nil }

func (s *poolScopeAuthSource) MCPConnectionAuthScope() string { return s.scope }

// TestMCPStdioPoolKeyIncludesCredentialScopeLikeRust covers Rust #57211's
// ConnectionKey::auth_state: two chats may only join the same pooled helper when
// their credential scope matches.
func TestMCPStdioPoolKeyIncludesCredentialScopeLikeRust(t *testing.T) {
	config := &ServerConfig{Enabled: true, Command: "helper", StartupTimeout: 2 * time.Second}
	base := mcpStdioPoolKey("conn", config, "auth=none")
	if !strings.HasPrefix(base, "conn|sharing|startup=2000000000|catalog=0|") {
		t.Fatalf("pool key = %q, want helper/startup/catalog prefix", base)
	}
	if other := mcpStdioPoolKey("conn", config, "auth=x|owner=1|login=1"); other == base {
		t.Fatal("pool key ignored the credential scope")
	}
}

// TestMCPStdioAuthScopeUsesCredentialSourceLikeRust covers how a service derives
// its pool scope: no source shares freely, an unaware source stays one opaque
// scope, and a bridging source supplies the owner/login revisions.
func TestMCPStdioAuthScopeUsesCredentialSourceLikeRust(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{})
	if got := service.mcpStdioAuthScope(); got != "auth=none" {
		t.Fatalf("scope without a source = %q, want auth=none", got)
	}

	service.SetAuthChangeSource(MCPAuthChangeSourceFunc{
		State: func() MCPAuthChangeState { return MCPAuthChangeState{} },
	})
	if got := service.mcpStdioAuthScope(); got != "auth=opaque" {
		t.Fatalf("scope for an unaware source = %q, want auth=opaque", got)
	}

	service.SetAuthChangeSource(&poolScopeAuthSource{scope: "  auth=a|owner=2|login=3  "})
	if got := service.mcpStdioAuthScope(); got != "auth=a|owner=2|login=3" {
		t.Fatalf("scope = %q, want the trimmed source scope", got)
	}

	service.SetAuthChangeSource(&poolScopeAuthSource{})
	if got := service.mcpStdioAuthScope(); got != "auth=none" {
		t.Fatalf("scope for an empty source scope = %q, want auth=none", got)
	}
}

// TestMCPConnectionSharingStaysPrivateAcrossCredentialSourcesLikeRust covers
// Rust #57211's ConnectionKey::auth_state end to end: a credential refresh
// within one login keeps sharing the helper, while another credential source
// starts its own.
func TestMCPConnectionSharingStaysPrivateAcrossCredentialSourcesLikeRust(t *testing.T) {
	if os.Getenv(mcpSharingHelperEnv) == "1" {
		runMCPSharingHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "starts")
	env := map[string]string{
		mcpSharingHelperEnv:     "1",
		mcpSharingHelperModeEnv: "shared",
		mcpSharingHelperLogEnv:  logPath,
	}
	chatA := newSharingService(executable, env)
	chatA.SetAuthChangeSource(&poolScopeAuthSource{
		state: MCPAuthChangeState{Generation: 1, OwnerGeneration: 1},
		scope: "auth=account-a|owner=1|login=1",
	})
	defer chatA.Close()
	// Same login and owner, newer credential revision: still the same source.
	chatB := newSharingService(executable, env)
	chatB.SetAuthChangeSource(&poolScopeAuthSource{
		state: MCPAuthChangeState{Generation: 7, OwnerGeneration: 1},
		scope: "auth=account-a|owner=1|login=1",
	})
	defer chatB.Close()
	chatC := newSharingService(executable, env)
	chatC.SetAuthChangeSource(&poolScopeAuthSource{
		state: MCPAuthChangeState{OwnerGeneration: 1},
		scope: "auth=account-b|owner=1|login=1",
	})
	defer chatC.Close()

	clientA := sharingClientFor(t, chatA, "shared")
	if got := mcpSharingAsk(t, clientA, "chat-a"); got != "chat-a" {
		t.Fatalf("chat A answer = %q, want chat-a", got)
	}
	clientB := sharingClientFor(t, chatB, "shared")
	if got := mcpSharingAsk(t, clientB, "chat-b"); got != "chat-b" {
		t.Fatalf("chat B answer = %q, want chat-b", got)
	}
	if clientA != clientB {
		t.Fatal("a credential refresh lost its shared stdio connection")
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts != 1 {
		t.Fatalf("helper starts after a credential refresh = %d, want 1", starts)
	}

	clientC := sharingClientFor(t, chatC, "shared")
	if got := mcpSharingAsk(t, clientC, "chat-c"); got != "chat-c" {
		t.Fatalf("chat C answer = %q, want chat-c", got)
	}
	if clientC == clientA {
		t.Fatal("a second credential source joined the first source's connection")
	}
	if starts := mcpSharingLogCount(t, logPath, "start"); starts != 2 {
		t.Fatalf("helper starts after a credential source change = %d, want 2", starts)
	}
}
