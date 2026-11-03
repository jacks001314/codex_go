package agentboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"codex_go/agent"
)

// Rust #58986: the host selects the backend and the extension opens it with the
// supplied host capabilities.
func TestBoardBackendSelectsConfiguredStorageLikeRust(t *testing.T) {
	ctx := context.Background()
	host := newTestHost(t, map[string]agent.AgentPath{"thread-1": agent.AgentPathRoot})
	registry := &InMemoryMessageBoards{}
	first, err := OpenBoardBackend(ctx, BoardBackend{Kind: BoardBackendInMemory, InMemory: registry}, "tree", host)
	if err != nil {
		t.Fatalf("OpenBoardBackend(in-memory) error = %v", err)
	}
	second, err := OpenBoardBackend(ctx, BoardBackend{Kind: BoardBackendInMemory, InMemory: registry}, "tree", host)
	if err != nil {
		t.Fatalf("OpenBoardBackend(in-memory) error = %v", err)
	}
	if _, err := first.CreateChannel(ctx, "thread-1", CreateChannelRequest{ChannelName: "general"}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	page, err := second.ListChannels(ctx, "thread-1", ChannelQuery{})
	if err != nil || page == nil || len(page.Results) != 1 {
		t.Fatalf("second handle did not share the tree board: %#v, %v", page, err)
	}
	if _, err := OpenBoardBackend(ctx, BoardBackend{Kind: "unknown"}, "tree", host); !errors.Is(err, ErrBoardBackendUnknown) {
		t.Fatalf("unknown backend error = %v", err)
	}
}

// Rust #58986: remote credential handling moves with the backend.
func TestRemoteBoardBackendResolvesCredentialsLikeRust(t *testing.T) {
	ctx := context.Background()
	host := newTestHost(t, map[string]agent.AgentPath{"thread-1": agent.AgentPathRoot})
	envName := "CODEX_TEST_MESSAGE_BOARD_TOKEN"
	t.Setenv(envName, "")
	missing := BoardBackend{Kind: BoardBackendRemote, Remote: &RemoteBoardBackend{
		Endpoint: "https://board.example.test", BearerTokenEnvVar: &envName, HTTP: http.DefaultClient,
	}}
	if _, err := OpenBoardBackend(ctx, missing, "tree", host); !errors.Is(err, ErrRemoteBoardCredentialEnv) {
		t.Fatalf("missing env error = %v", err)
	}
	noCredential := BoardBackend{Kind: BoardBackendRemote, Remote: &RemoteBoardBackend{
		Endpoint: "https://board.example.test", HTTP: http.DefaultClient,
	}}
	if _, err := OpenBoardBackend(ctx, noCredential, "tree", host); !errors.Is(err, ErrRemoteBoardMissingCredential) {
		t.Fatalf("missing credential error = %v", err)
	}
	short := "short"
	invalid := BoardBackend{Kind: BoardBackendRemote, Remote: &RemoteBoardBackend{
		Endpoint: "https://board.example.test", BearerToken: &short, HTTP: http.DefaultClient,
	}}
	if _, err := OpenBoardBackend(ctx, invalid, "tree", host); err == nil || !strings.Contains(err.Error(), "32-4096") {
		t.Fatalf("short credential error = %v", err)
	}
	token := strings.Repeat("a", 32)
	valid := BoardBackend{Kind: BoardBackendRemote, Remote: &RemoteBoardBackend{
		Endpoint: "https://board.example.test", BearerToken: &token, HTTP: http.DefaultClient,
	}}
	if _, err := OpenBoardBackend(ctx, valid, "tree", host); err != nil {
		t.Fatalf("valid remote backend error = %v", err)
	}
}
