package appserver

import (
	"context"
	"errors"
	"strings"
	"sync"

	"codex_go/config"
	execserverclient "codex_go/execserver"
	"codex_go/features"
	"codex_go/turn"
)

// runtimeCapabilityDiscoveryV2Cache owns the clients and bounded V2 inventory
// cache for one turn. A replacement turn gets a fresh cache so every model step
// observes the same inventory until the turn ends (Rust #53736).
type runtimeCapabilityDiscoveryV2Cache struct {
	cache *execserverclient.ExecutorCapabilityDiscoveryV2Cache

	mu      sync.Mutex
	clients map[string]*execserverclient.Client

	executorMCPMu        sync.Mutex
	executorMCPDiscovery map[string]executorMCPDiscoveryResult
}

func newRuntimeCapabilityDiscoveryV2Cache() *runtimeCapabilityDiscoveryV2Cache {
	return &runtimeCapabilityDiscoveryV2Cache{
		cache:   &execserverclient.ExecutorCapabilityDiscoveryV2Cache{},
		clients: map[string]*execserverclient.Client{},
	}
}

type executorCapabilityDiscoveryV2Target struct {
	service *runtimeCapabilityDiscoveryV2Cache
	record  EnvironmentRecord
}

func (t *executorCapabilityDiscoveryV2Target) CapabilityDiscoveryInstanceID() string {
	if t == nil {
		return ""
	}
	if instanceID := strings.TrimSpace(t.record.InstanceID); instanceID != "" {
		return instanceID
	}
	return strings.TrimSpace(t.record.EnvironmentID) + "\x00" + strings.TrimSpace(t.record.ExecServerURL)
}

func (t *executorCapabilityDiscoveryV2Target) CapabilityDiscoverySessionID(ctx context.Context) (string, error) {
	return t.service.executorSessionID(ctx, t.record)
}

func (s *runtimeCapabilityDiscoveryV2Cache) executorSessionID(ctx context.Context, record EnvironmentRecord) (string, error) {
	client, err := s.client(ctx, record)
	if err != nil {
		return "", err
	}
	return client.SessionID(), nil
}

func (t *executorCapabilityDiscoveryV2Target) DiscoverCapabilitiesV2(ctx context.Context, request *execserverclient.DiscoverV2CapabilitiesParams) (*execserverclient.DiscoverV2CapabilitiesResponse, error) {
	client, err := t.service.client(ctx, t.record)
	if err != nil {
		return nil, err
	}
	return client.DiscoverCapabilitiesV2(ctx, request)
}

func (s *runtimeCapabilityDiscoveryV2Cache) client(ctx context.Context, record EnvironmentRecord) (*execserverclient.Client, error) {
	if s == nil {
		return nil, errors.New("executor discovery V2 cache is nil")
	}
	key := environmentDiscoveryV2InstanceKey(record)
	s.mu.Lock()
	if client := s.clients[key]; client != nil {
		s.mu.Unlock()
		return client, nil
	}
	s.mu.Unlock()

	connectCtx, cancel := context.WithTimeout(ctx, environmentConnectDeadlineForRecord(&record))
	defer cancel()
	var client *execserverclient.Client
	var err error
	switch {
	case record.NoiseProvider != nil:
		options := provisionedNoiseDialOptions(&record, "codex-go-capability-discovery-v2")
		options.Provisioned = record.provisionedReady()
		client, err = execserverclient.DialNoiseRendezvousClient(connectCtx, record.NoiseProvider, options)
	case recordUsesStdioTransport(&record):
		client, err = execserverclient.DialClientWithOptions(connectCtx, "", execServerClientOptions(&record, "codex-go-capability-discovery-v2"))
	default:
		client, err = execserverclient.DialClientWithOptions(connectCtx, strings.TrimSpace(record.ExecServerURL), execServerClientOptions(&record, "codex-go-capability-discovery-v2"))
	}
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if existing := s.clients[key]; existing != nil {
		s.mu.Unlock()
		_ = client.Close()
		return existing, nil
	}
	if s.clients == nil {
		s.clients = map[string]*execserverclient.Client{}
	}
	s.clients[key] = client
	s.mu.Unlock()
	return client, nil
}

