package appserver

// Rust parity: codex-rs/ext/guardian-v3/src/host.rs install(). The runtime is
// the host: it decides which threads are roots, opens their evidence board and
// history reader, supplies the routing sampler, and wakes or stops the worker
// as turns end and threads leave.

import (
	"context"
	"errors"
	"strings"
	"time"

	"codex_go/agentboard"
	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/session"
)

const guardianV3StopTimeout = 5 * time.Second

// installGuardianV3Host wires one host to the thread lifecycle. It is a no-op
// for a router without a thread manager.
func (r *RuntimeRouter) installGuardianV3Host() {
	if r == nil || r.threads == nil {
		return
	}
	r.guardianV3Host = NewGuardianV3Host()
	r.threads.SetGuardianV3Lifecycle(r.guardianV3ThreadReady, r.guardianV3TurnEnded, r.guardianV3ThreadStopped)
}

// closeGuardianV3Host joins every routing worker before the runtime closes the
// thread stores its boards read from.
func (r *RuntimeRouter) closeGuardianV3Host() {
	if r == nil || r.guardianV3Host == nil {
		return
	}
	r.guardianV3Host.Close()
	r.guardianV3Host = nil
}

// guardianV3ThreadReady starts a root's routing worker when the feature is on.
// Child threads have no board of their own; the tree root owns the evidence.
func (r *RuntimeRouter) guardianV3ThreadReady(record *session.Record) {
	if r == nil || record == nil || r.guardianV3Host == nil {
		return
	}
	if strings.TrimSpace(string(record.ParentThreadID)) != "" {
		return
	}
	cfg := r.effectiveConfigForSessionTelemetry()
	if cfg == nil || !features.Enabled(cfg.FeatureSettings(), "guardianv3") {
		return
	}
	store := r.guardianV3ThreadStore()
	if store == nil {
		return
	}
	root := strings.TrimSpace(string(record.ID))
	if root == "" {
		return
	}
	if _, ok := r.guardianV3Host.Root(root); ok {
		return
	}
	start := guardianV3StartPosition(store, root)
	options, err := r.guardianV3RootOptions(cfg, root, store, start)
	if err != nil {
		return
	}
	_, _ = r.guardianV3Host.EnsureRoot(context.Background(), options)
}

// guardianV3RootOptions assembles one root's board, reader, start position,
// housekeeper budget and decider.
func (r *RuntimeRouter) guardianV3RootOptions(cfg *config.Config, root string, store *session.Store, start agentboard.GuardianHistoryPosition) (GuardianV3RootOptions, error) {
	sqliteConfig, err := r.messageBoardSqliteConfig(cfg)
	if err != nil {
		return GuardianV3RootOptions{}, err
	}
	decider, err := r.guardianV3Decider(cfg)
	if err != nil {
		return GuardianV3RootOptions{}, err
	}
	host := &messageBoardHost{router: r, tree: root, caller: root}
	return GuardianV3RootOptions{
		Root:   root,
		Reader: GuardianStoreEvidenceReader(store),
		Start:  start,
		Board: func(ctx context.Context) (agentboard.AgentMessageBoard, error) {
			return agentboard.NewGuardianLocalBoard(sqliteConfig, root).ReadWrite().Open(ctx, host)
		},
		Housekeeper: agentboard.GuardianHousekeeperOptions{
			BatchTimeout: cfg.GuardianV3HousekeeperBatchTimeout(),
		},
		Decider: decider,
	}, nil
}

// guardianV3Decider builds the routing decider from the configured guidance and
// the host sampler.
func (r *RuntimeRouter) guardianV3Decider(cfg *config.Config) (agentboard.GuardianRoutingDecider, error) {
	sampler := r.guardianV3Sampler
	if sampler == nil {
		sampler = r.guardianV3ModelSampler(cfg)
	}
	if sampler == nil {
		return nil, errors.New("Guardian routing has no sampler")
	}
	instructions, _ := cfg.GuardianV3RoutingInstructions()
	return NewGuardianRoutingDecider(sampler, GuardianV3DeciderOptions{RoutingInstructions: instructions})
}

// guardianV3ModelSampler runs one tool-free routing request on the Guardian
// inference route.
func (r *RuntimeRouter) guardianV3ModelSampler(cfg *config.Config) GuardianRoutingSampler {
	if r == nil || cfg == nil {
		return nil
	}
	provider, _ := r.guardianInferenceProvider()
	return func(ctx context.Context, instructions string, input string) (string, error) {
		runner, err := r.guardianInferenceAgent(cfg)
		if err != nil {
			return "", err
		}
		if runner == nil {
			return "", errors.New("Guardian routing sampler is unavailable")
		}
		response, err := runner.Run(ctx, &model.AgentRequest{
			Instructions: instructions,
			Prompt:       input,
			ProviderID:   provider.ID,
		})
		if err != nil {
			return "", err
		}
		if response == nil {
			return "", errors.New("Guardian routing sampler returned no response")
		}
		return response.Message, nil
	}
}

// guardianV3TurnEnded wakes the root that owns a finished turn, so the turn's
// originals route once the coalesced wake fires.
func (r *RuntimeRouter) guardianV3TurnEnded(threadID string) {
	if r == nil || r.guardianV3Host == nil {
		return
	}
	root := r.guardianV3RootForThread(threadID)
	if root == "" {
		return
	}
	r.guardianV3Host.Wake(root)
}

// guardianV3ThreadStopped stops a root whose thread left the process. Child
// threads map onto their root and leave the worker running for the tree.
func (r *RuntimeRouter) guardianV3ThreadStopped(threadID string) {
	if r == nil || r.guardianV3Host == nil {
		return
	}
	root := r.guardianV3RootForThread(threadID)
	if root == "" {
		return
	}
	if _, ok := r.guardianV3Host.Root(root); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), guardianV3StopTimeout)
	defer cancel()
	_ = r.guardianV3Host.StopRoot(ctx, root)
}

// guardianV3RootForThread walks the parent chain to the tree root.
func (r *RuntimeRouter) guardianV3RootForThread(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	store := r.guardianV3ThreadStore()
	if store == nil {
		return threadID
	}
	seen := map[string]struct{}{}
	for threadID != "" {
		if _, ok := seen[threadID]; ok {
			return threadID
		}
		seen[threadID] = struct{}{}
		record, err := store.Read(session.ThreadID(threadID), true, false)
		if err != nil || record == nil {
			return threadID
		}
		parent := strings.TrimSpace(string(record.ParentThreadID))
		if parent == "" {
			return threadID
		}
		threadID = parent
	}
	return ""
}

// guardianV3ThreadStore is the thread store the evidence reader reads.
func (r *RuntimeRouter) guardianV3ThreadStore() *session.Store {
	if r == nil || r.services.ThreadRouter == nil {
		return nil
	}
	return r.services.ThreadRouter.store
}

// guardianV3StartPosition resumes at the durable tail and starts a new root at
// the beginning (Rust EvidenceCursor::at_thread_ready).
func guardianV3StartPosition(store *session.Store, root string) agentboard.GuardianHistoryPosition {
	start := agentboard.GuardianHistoryPosition{ThreadID: root}
	if store == nil {
		return start
	}
	tail, err := ReadGuardianOriginalHistory(store, root, GuardianOriginalHistoryTail(root))
	if err != nil || tail.End.EndOrdinalExclusive == 0 {
		return start
	}
	return tail.End
}
