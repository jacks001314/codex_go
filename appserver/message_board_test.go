package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/agentboard"
	"codex_go/config"
	"codex_go/session"
	"codex_go/state"
	"codex_go/turn"
)

// Rust parity: the local thread store's `thread_data_cleanup` callback deletes
// the boards owned by the permanently removed thread roots, and a subagent's ID
// does not match its parent's board.
type stubMessageBoardHost struct {
	members map[string]agent.AgentPath
}

func (h stubMessageBoardHost) AgentPath(_ context.Context, caller string) (agent.AgentPath, error) {
	path, ok := h.members[caller]
	if !ok {
		return "", fmt.Errorf("unknown agent")
	}
	return path, nil
}

func (h stubMessageBoardHost) ResolveAgent(_ context.Context, path agent.AgentPath) (string, error) {
	for id, member := range h.members {
		if member == path {
			return id, nil
		}
	}
	return "", fmt.Errorf("unknown agent")
}

func (h stubMessageBoardHost) CurrentTime(context.Context, string) (time.Time, error) {
	return time.Now().UTC(), nil
}

func (h stubMessageBoardHost) Notify(context.Context, string, agentboard.PostPreview) (agentboard.NotificationDelivery, error) {
	return agentboard.NotificationAccepted, nil
}

func TestThreadDeleteRemovesOwnedMessageBoardsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	rootID := "root-thread"
	childID := "child-thread"
	for _, record := range []*session.Record{
		{ID: session.ThreadID(rootID), SessionID: rootID, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(),
			Metadata: session.Metadata{CWD: home, AgentPath: "/root"}},
		{ID: session.ThreadID(childID), SessionID: rootID, ParentThreadID: session.ThreadID(rootID),
			CreatedAt: time.Unix(2, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(),
			Metadata: session.Metadata{CWD: home, AgentPath: "/root/worker", AgentDepth: 1}},
	} {
		if err := store.Save(record); err != nil {
			t.Fatalf("save record %s: %v", record.ID, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	defer router.Close()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The board only needs membership and a clock; the production host's clock
	// path goes through the app-server current-time request, which a unit test
	// has no client for.
	host := stubMessageBoardHost{members: map[string]agent.AgentPath{
		rootID: agent.AgentPathRoot, childID: agent.AgentPath("/root/worker"),
	}}
	board, err := agentboard.OpenLocalBoard(ctx, sqliteConfig, rootID, host)
	if err != nil {
		t.Fatalf("OpenLocalBoard() error = %v", err)
	}
	defer board.Close()
	if _, err := board.CreateChannel(ctx, rootID, agentboard.CreateChannelRequest{ChannelName: "work", Subscription: agentboard.Subscribe}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}

	// Deleting the subagent leaves the tree's board alone.
	if response := router.Handle(requestWithParams(t, IntID(2), MethodThreadDelete, ThreadDeleteParams{ThreadID: childID})); response.Error != nil {
		t.Fatalf("delete child error = %+v", response.Error)
	}
	if _, err := board.CreateChannel(ctx, rootID, agentboard.CreateChannelRequest{ChannelName: "still-here"}); err != nil {
		t.Fatalf("deleting a subagent dropped the tree's board: %v", err)
	}

	// Deleting the root tombstones the board, and the tombstone survives a
	// reopen.
	if response := router.Handle(requestWithParams(t, IntID(3), MethodThreadDelete, ThreadDeleteParams{ThreadID: rootID})); response.Error != nil {
		t.Fatalf("delete root error = %+v", response.Error)
	}
	if _, err := board.CreateChannel(ctx, rootID, agentboard.CreateChannelRequest{ChannelName: "gone"}); err == nil ||
		!strings.Contains(err.Error(), "permanently deleted") {
		t.Fatalf("create after delete error = %v", err)
	}
	board.Close()
	reopened, err := agentboard.OpenLocalBoard(ctx, sqliteConfig, rootID, host)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.CreateChannel(ctx, rootID, agentboard.CreateChannelRequest{ChannelName: "gone"}); err == nil ||
		!strings.Contains(err.Error(), "permanently deleted") {
		t.Fatalf("create after reopen error = %v", err)
	}
}

// Rust parity: a thread reuses one board handle across turns, and unloading the
// thread releases it (Rust opens a handle with the thread runtime and drops it
// when the runtime unloads).
func TestThreadUnloadReleasesTheMessageBoardHandleLikeRust(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(home)),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
	})
	defer router.Close()
	cfg := &config.Config{Values: map[string]any{}}
	v2 := &config.MultiAgentV2Config{ToolNamespace: "collaboration", MessageBoardInMemory: true}

	first, err := router.messageBoardOptionsForTurn(context.Background(), cfg, "thread-1", v2, nil)
	if err != nil || first == nil {
		t.Fatalf("messageBoardOptionsForTurn() = %#v, %v", first, err)
	}
	if router.messageBoards.Len() != 1 {
		t.Fatalf("live boards = %d, want one for the loaded thread", router.messageBoards.Len())
	}
	again, err := router.messageBoardOptionsForTurn(context.Background(), cfg, "thread-1", v2, nil)
	if err != nil || again == nil || again.Board != first.Board {
		t.Fatalf("the thread opened a second handle: %#v", again)
	}
	if router.messageBoards.Len() != 1 {
		t.Fatalf("live boards = %d, want the handle reused", router.messageBoards.Len())
	}

	router.markThreadUnloaded("thread-1")
	if router.messageBoards.Len() != 0 {
		t.Fatalf("live boards = %d, want the handle released on unload", router.messageBoards.Len())
	}
	// A thread loaded again after the unload opens a fresh handle.
	reopened, err := router.messageBoardOptionsForTurn(context.Background(), cfg, "thread-1", v2, nil)
	if err != nil || reopened == nil || reopened.Board == first.Board {
		t.Fatalf("handle after unload = %#v, %v", reopened, err)
	}
}

