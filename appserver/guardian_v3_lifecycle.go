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
	"codex_go/utils"
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
	decider, sink, err := r.guardianV3Decider(cfg)
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
		Housekeeper: guardianV3HousekeeperOptions(cfg, sink),
		Decider:     decider,
	}, nil
}

// guardianV3HousekeeperOptions carries the routing batch budget: the configured
// batch deadline, the sampler that retains each committed batch, and the model's
// tool-output budget as the batch-size cap (Rust #62372 sizes a routing batch by
// the model input window; the host's model budget is the equivalent figure it
// can resolve here).
func guardianV3HousekeeperOptions(cfg *config.Config, sink agentboard.GuardianRoutingBatchSink) agentboard.GuardianHousekeeperOptions {
	options := agentboard.GuardianHousekeeperOptions{BatchSink: sink}
	if cfg == nil {
		return options
	}
	options.BatchTimeout = cfg.GuardianV3HousekeeperBatchTimeout()
	if limit := cfg.ToolOutputTokenLimit(); limit != nil && *limit > 0 {
		options.InputBudget = utils.TokensPolicy(*limit)
	}
	return options
}

// guardianV3Decider builds the routing decider from the configured guidance and
// the host sampler. A sampler that retains a tool-free conversation also
// receives the batch outcome, so its history follows the board's durable state.
func (r *RuntimeRouter) guardianV3Decider(cfg *config.Config) (agentboard.GuardianRoutingDecider, agentboard.GuardianRoutingBatchSink, error) {
	sampler := r.guardianV3Sampler
	var sink agentboard.GuardianRoutingBatchSink
	if sampler == nil {
		housekeeperSampler, err := r.guardianV3HousekeeperSampler(cfg)
		if err != nil {
			return nil, nil, err
		}
		if housekeeperSampler != nil {
			sampler = housekeeperSampler.Sample
			sink = housekeeperSampler
		}
	}
	if sampler == nil {
		return nil, nil, errors.New("Guardian routing has no sampler")
	}
	instructions, _ := cfg.GuardianV3RoutingInstructions()
	decider, err := NewGuardianRoutingDecider(sampler, GuardianV3DeciderOptions{RoutingInstructions: instructions})
	if err != nil {
		return nil, nil, err
	}
	return decider, sink, nil
}

// guardianV3HousekeeperSampler builds one root's dedicated routing sampler: a
// tool-free conversation over the Guardian inference route that retains
// completed batches, evicts whole batches under input pressure, renews the
// credential per batch and cancels a pass when the owning account changes
// (Rust #61798/#61799).
func (r *RuntimeRouter) guardianV3HousekeeperSampler(cfg *config.Config) (*guardianV3HousekeeperSampler, error) {
	if r == nil || cfg == nil {
		return nil, nil
	}
	if _, ok := r.guardianInferenceProvider(); !ok {
		return nil, nil
	}
	options := GuardianV3HousekeeperSamplerOptions{AccountWatch: r.guardianV3AccountWatch()}
	if limit := cfg.ToolOutputTokenLimit(); limit != nil && *limit > 0 {
		options.InputLimit = *limit
	}
	return newGuardianV3HousekeeperSampler(func() (model.AgentRunner, string, error) {
		runner, err := r.guardianInferenceAgent(cfg)
		if err != nil {
			return nil, "", err
		}
		provider, _ := r.guardianInferenceProvider()
		return runner, provider.ID, nil
	}, options), nil
}

// guardianV3AccountWatch cancels a routing pass when the credential revision
// that started it is replaced. A router without a credential tracker keeps no
// owner, so routing is never cancelled for an account change.
func (r *RuntimeRouter) guardianV3AccountWatch() agentboard.GuardianSamplerAccountWatch {
	if r == nil {
		return agentboard.GuardianSamplerAccountWatch{}
	}
	revision, err := r.authRevisionSnapshot(context.Background())
	if err != nil {
		return agentboard.GuardianSamplerAccountWatch{}
	}
	changes := r.authChangedChannel()
	current := func() bool {
		now, err := r.authRevisionSnapshot(context.Background())
		return err == nil && now == revision
	}
	return agentboard.NewGuardianSamplerAccountWatch(current, changes)
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