func environmentDiscoveryV2InstanceKey(record EnvironmentRecord) string {
	if instanceID := strings.TrimSpace(record.InstanceID); instanceID != "" {
		return instanceID
	}
	return strings.TrimSpace(record.EnvironmentID) + "\x00" + strings.TrimSpace(record.ExecServerURL)
}

// executorCapabilityDiscoveryV2ForStep acquires inert V2 inventories for the
// selected remote environments without loading their plugins or skills into the
// model. Unsupported executors keep their existing behavior.
func (r *RuntimeRouter) executorCapabilityDiscoveryV2ForStep(ctx context.Context, threadID string, params *turn.TurnStartParams, cfg *config.Config, sandboxContexts map[string]*execserverclient.FileSystemSandboxContext, cache *runtimeCapabilityDiscoveryV2Cache) *[]execserverclient.ExecutorCapabilityDiscoveryV2Snapshot {
	if r == nil || cfg == nil || cache == nil ||
		!features.Enabled(cfg.FeatureSettings(), "executor_capability_discovery_v2") ||
		r.services.Environment == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	selections := []map[string]any(nil)
	if params != nil {
		selections = params.Environments
	}
	if len(selections) == 0 {
		selections = r.threadEnvironmentSelections(threadID)
	}

	remoteEnvironmentCount := 0
	for _, selection := range selections {
		environmentID := selectionEnvironmentID(selection)
		if environmentID == "" || environmentID == execserverclient.LocalEnvironmentID {
			continue
		}
		if _, ok := r.services.Environment.Record(environmentID); ok {
			remoteEnvironmentCount++
		}
	}
	cache.cache.EnsureCapacity(remoteEnvironmentCount)

	snapshots := []execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{}
	skippedIncompatibleExecutor := false
	for _, selection := range selections {
		state, stateErr := environmentConfigStateFromAnyMap(selection)
		if stateErr == nil && (state.Kind == EnvironmentConfigPending || state.Kind == EnvironmentConfigFailed) {
			continue
		}
		environmentID := selectionEnvironmentID(selection)
		if environmentID == "" || environmentID == execserverclient.LocalEnvironmentID {
			continue
		}
		record, ok := r.services.Environment.Record(environmentID)
		if !ok || record == nil {
			continue
		}
		cwd := environmentSelectionCWD(selection)
		if cwd == "" && record.CWD != nil {
			cwd = strings.TrimSpace(*record.CWD)
		}
		if !strings.HasPrefix(cwd, "file://") {
			continue
		}
		target := &executorCapabilityDiscoveryV2Target{service: cache, record: *record}
		client, clientErr := cache.client(ctx, *record)
		if clientErr != nil {
			snapshots = append(snapshots, executorCapabilityDiscoveryV2Failure(environmentID))
			continue
		}
		supported, capabilityErr := client.SupportsCapabilityDiscoveryV2(ctx)
		if capabilityErr != nil {
			snapshots = append(snapshots, executorCapabilityDiscoveryV2Failure(environmentID))
			continue
		}
		if !supported {
			skippedIncompatibleExecutor = true
			continue
		}
		request := &execserverclient.DiscoverV2CapabilitiesParams{
			CWD:     cwd,
			Sandbox: sandboxContexts[environmentID],
		}
		response, discoverErr := cache.cache.Discover(ctx, environmentID, target, request)
		if discoverErr != nil {
			snapshots = append(snapshots, executorCapabilityDiscoveryV2Failure(environmentID))
			continue
		}
		snapshots = append(snapshots, execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{
			EnvironmentID: environmentID,
			Response:      response,
		})
	}
	if cache.cache.TakeRecoveredDiscovery() && r.mcpRuntimes != nil {
		r.mcpRuntimes.invalidateThread(strings.TrimSpace(threadID))
	}
	if len(snapshots) == 0 && skippedIncompatibleExecutor {
		// Rust #57192: an executor that only advertises the old V2 contract
		// must keep V1 discovery (and its selected roots/skills) active.
		return nil
	}
	return &snapshots
}

func executorCapabilityDiscoveryV2Failure(environmentID string) execserverclient.ExecutorCapabilityDiscoveryV2Snapshot {
	return execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{
		EnvironmentID: environmentID,
		Err:           "executor discovery V2 failed",
	}
}
