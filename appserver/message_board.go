package appserver

// Host-side wiring for the shared discussion board.
//
// Rust parity: codex-rs/core/src/agent_message_board.rs, which bridges the board
// extension to the selected controller, clock and active-turn services. The
// board owns storage and tools; this adapter never starts or restores
// recipients and never queues a notification for an idle agent.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"codex_go/agent"
	"codex_go/agentboard"
	"codex_go/config"
	featureflags "codex_go/features"
	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
	"codex_go/turn"
)

// messageBoardCleanup removes the discussion boards owned by permanently
// deleted thread roots (Rust's local thread store `thread_data_cleanup`
// callback). A child's ID does not match its parent's board, so deleting a
// subagent thread leaves the tree's board in place. The callback is safe to
// retry and independent of whether the message-board feature is currently
// enabled.
func (r *RuntimeRouter) messageBoardCleanup(threadIDs []session.ThreadID) error {
	if r == nil || len(threadIDs) == 0 {
		return nil
	}
	roots := make([]string, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		if id := strings.TrimSpace(string(threadID)); id != "" {
			roots = append(roots, id)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	sqliteConfig, err := r.messageBoardSqliteConfig(r.effectiveConfigForSessionTelemetry())
	if err != nil {
		// No resolvable board storage means there is no durable board to drop;
		// failing the whole delete here would strand the caller on a missing
		// sqlite home rather than a cleanup problem.
		return nil
	}
	if err := agentboard.DeleteLocalBoards(context.Background(), sqliteConfig, roots); err != nil {
		return fmt.Errorf("failed to delete agent message boards: %w", err)
	}
	return nil
}

// messageBoardFeatureEnabled reports whether the two features that own the
// board are on (Rust `install_agent_message_board`'s feature gate).
func messageBoardFeatureEnabled(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	settings := cfg.FeatureSettings()
	return featureflags.Enabled(settings, "agent_message_board") && featureflags.Enabled(settings, "multi_agent_v2")
}

// messageBoardEnabledForTurn mirrors Rust's `install_agent_message_board` gate:
// the board is available only when both features are enabled, and an ephemeral
// session must not open durable storage unless the board is kept in memory.
func messageBoardEnabledForTurn(cfg *config.Config, ephemeral bool, v2Config *config.MultiAgentV2Config) bool {
	if !messageBoardFeatureEnabled(cfg) || v2Config == nil {
		return false
	}
	if ephemeral && !v2Config.MessageBoardInMemory {
		return false
	}
	return true
}

// messageBoardOptionsForTurn opens the caller's board and returns the tool
// registration options for the turn. Rust defers backend selection to the host:
// the in-memory backend shares one board per tree, and the SQLite backend
// reopens the tree's durable board.
func (r *RuntimeRouter) messageBoardOptionsForTurn(ctx context.Context, cfg *config.Config, threadID string, v2Config *config.MultiAgentV2Config, turnModelInfo *model.ModelInfo) (*turn.MessageBoardOptions, error) {
	if r == nil || cfg == nil || v2Config == nil {
		return nil, nil
	}
	tree, callerPath := r.runtimeAgentIdentity(threadID)
	if strings.TrimSpace(tree) == "" {
		return nil, fmt.Errorf("message board has no tree identity for thread %s", threadID)
	}
	host := &messageBoardHost{router: r, tree: tree, caller: strings.TrimSpace(threadID)}
	var board agentboard.Board
	if v2Config.MessageBoardInMemory {
		board = r.messageBoards.Open(tree, host)
	} else {
		sqliteConfig, err := r.messageBoardSqliteConfig(cfg)
		if err != nil {
			return nil, err
		}
		opened, err := agentboard.OpenLocalBoard(ctx, sqliteConfig, tree, host)
		if err != nil {
			return nil, err
		}
		board = opened
	}
	return &turn.MessageBoardOptions{
		Board:                board,
		Caller:               strings.TrimSpace(threadID),
		CallerPath:           agent.AgentPath(callerPath),
		Namespace:            v2Config.ToolNamespace,
		NamespaceDescription: agent.MultiAgentV2NamespaceDescription,
		ToolOverrides:        messageBoardToolOverridesFromCatalog(turnModelInfo),
	}, nil
}

// messageBoardSqliteConfig mirrors Rust's `config.sqlite_config()`: the
// configured sqlite_home wins, then CODEX_SQLITE_HOME, then the codex home.
func (r *RuntimeRouter) messageBoardSqliteConfig(cfg *config.Config) (state.SqliteConfig, error) {
	codexHome := ""
	if r != nil && r.services.Config != nil {
		codexHome = r.services.Config.CodexHome()
	}
	override := ""
	if cfg != nil {
		override = cfg.SQLiteHome()
	}
	return state.SqliteConfigForCodexHomeWithOverride(codexHome, override)
}

// messageBoardToolOverridesFromCatalog reads the active model's per-tool
// overrides for the discussion tools (Rust `MultiAgentToolMessages::by_name`,
// which the board tools look up by their own names).
func messageBoardToolOverridesFromCatalog(info *model.ModelInfo) map[string]agentboard.MessageBoardToolOverride {
	if info == nil || info.ModelMessages == nil {
		return nil
	}
	out := map[string]agentboard.MessageBoardToolOverride{}
	for _, name := range agentboard.MessageBoardToolNames {
		description := info.ModelMessages.MultiAgentToolDescriptionOverride(name)
		if description == nil {
			continue
		}
		out[name] = agentboard.MessageBoardToolOverride{Description: description}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// messageBoardHost mirrors Rust's LocalBoardHost.
type messageBoardHost struct {
	router *RuntimeRouter
	tree   string
	caller string
}

func (h *messageBoardHost) registry() *agent.Registry {
	if h == nil || h.router == nil {
		return nil
	}
	return h.router.runtimeAgentRegistry(h.tree)
}

// registeredPath mirrors Rust's `LocalBoardHost::registered_path`: the agent's
// recorded tree path, with the root fallback, must still resolve back to the
// same thread.
func (h *messageBoardHost) registeredPath(caller string) (agent.AgentPath, error) {
	caller = strings.TrimSpace(caller)
	registry := h.registry()
	if registry == nil {
		return "", fmt.Errorf("agent runtime is unavailable")
	}
	path := agent.AgentPath("")
	if metadata, ok := registry.MetadataForThread(caller); ok {
		path = metadata.Path
	} else if caller == strings.TrimSpace(h.tree) {
		path = agent.AgentPathRoot
	}
	if strings.TrimSpace(string(path)) == "" {
		return "", fmt.Errorf("agent has no tree path")
	}
	resolved, ok := registry.AgentIDForPath(path)
	if !ok || strings.TrimSpace(resolved) != caller {
		return "", fmt.Errorf("agent does not own its tree path")
	}
	return path, nil
}

// AgentPath returns the caller's registered tree path.
func (h *messageBoardHost) AgentPath(ctx context.Context, caller string) (agent.AgentPath, error) {
	_ = ctx
	return h.registeredPath(caller)
}

// ResolveAgent resolves a path in this tree to its thread.
func (h *messageBoardHost) ResolveAgent(ctx context.Context, path agent.AgentPath) (string, error) {
	_ = ctx
	registry := h.registry()
	if registry == nil {
		return "", fmt.Errorf("agent runtime is unavailable")
	}
	if _, err := h.registeredPath(h.caller); err != nil {
		return "", err
	}
	threadID, ok := registry.AgentIDForPath(path)
	if !ok || strings.TrimSpace(threadID) == "" {
		return "", fmt.Errorf("unknown agent")
	}
	return strings.TrimSpace(threadID), nil
}

// CurrentTime reads the posting agent's configured clock, including simulated
// time.
func (h *messageBoardHost) CurrentTime(ctx context.Context, caller string) (time.Time, error) {
	if h == nil || h.router == nil {
		return time.Time{}, fmt.Errorf("agent runtime is unavailable")
	}
	if _, err := h.registeredPath(caller); err != nil {
		return time.Time{}, err
	}
	provider := &appServerClockProvider{router: h.router}
	return provider.CurrentTime(ctx, strings.TrimSpace(caller))
}

// Notify pushes metadata and a bounded preview to a recipient that is running a
// turn right now. An idle, finalized or unloaded recipient is skipped; nothing
// is queued for a later turn.
func (h *messageBoardHost) Notify(ctx context.Context, recipient string, post agentboard.PostPreview) (agentboard.NotificationDelivery, error) {
	if h == nil || h.router == nil {
		return agentboard.NotificationSkippedInactive, nil
	}
	recipient = strings.TrimSpace(recipient)
	// Rust resolves the recipient first (a missing thread is skipped) and
	// rejects an agent that belongs to another board before it asks whether the
	// recipient is running.
	record, recordErr := h.router.threadRecord(session.ThreadID(recipient), true, false)
	if recordErr != nil || record == nil {
		return agentboard.NotificationSkippedInactive, nil
	}
	recipientTree, _ := h.router.runtimeAgentIdentity(recipient)
	if strings.TrimSpace(recipientTree) != strings.TrimSpace(h.tree) {
		return "", fmt.Errorf("notification recipient belongs to another board")
	}
	recipientPath, err := h.registeredPath(recipient)
	if err != nil {
		return "", err
	}
	active := h.router.activeRuntimeTurnSnapshot(recipient)
	if active == nil {
		return agentboard.NotificationSkippedInactive, nil
	}
	notice := agentboard.NewAgentMessageBoardNotification(post)
	item := runtimeAgentCommunicationInputItem(string(post.Author), string(recipientPath), notice.Body(), false, true)
	if err := h.router.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{
		ThreadID: recipient, TurnID: active.ID, InputItems: []any{item},
	}); err != nil {
		return agentboard.NotificationSkippedInactive, nil
	}
	return agentboard.NotificationAccepted, nil
}

var _ agentboard.Host = (*messageBoardHost)(nil)
