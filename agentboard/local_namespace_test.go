package agentboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Mirrors Rust #55971: boards that share a session identity can be isolated in
// separate storage, the selected namespace is used consistently when opening,
// configuring and deleting, and the default namespace keeps its historical
// database filename.
func TestLocalBoardStorageNamespacesLikeRust(t *testing.T) {
	t.Run("database file names", func(t *testing.T) {
		name, err := (LocalBoardOptions{}).databaseFile()
		if err != nil {
			t.Fatalf("default databaseFile() error = %v", err)
		}
		if name != AgentMessageBoardDatabaseFile {
			t.Fatalf("default database file = %q, want %q", name, AgentMessageBoardDatabaseFile)
		}
		explicitDefault, err := (LocalBoardOptions{StorageNamespace: DefaultBoardStorageNamespace}).databaseFile()
		if err != nil || explicitDefault != AgentMessageBoardDatabaseFile {
			t.Fatalf("explicit default databaseFile() = %q, %v", explicitDefault, err)
		}
		custom, err := (LocalBoardOptions{StorageNamespace: "shared_session-2"}).databaseFile()
		if err != nil {
			t.Fatalf("custom databaseFile() error = %v", err)
		}
		if custom != "message_board_shared_session-2_1.sqlite" {
			t.Fatalf("custom database file = %q", custom)
		}
		// Custom boards must not share a filename with other runtime databases.
		for _, runtimeDatabase := range []string{"logs_2.sqlite", "state_5.sqlite", AgentMessageBoardDatabaseFile} {
			if custom == runtimeDatabase {
				t.Fatalf("custom database file %q collides with %q", custom, runtimeDatabase)
			}
		}
	})

	t.Run("invalid namespaces are rejected", func(t *testing.T) {
		for _, namespace := range []string{
			"AgentBoard",
			"agent board",
			"agent/board",
			"agent.board",
			"agent_board\n",
			"\u00e9",
			strings.Repeat("a", 65),
		} {
			if _, err := (LocalBoardOptions{StorageNamespace: namespace}).databaseFile(); err == nil || !IsInvalidRequest(err) {
				t.Fatalf("namespace %q error = %v, want invalid request", namespace, err)
			}
		}
		if _, err := (LocalBoardOptions{StorageNamespace: strings.Repeat("a", 64)}).databaseFile(); err != nil {
			t.Fatalf("64-byte namespace error = %v", err)
		}
	})

	t.Run("namespaces isolate boards", func(t *testing.T) {
		ctx := context.Background()
		host, root, _, _ := testTree(t)
		sqlite := openBoardSqliteConfig(t)
		custom := LocalBoardOptions{StorageNamespace: "isolated"}

		defaultBoard := openLocalBoard(t, sqlite, root, host)
		if _, err := defaultBoard.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "general", Subscription: Subscribe}); err != nil {
			t.Fatalf("default CreateChannel() error = %v", err)
		}
		if _, err := defaultBoard.Post(ctx, root, PostRequest{
			RequestID:   "default:1",
			Destination: PostDestination{Kind: "channel", Name: "general"},
			Text:        "default board post",
		}); err != nil {
			t.Fatalf("default Post() error = %v", err)
		}
		defaultBoard.Close()

		customBoard, err := OpenLocalBoardWithOptions(ctx, sqlite, custom, root, host)
		if err != nil {
			t.Fatalf("OpenLocalBoardWithOptions() error = %v", err)
		}
		if _, err := customBoard.CreateChannel(ctx, root, CreateChannelRequest{ChannelName: "general", Subscription: Subscribe}); err != nil {
			t.Fatalf("custom CreateChannel() error = %v", err)
		}
		if _, err := customBoard.Post(ctx, root, PostRequest{
			RequestID:   "custom:1",
			Destination: PostDestination{Kind: "channel", Name: "general"},
			Text:        "custom board post",
		}); err != nil {
			t.Fatalf("custom Post() error = %v", err)
		}
		customBoard.Close()

		for _, filename := range []string{AgentMessageBoardDatabaseFile, "message_board_isolated_1.sqlite"} {
			if _, err := os.Stat(filepath.Join(sqlite.Home(), filename)); err != nil {
				t.Fatalf("storage file %q missing: %v", filename, err)
			}
		}

		// Reopening each namespace keeps its own posts.
		for _, testCase := range []struct {
			name    string
			options LocalBoardOptions
			preview string
			text    string
		}{
			{name: "default", options: LocalBoardOptions{}, preview: "default board post", text: "default board post"},
			{name: "custom", options: custom, preview: "custom board post", text: "custom board post"},
		} {
			reopened, err := OpenLocalBoardWithOptions(ctx, sqlite, testCase.options, root, host)
			if err != nil {
				t.Fatalf("%s reopen error = %v", testCase.name, err)
			}
			threads, err := reopened.ListThreads(ctx, root, ThreadQuery{
				ChannelName:     "general",
				Sort:            ThreadSortActivity,
				Direction:       NewestFirst,
				Page:            PageRequest{Limit: 10},
				MaxCharsPerPost: 1_000,
			})
			reopened.Close()
			if err != nil {
				t.Fatalf("%s ListThreads() error = %v", testCase.name, err)
			}
			if len(threads.Results) != 1 || threads.Results[0].RootPost.TextPreview != testCase.preview {
				t.Fatalf("%s threads = %#v, want %q", testCase.name, threads.Results, testCase.preview)
			}
		}
	})

	t.Run("setup and deletion use the namespace", func(t *testing.T) {
		ctx := context.Background()
		sqlite := openBoardSqliteConfig(t)
		custom := LocalBoardOptions{StorageNamespace: "ops"}
		setup := LocalBoardSetup{Template: LocalBoardTemplate{
			Version:  localBoardTemplateVersion,
			Channels: map[string]LocalBoardTemplateChannel{"alpha": {}},
		}}
		const boardID = "root-thread"
		if err := ConfigureLocalBoardWithOptions(ctx, sqlite, LocalBoardOptions{}, boardID, setup); err != nil {
			t.Fatalf("default ConfigureLocalBoardWithOptions() error = %v", err)
		}
		if err := ConfigureLocalBoardWithOptions(ctx, sqlite, custom, boardID, setup); err != nil {
			t.Fatalf("custom ConfigureLocalBoardWithOptions() error = %v", err)
		}
		if err := DeleteLocalBoardsWithOptions(ctx, sqlite, custom, []string{boardID}); err != nil {
			t.Fatalf("DeleteLocalBoardsWithOptions() error = %v", err)
		}
		// The custom namespace is tombstoned while the default namespace still
		// accepts the identical setup.
		if err := ConfigureLocalBoardWithOptions(ctx, sqlite, custom, boardID, setup); err == nil || !IsInvalidRequest(err) {
			t.Fatalf("deleted custom namespace configure error = %v, want invalid request", err)
		}
		if err := ConfigureLocalBoardWithOptions(ctx, sqlite, LocalBoardOptions{}, boardID, setup); err != nil {
			t.Fatalf("default namespace configure after custom deletion error = %v", err)
		}
	})

	t.Run("connection options select storage", func(t *testing.T) {
		ctx := context.Background()
		host, root, _, _ := testTree(t)
		sqlite := openBoardSqliteConfig(t)
		board := NewLocalBoard(sqlite, root).WithOptions(LocalBoardOptions{StorageNamespace: "connections"})
		opened, err := board.ReadWrite().Open(ctx, host)
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		defer opened.Close()
		if opened.poolPath != filepath.Join(mustCanonicalHome(t, sqlite), "message_board_connections_1.sqlite") {
			t.Fatalf("opened database = %q", opened.poolPath)
		}
	})
}

func mustCanonicalHome(t *testing.T, sqlite interface{ Home() string }) string {
	t.Helper()
	home := sqlite.Home()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	absolute, err := filepath.Abs(home)
	if err != nil {
		t.Fatalf("filepath.Abs() error = %v", err)
	}
	return absolute
}