func TestMessageBoardGateRequiresBothFeaturesLikeRust(t *testing.T) {
	v2 := &config.MultiAgentV2Config{ToolNamespace: "collaboration"}
	both := &config.Config{Values: map[string]any{"features": map[string]any{
		"multi_agent_v2":      true,
		"agent_message_board": true,
	}}}
	if !messageBoardFeatureEnabled(both) || !messageBoardEnabledForTurn(both, false, v2) {
		t.Fatal("both features enabled did not enable the board")
	}
	if messageBoardEnabledForTurn(both, true, v2) {
		t.Fatal("an ephemeral session opened durable board storage")
	}
	inMemory := &config.MultiAgentV2Config{ToolNamespace: "collaboration", MessageBoardInMemory: true}
	if !messageBoardEnabledForTurn(both, true, inMemory) {
		t.Fatal("an ephemeral session did not open the in-memory board")
	}
	boardOnly := &config.Config{Values: map[string]any{"features": map[string]any{"agent_message_board": true}}}
	if messageBoardFeatureEnabled(boardOnly) || messageBoardEnabledForTurn(boardOnly, false, v2) {
		t.Fatal("the board was enabled without multi_agent_v2")
	}
	v2Only := &config.Config{Values: map[string]any{"features": map[string]any{"multi_agent_v2": true}}}
	if messageBoardFeatureEnabled(v2Only) || messageBoardEnabledForTurn(v2Only, false, v2) {
		t.Fatal("the board was enabled without agent_message_board")
	}
	if messageBoardEnabledForTurn(both, false, nil) {
		t.Fatal("the board was enabled without a V2 configuration")
	}
}

