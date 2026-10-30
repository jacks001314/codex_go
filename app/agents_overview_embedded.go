package app

import (
	"context"
	"errors"
	"strings"

	"codex_go/appserver"
	"codex_go/session"
	agentsoverview "codex_go/tui/agents_overview"
	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

// Rust #58238/#58239: embedded sessions run the command center against this
// process's own app server, so listing, switching, and lifecycle actions work
// without a shared background server.
type embeddedAgentsDashboardSource struct {
	store *session.Store
	// closeSideConversations shuts down side conversations whose parent belongs
	// to the affected thread's subtree and reports whether they all closed.
	// Conversations owned by other threads stay open (Rust #58237).
	closeSideConversations func(threadIDs map[string]struct{}) bool
}

func newEmbeddedAgentsDashboardSource(store *session.Store, closeSideConversations func(map[string]struct{}) bool) *embeddedAgentsDashboardSource {
	if store == nil {
		store = newAgentsDashboardStore()
	}
	return &embeddedAgentsDashboardSource{store: store, closeSideConversations: closeSideConversations}
}

func (s *embeddedAgentsDashboardSource) List(context.Context) ([]agentsoverview.Row, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("session store is unavailable")
	}
	records, err := listSessionsByArchived(s.store, false)
	if err != nil {
		return nil, err
	}
	return agentsOverviewRowsFromRecords(records, ""), nil
}

func (s *embeddedAgentsDashboardSource) NewSession(_ context.Context, cwd string) (string, error) {
	started, err := s.StartSession(cwd)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(started.Thread.ID), nil
}

// StartSession returns the full start response so the dashboard can attach the
// new session with its server-resolved settings (Rust #45255).
func (s *embeddedAgentsDashboardSource) StartSession(cwd string) (*appserver.ThreadStartResponse, error) {
	params := appserver.ThreadStartParams{CWD: strings.TrimSpace(cwd)}
	result, err := s.request(appserver.MethodThreadStart, params)
	if err != nil {
		return nil, err
	}
	started, ok := result.(*appserver.ThreadStartResponse)
	if !ok || started == nil || started.Thread == nil || strings.TrimSpace(started.Thread.ID) == "" {
		return nil, errors.New("thread start response did not include a thread id")
	}
	return started, nil
}

func (s *embeddedAgentsDashboardSource) Stop(_ context.Context, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("agent thread id is required to stop")
	}
	result, err := s.request(appserver.MethodThreadRead, appserver.ThreadReadParams{ThreadID: threadID, IncludeTurns: true})
	if err != nil {
		return err
	}
	read, ok := result.(*appserver.ThreadReadResponse)
	if !ok || read == nil || read.Thread == nil {
		return errors.New("thread read response did not include a thread")
	}
	for i := range read.Thread.Turns {
		if read.Thread.Turns[i].Status != appserver.TurnStatusInProgress {
			continue
		}
		_, err := s.request(appserver.MethodTurnInterrupt, turn.TurnInterruptParams{
			ThreadID: threadID,
			TurnID:   strings.TrimSpace(read.Thread.Turns[i].ID),
		})
		return err
	}
	return nil
}

func (s *embeddedAgentsDashboardSource) Rename(_ context.Context, threadID, name string) error {
	threadID = strings.TrimSpace(threadID)
	name = strings.TrimSpace(name)
	if threadID == "" {
		return errors.New("agent thread id is required to rename")
	}
	if name == "" {
		return errors.New("task name must not be empty")
	}
	_, err := s.request(appserver.MethodThreadSetName, appserver.ThreadSetNameParams{ThreadID: threadID, Name: name})
	return err
}

func (s *embeddedAgentsDashboardSource) Archive(_ context.Context, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("agent thread id is required to archive")
	}
	if !s.closeRelatedSideConversations(threadID) {
		return errors.New("a side conversation for this task is still open")
	}
	if _, err := s.request(appserver.MethodThreadArchive, appserver.ThreadArchiveParams{ThreadID: threadID}); err != nil {
		// Rust #48828/#58238: a fresh task has no rollout until its first turn,
		// and the app server persists the loaded thread before archiving it.
		// This embedded server is built per request, so it holds no live thread
		// to persist; archive the persisted registration record directly so a
		// task created in the command center can still be archived immediately.
		if !isFreshThreadArchiveError(err) {
			return err
		}
		if storeErr := s.store.Archive(session.ThreadID(threadID)); storeErr != nil && !errors.Is(storeErr, session.ErrThreadNotFound) {
			return storeErr
		}
	}
	return nil
}

