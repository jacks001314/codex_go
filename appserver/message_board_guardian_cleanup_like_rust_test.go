package appserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/agent"
	"codex_go/agentboard"
	"codex_go/config"
	"codex_go/session"
	"codex_go/state"
)

// Mirrors Rust #58955: permanent thread deletion removes Guardian evidence
// boards only when Guardian V3 is enabled. Boards in other namespaces and
// boards owned by surviving roots remain available.
func TestThreadDeleteRemovesGuardianEvidenceBoardsOnlyWhenFeatureEnabledLikeRust(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("guardian_enabled_%t", enabled), func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(config.ConfigPath(home), []byte(fmt.Sprintf("[features.guardianv3]\nenabled = %t\n", enabled)), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			store := session.NewStore(filepath.Join(home, "sessions"))
			const deletedRoot = "guardian-deleted-root"
			const retainedRoot = "guardian-retained-root"
			for _, id := range []string{deletedRoot, retainedRoot} {
				if err := store.Save(&session.Record{ID: session.ThreadID(id), SessionID: id, Metadata: session.Metadata{CWD: home}}); err != nil {
					t.Fatalf("save record %s: %v", id, err)
				}
			}
			router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(home)})
			t.Cleanup(func() { _ = router.Close() })
			sqliteConfig, err := state.NewSqliteConfig(home)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			host := stubMessageBoardHost{members: map[string]agent.AgentPath{
				deletedRoot:  agent.AgentPathRoot,
				retainedRoot: "/root/retained",
			}}
			deletedBoard, err := agentboard.NewGuardianLocalBoard(sqliteConfig, deletedRoot).ReadWrite().Open(ctx, host)
			if err != nil {
				t.Fatalf("open deleted Guardian board: %v", err)
			}
			t.Cleanup(func() { deletedBoard.Close() })
			retainedBoard, err := agentboard.NewGuardianLocalBoard(sqliteConfig, retainedRoot).ReadWrite().Open(ctx, host)
			if err != nil {
				t.Fatalf("open retained Guardian board: %v", err)
			}
			t.Cleanup(func() { retainedBoard.Close() })
			for _, board := range []*agentboard.LocalAgentMessageBoard{deletedBoard, retainedBoard} {
				if _, err := board.CreateChannel(ctx, board.Identity(), agentboard.CreateChannelRequest{ChannelName: "evidence"}); err != nil {
					t.Fatalf("seed Guardian board %s: %v", board.Identity(), err)
				}
			}

			response := router.Handle(requestWithParams(t, IntID(1), MethodThreadDelete, ThreadDeleteParams{ThreadID: deletedRoot}))
			if response.Error != nil {
				t.Fatalf("thread/delete error = %+v", response.Error)
			}
			_, deletedErr := deletedBoard.CreateChannel(ctx, deletedRoot, agentboard.CreateChannelRequest{ChannelName: "after-delete"})
			if enabled {
				if deletedErr == nil || !strings.Contains(deletedErr.Error(), "permanently deleted") {
					t.Fatalf("deleted Guardian board write error = %v", deletedErr)
				}
			} else if deletedErr != nil {
				t.Fatalf("disabled Guardian cleanup modified the board: %v", deletedErr)
			}
			if _, err := retainedBoard.CreateChannel(ctx, retainedRoot, agentboard.CreateChannelRequest{ChannelName: "still-here"}); err != nil {
				t.Fatalf("surviving Guardian board was removed: %v", err)
			}
		})
	}
}