// Rust parity: LocalBoardHost resolves the caller's registered tree path, the
// tree's agents, and the caller's clock.
func TestMessageBoardHostResolvesTreeMembershipLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	rootID := "root-thread"
	childID := "child-thread"
	if err := store.Save(&session.Record{
		ID: session.ThreadID(rootID), SessionID: rootID,
		CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(),
		Metadata: session.Metadata{CWD: home, AgentPath: "/root"},
	}); err != nil {
		t.Fatalf("save root record: %v", err)
	}
	if err := store.Save(&session.Record{
		ID: session.ThreadID(childID), SessionID: rootID, ParentThreadID: session.ThreadID(rootID),
		CreatedAt: time.Unix(2, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(),
		Metadata: session.Metadata{CWD: home, AgentPath: "/root/worker", AgentDepth: 1},
	}); err != nil {
		t.Fatalf("save child record: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	defer router.Close()
	host := &messageBoardHost{router: router, tree: rootID, caller: rootID}

	path, err := host.AgentPath(context.Background(), rootID)
	if err != nil || path != agent.AgentPathRoot {
		t.Fatalf("root AgentPath() = %q, %v", path, err)
	}
	childPath, err := host.AgentPath(context.Background(), childID)
	if err != nil || childPath != agent.AgentPath("/root/worker") {
		t.Fatalf("child AgentPath() = %q, %v", childPath, err)
	}
	resolved, err := host.ResolveAgent(context.Background(), agent.AgentPath("/root/worker"))
	if err != nil || resolved != childID {
		t.Fatalf("ResolveAgent() = %q, %v", resolved, err)
	}
	if _, err := host.ResolveAgent(context.Background(), agent.AgentPath("/root/missing")); err == nil {
		t.Fatal("unknown path resolved")
	}
	if _, err := host.AgentPath(context.Background(), "unknown-thread"); err == nil {
		t.Fatal("unknown thread reported a tree path")
	}
	// The board clock goes through the app-server's current-time request, which
	// needs a subscribed client; that path is covered by the clock-tool tests.
	// This test pins the membership checks the board host makes first.
	if _, err := host.CurrentTime(context.Background(), "unknown-thread"); err == nil {
		t.Fatal("CurrentTime() succeeded for an unknown agent")
	}
}

// An idle recipient is skipped and nothing is queued for a later turn; a
// recipient from another tree is a hard error (Rust's notify contract).
func TestMessageBoardHostNotifyOnlyReachesRunningRecipientsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	rootID := "root-thread"
	childID := "child-thread"
	for _, record := range []*session.Record{
		{ID: session.ThreadID(rootID), SessionID: rootID, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(), Metadata: session.Metadata{CWD: home, AgentPath: "/root"}},
		{ID: session.ThreadID(childID), SessionID: rootID, ParentThreadID: session.ThreadID(rootID), CreatedAt: time.Unix(2, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(), Metadata: session.Metadata{CWD: home, AgentPath: "/root/worker", AgentDepth: 1}},
	} {
		if err := store.Save(record); err != nil {
			t.Fatalf("save record %s: %v", record.ID, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	defer router.Close()
	host := &messageBoardHost{router: router, tree: rootID, caller: rootID}
	post := agentboard.PostPreview{
		PostMetadata: agentboard.PostMetadata{
			MessageID: "11111111-1111-4111-8111-111111111111", ChannelName: "work",
			Author: agent.AgentPathRoot, ThreadID: "11111111-1111-4111-8111-111111111111",
		},
		TextPreview: "hello",
	}
	delivery, err := host.Notify(context.Background(), childID, post)
	if err != nil {
		t.Fatalf("Notify(idle) error = %v", err)
	}
	if delivery != agentboard.NotificationSkippedInactive {
		t.Fatalf("Notify(idle) = %q, want skipped", delivery)
	}
	if pending := router.requireSteerMailbox().HasPending(childID, "turn-1"); pending {
		t.Fatal("skipped notification queued input for a later turn")
	}

	// A recipient outside this tree is rejected rather than silently skipped.
	otherTree := "other-root"
	if err := store.Save(&session.Record{
		ID: session.ThreadID(otherTree), SessionID: otherTree,
		CreatedAt: time.Unix(3, 0).UTC(), UpdatedAt: time.Unix(3, 0).UTC(),
		Metadata: session.Metadata{CWD: home, AgentPath: "/root"},
	}); err != nil {
		t.Fatalf("save other tree record: %v", err)
	}
	if _, err := host.Notify(context.Background(), otherTree, post); err == nil ||
		!strings.Contains(err.Error(), "another board") {
		t.Fatalf("Notify(other tree) error = %v", err)
	}
}

// Rust parity: #49267 adds `features.multi_agent_v2.message_board_remote` with a
// board URL and a bearer token supplied directly or through an environment
// variable, and an ephemeral session may use the remote board without local
// storage (Rust `RemoteMessageBoardConfigToml` / `install_agent_message_board`).
func TestMessageBoardRemoteConfigParsesAndOpensEphemeralGateLikeRust(t *testing.T) {
	remote := map[string]any{
		"url":          "https://board.example/v1",
		"bearer_token": "research-board-credential-for-runtime-test",
	}
	cfg := &config.Config{Values: map[string]any{"features": map[string]any{
		"agent_message_board": true,
		"multi_agent_v2": map[string]any{
			"enabled":                 true,
			"message_board_remote":    remote,
			"message_board_in_memory": false,
		},
	}}}
	v2, err := cfg.MultiAgentV2Config(0)
	if err != nil {
		t.Fatalf("MultiAgentV2Config() error = %v", err)
	}
	if v2.MessageBoardRemote == nil {
		t.Fatal("message_board_remote was not parsed")
	}
	if v2.MessageBoardRemote.URL != "https://board.example/v1" {
		t.Fatalf("remote URL = %q", v2.MessageBoardRemote.URL)
	}
	if v2.MessageBoardRemote.BearerToken == nil || *v2.MessageBoardRemote.BearerToken != "research-board-credential-for-runtime-test" {
		t.Fatalf("remote bearer token = %v", v2.MessageBoardRemote.BearerToken)
	}
	if v2.MessageBoardRemote.BearerTokenEnvVar != nil {
		t.Fatalf("remote bearer token env var = %v, want none", *v2.MessageBoardRemote.BearerTokenEnvVar)
	}
	// An ephemeral session may use the configured remote board; without it, the
	// session must not open local SQLite storage.
	if !messageBoardEnabledForTurn(cfg, true, v2) {
		t.Fatal("an ephemeral session did not enable the configured remote board")
	}
	plain, err := cfg.MultiAgentV2Config(0)
	if err != nil {
		t.Fatal(err)
	}
	plain.MessageBoardRemote = nil
	if messageBoardEnabledForTurn(cfg, true, plain) {
		t.Fatal("an ephemeral session without a remote board opened durable storage")
	}

	envCfg := &config.Config{Values: map[string]any{"features": map[string]any{
		"multi_agent_v2": map[string]any{"message_board_remote": map[string]any{
			"url": "https://board.example", "bearer_token_env_var": "CODEX_BOARD_TOKEN",
		}},
	}}}
	envV2, err := envCfg.MultiAgentV2Config(0)
	if err != nil {
		t.Fatalf("MultiAgentV2Config(env) error = %v", err)
	}
	if envV2.MessageBoardRemote == nil || envV2.MessageBoardRemote.BearerTokenEnvVar == nil ||
		*envV2.MessageBoardRemote.BearerTokenEnvVar != "CODEX_BOARD_TOKEN" {
		t.Fatalf("remote env var config = %#v", envV2.MessageBoardRemote)
	}

	// Rust deserializes `url` as a required field.
	missingURL := &config.Config{Values: map[string]any{"features": map[string]any{
		"multi_agent_v2": map[string]any{"message_board_remote": map[string]any{"bearer_token": "x"}},
	}}}
	if _, err := missingURL.MultiAgentV2Config(0); err == nil ||
		!strings.Contains(err.Error(), "features.multi_agent_v2.message_board_remote.url is required") {
		t.Fatalf("missing url error = %v", err)
	}
}

// Rust parity: #49267 serves an ephemeral session's board from the configured
// remote endpoint in preference to local or in-memory storage, using the
// runtime's HTTP client and clock (Rust scenario
// `remote_board_uses_the_existing_tools_and_session_identity`): the request
// carries the bearer credential, the session identity as the board and caller,
// the caller's clock, and the post the service returned.
func TestRemoteMessageBoardPrecedesLocalBoardsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))

	const token = "research-board-credential-for-runtime-test"
	const clockAt int64 = 1781717655
	type boardCall struct {
		path string
		auth string
		body map[string]any
	}
	var (
		mu    sync.Mutex
		calls []boardCall
	)
	post := map[string]any{
		"message_id":   "00000000-0000-4000-8000-000000000001",
		"thread_id":    "00000000-0000-4000-8000-000000000001",
		"author":       "/root",
		"channel_name": "design",
		"created_at":   "2026-09-18T12:00:00Z",
	}
	board := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read board request body: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("decode board request body %q: %v", string(payload), err)
		}
		mu.Lock()
		calls = append(calls, boardCall{path: r.URL.Path, auth: r.Header.Get("Authorization"), body: body})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(post); err != nil {
			t.Errorf("encode board response: %v", err)
		}
	}))
	defer board.Close()

	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		HTTPClient:   board.Client(),
	})
	defer router.Close()
	// The remote board carries the caller's configured time, which the runtime
	// resolves through the thread's connected client (Rust's external time
	// provider).
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		if request.Method != ServerRequestCurrentTimeRead {
			t.Errorf("server request method = %s, want %s", request.Method, ServerRequestCurrentTimeRead)
			return
		}
		go func() {
			_, _ = router.requireServerRequests().Resolve(OK(request.ID, &CurrentTimeReadResponse{CurrentTimeAt: clockAt}))
		}()
	}))

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir(), Prompt: "hello"}))
	if start.Error != nil {
		t.Fatalf("thread/start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID

	cfg := &config.Config{Values: map[string]any{"features": map[string]any{
		"agent_message_board": true,
		"multi_agent_v2": map[string]any{
			"enabled": true,
			"message_board_remote": map[string]any{
				"url":          board.URL,
				"bearer_token": token,
			},
		},
	}}}
	v2, err := cfg.MultiAgentV2Config(0)
	if err != nil {
		t.Fatalf("MultiAgentV2Config() error = %v", err)
	}
	options, err := router.messageBoardOptionsForTurn(context.Background(), cfg, threadID, v2, nil)
	if err != nil || options == nil {
		t.Fatalf("messageBoardOptionsForTurn() = %#v, %v", options, err)
	}
	remote, ok := options.Board.(*agentboard.RemoteBoard)
	if !ok {
		t.Fatalf("board = %T, want the configured remote board to take precedence", options.Board)
	}
	if remote.Identity() != threadID {
		t.Fatalf("remote board identity = %q, want the session %q", remote.Identity(), threadID)
	}
	if options.Caller != threadID {
		t.Fatalf("board caller = %q, want %q", options.Caller, threadID)
	}

	posted, err := options.Board.Post(context.Background(), threadID, agentboard.PostRequest{
		RequestID:   "remote-post",
		Destination: agentboard.PostDestination{Kind: "new_channel", Name: "design"},
		Text:        "A remote decision.",
	})
	if err != nil {
		t.Fatalf("remote Post() error = %v", err)
	}
	if posted.MessageID != "00000000-0000-4000-8000-000000000001" || posted.ChannelName != "design" {
		t.Fatalf("remote Post() = %#v, want the service's returned post", posted)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("board requests = %d, want one remote call (%#v)", len(calls), calls)
	}
	call := calls[0]
	if call.path != "/v1/boards/"+threadID+"/call" {
		t.Fatalf("board request path = %q, want /v1/boards/%s/call", call.path, threadID)
	}
	if call.auth != "Bearer "+token {
		t.Fatalf("board authorization = %q, want bearer authentication", call.auth)
	}
	if call.body["caller"] != threadID || call.body["method"] != "post" {
		t.Fatalf("board request = %#v, want caller %s and method post", call.body, threadID)
	}
	if call.body["timestamp"] != time.Unix(clockAt, 0).UTC().Format(time.RFC3339) {
		t.Fatalf("board timestamp = %v, want the caller's clock %s", call.body["timestamp"], time.Unix(clockAt, 0).UTC().Format(time.RFC3339))
	}
	params, _ := call.body["params"].(map[string]any)
	if params["text"] != "A remote decision." {
		t.Fatalf("board params = %#v, want the posted text", params)
	}
}

// Rust parity: #49267 strips `features.multi_agent_v2.message_board_remote` from
// project-local configuration (Rust `sanitize_project_config` removes the key
// and reports it), so a repository cannot redirect the board or inject a
// credential.
func TestProjectConfigCannotSetRemoteMessageBoardLikeRust(t *testing.T) {
	dir := t.TempDir()
	dotCodex := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(dotCodex, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dotCodex, "config.toml")
	body := strings.Join([]string{
		`model = "gpt-5"`,
		`[features.multi_agent_v2.message_board_remote]`,
		`url = "https://project.example.com/v1"`,
		`bearer_token = "project-supplied-credential-000000000000000000"`,
	}, "\n") + "\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	warnings := config.ProjectIgnoredConfigKeysWarningsForLayers([]config.Layer{{
		Name: config.LayerSource{Type: config.LayerSourceProject, File: configPath, DotCodexFolder: dotCodex},
	}})
	if len(warnings) != 1 {
		t.Fatalf("project warnings = %#v, want one warning", warnings)
	}
	if !strings.Contains(warnings[0], "features.multi_agent_v2.message_board_remote") {
		t.Fatalf("project warning = %q, want the remote board key reported as ignored", warnings[0])
	}
	// The stripped key never reaches the effective configuration, so the parsed
	// V2 settings carry no remote board.
	stripped, err := config.NewConfigService(dir).Read(&config.ConfigReadParams{CWD: &dir})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	values, _ := stripped.Config["features"].(map[string]any)
	multiAgent, _ := values["multi_agent_v2"].(map[string]any)
	if _, ok := multiAgent["message_board_remote"]; ok {
		t.Fatalf("project config still supplies message_board_remote: %#v", multiAgent)
	}
}