// isFreshThreadArchiveError reports the app server's missing-rollout rejection
// for a thread whose first turn has not run yet.
func isFreshThreadArchiveError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no rollout found for thread id")
}

func (s *embeddedAgentsDashboardSource) Delete(_ context.Context, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("agent thread id is required to delete")
	}
	if !s.closeRelatedSideConversations(threadID) {
		return errors.New("a side conversation for this task is still open")
	}
	_, err := s.request(appserver.MethodThreadDelete, appserver.ThreadDeleteParams{ThreadID: threadID})
	return err
}

func (s *embeddedAgentsDashboardSource) Close() {}

func (s *embeddedAgentsDashboardSource) closeRelatedSideConversations(threadID string) bool {
	if s == nil || s.closeSideConversations == nil {
		return true
	}
	return s.closeSideConversations(knownLocalThreadSubtree(s.store, threadID))
}

func (s *embeddedAgentsDashboardSource) request(method appserver.Method, params any) (any, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("session store is unavailable")
	}
	router := appserver.NewRouter(s.store)
	defer router.Close()
	return localSideRequest(router, appserver.IntID(1), method, params)
}

// knownLocalThreadSubtree walks the persisted parent graph so lifecycle actions
// cover the same subtree the dashboard groups under a root task (Rust #58237
// known_thread_subtree).
func knownLocalThreadSubtree(store *session.Store, threadID string) map[string]struct{} {
	ids := map[string]struct{}{strings.TrimSpace(threadID): {}}
	if store == nil || strings.TrimSpace(threadID) == "" {
		return ids
	}
	parentOf := map[string]string{}
	for _, archived := range []bool{false, true} {
		records, err := listSessionsByArchived(store, archived)
		if err != nil {
			continue
		}
		for i := range records {
			id := strings.TrimSpace(string(records[i].ID))
			parent := strings.TrimSpace(string(records[i].ParentThreadID))
			if id != "" && parent != "" {
				parentOf[id] = parent
			}
		}
	}
	for {
		before := len(ids)
		for id, parent := range parentOf {
			if _, removed := ids[parent]; removed {
				ids[id] = struct{}{}
			}
		}
		if len(ids) == before {
			return ids
		}
	}
}

func embeddedAgentsOverviewRefresh(store *session.Store, closeSideConversations func(map[string]struct{}) bool) codextea.AgentsOverviewRefreshFunc {
	source := newEmbeddedAgentsDashboardSource(store, closeSideConversations)
	return func(string) ([]agentsoverview.Row, error) {
		return source.List(context.Background())
	}
}

func embeddedAgentsOverviewNewSession(store *session.Store) codextea.AgentsOverviewNewSessionFunc {
	source := newEmbeddedAgentsDashboardSource(store, nil)
	return func(cwd string) (codextea.AgentThreadSwitchResponse, error) {
		started, err := source.StartSession(cwd)
		if err != nil {
			return codextea.AgentThreadSwitchResponse{}, err
		}
		return remoteTUIAgentSwitchResponseForStartedSession(started), nil
	}
}

func embeddedAgentsOverviewStop(store *session.Store) codextea.AgentsOverviewStopFunc {
	source := newEmbeddedAgentsDashboardSource(store, nil)
	return func(threadID string) error { return source.Stop(context.Background(), threadID) }
}

func embeddedAgentsOverviewRename(store *session.Store) codextea.AgentsOverviewRenameFunc {
	source := newEmbeddedAgentsDashboardSource(store, nil)
	return func(threadID string, name string) error { return source.Rename(context.Background(), threadID, name) }
}

func embeddedAgentsOverviewArchive(store *session.Store, closeSideConversations func(map[string]struct{}) bool) codextea.AgentsOverviewArchiveFunc {
	source := newEmbeddedAgentsDashboardSource(store, closeSideConversations)
	return func(threadID string) error { return source.Archive(context.Background(), threadID) }
}

func embeddedAgentsOverviewDelete(store *session.Store, closeSideConversations func(map[string]struct{}) bool) codextea.AgentsOverviewDeleteFunc {
	source := newEmbeddedAgentsDashboardSource(store, closeSideConversations)
	return func(threadID string) error { return source.Delete(context.Background(), threadID) }
}