// Rust parity: the remote board settings load from user configuration through
// the same loader path as the rest of `features.multi_agent_v2` (Rust
// `features/src/tests.rs` parses `message_board_remote` together with the other
// V2 overrides).
func TestUserConfigTOMLSuppliesRemoteMessageBoardLikeRust(t *testing.T) {
	home := t.TempDir()
	toml := strings.Join([]string{
		`[features]`,
		`agent_message_board = true`,
		`[features.multi_agent_v2]`,
		`enabled = true`,
		`[features.multi_agent_v2.message_board_remote]`,
		`url = "https://board.example/v1"`,
		`bearer_token_env_var = "CODEX_BOARD_TOKEN"`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.NewConfigService(home).Read(&config.ConfigReadParams{})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	cfg := &config.Config{Values: loaded.Config}
	v2, err := cfg.MultiAgentV2Config(0)
	if err != nil {
		t.Fatalf("MultiAgentV2Config() error = %v", err)
	}
	if v2.MessageBoardRemote == nil {
		t.Fatalf("user TOML did not supply message_board_remote: %#v", loaded.Config)
	}
	if v2.MessageBoardRemote.URL != "https://board.example/v1" {
		t.Fatalf("remote URL = %q", v2.MessageBoardRemote.URL)
	}
	if v2.MessageBoardRemote.BearerTokenEnvVar == nil || *v2.MessageBoardRemote.BearerTokenEnvVar != "CODEX_BOARD_TOKEN" {
		t.Fatalf("remote credential env var = %v", v2.MessageBoardRemote.BearerTokenEnvVar)
	}
	if !messageBoardEnabledForTurn(cfg, true, v2) {
		t.Fatal("the configured remote board did not enable an ephemeral session")
	}
}

// Rust parity: #49267 reads the board credential from `bearer_token_env_var`
// when it is named, reports a missing environment as a missing credential, and
// requires a credential when neither source is configured.
func TestRemoteMessageBoardCredentialResolutionLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Config:       config.NewConfigService(t.TempDir()),
	})
	defer router.Close()
	host := &messageBoardHost{router: router, tree: "thread-1", caller: "thread-1"}
	const token = "research-board-credential-for-runtime-test"

	if _, err := router.openRemoteMessageBoard(context.Background(), nil, host, "thread-1", &config.RemoteMessageBoardConfig{URL: "https://board.example"}); err == nil ||
		!strings.Contains(err.Error(), "remote message board requires a credential") {
		t.Fatalf("credentialless remote board error = %v", err)
	}
	missingEnvVar := "CODEX_BOARD_TOKEN_MISSING_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	_ = os.Unsetenv(missingEnvVar)
	if _, err := router.openRemoteMessageBoard(context.Background(), nil, host, "thread-1", &config.RemoteMessageBoardConfig{
		URL: "https://board.example", BearerTokenEnvVar: &missingEnvVar,
	}); err == nil || !strings.Contains(err.Error(), "message-board credential environment variable is missing or invalid") {
		t.Fatalf("missing environment error = %v", err)
	}
	envVar := "CODEX_BOARD_TOKEN_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Setenv(envVar, token)
	board, err := router.openRemoteMessageBoard(context.Background(), nil, host, "thread-1", &config.RemoteMessageBoardConfig{
		URL: "https://board.example", BearerTokenEnvVar: &envVar,
	})
	if err != nil {
		t.Fatalf("environment credential error = %v", err)
	}
	remote, ok := board.(*agentboard.RemoteBoard)
	if !ok || remote.Identity() != "thread-1" {
		t.Fatalf("remote board = %#v, want the session identity board", board)
	}
	// The environment wins over a directly supplied credential, and a
	// credential Rust's AccessToken rejects is rejected here too.
	t.Setenv(envVar, "short")
	if _, err := router.openRemoteMessageBoard(context.Background(), nil, host, "thread-1", &config.RemoteMessageBoardConfig{
		URL: "https://board.example", BearerToken: ptrTo(token), BearerTokenEnvVar: &envVar,
	}); err == nil || !strings.Contains(err.Error(), "credentials must contain") {
		t.Fatalf("invalid environment credential error = %v", err)
	}
	if _, err := router.openRemoteMessageBoard(context.Background(), nil, host, "thread-1", &config.RemoteMessageBoardConfig{
		URL: "https://board.example", BearerToken: ptrTo("short"),
	}); err == nil || !strings.Contains(err.Error(), "credentials must contain") {
		t.Fatalf("invalid direct credential error = %v", err)
	}
}

func ptrTo[T any](value T) *T { return &value }
