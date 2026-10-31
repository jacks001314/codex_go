package appserver

import (
	"context"
	"strings"

	execserverclient "codex_go/execserver"
)

// runtimeEmptyRemoteSkillWatch owns the persistent executor connection that
// keeps an empty remote selected skill root under observation (Rust #57598).
type runtimeEmptyRemoteSkillWatch struct {
	client *execserverclient.Client
}

// remoteSelectedSkillCatalog returns the cached discovery for one selected
// remote skill root. An empty, complete discovery installs a filesystem watch
// so a later skill directory creation invalidates only this catalog
// generation instead of leaving the empty result cached forever.
func (r *RuntimeRouter) remoteSelectedSkillCatalog(ctx context.Context, threadID string, cacheKey string, selected SelectedCapabilityRoot, record *EnvironmentRecord, sandboxContext *execserverclient.FileSystemSandboxContext) ([]SkillsListEntry, []string, bool) {
	cached := r.selectedCapabilitySkillCatalog(threadID, cacheKey)
	cached.once.Do(func() {
		cached.entries, cached.warnings, cached.err = discoverRemoteEnvironmentSkillsWithSandbox(ctx, record, selected.Location.Path, sandboxContext)
		if cached.err != nil {
			cached.partial = true
			cached.warnings = append(cached.warnings, "executor skills unavailable: "+cached.err.Error())
			cached.err = nil
		}
		applyExecutorSkillDisplayPaths(cached.entries, selected.ID)
		if len(cached.entries) == 0 && len(cached.warnings) == 0 && !cached.partial {
			r.observeEmptyRemoteSkillRoot(threadID, cacheKey, cached, record, selected.Location.Path)
		}
	})
	return cloneSkills(cached.entries), cached.warnings, cached.partial
}

// observeEmptyRemoteSkillRoot registers fs/watchMissing in the background, as
// Rust does, so the cold discovery path does not pay an extra round trip.
func (r *RuntimeRouter) observeEmptyRemoteSkillRoot(threadID string, cacheKey string, entry *runtimeSelectedSkillCatalog, record *EnvironmentRecord, rootPath string) {
	if r == nil || record == nil {
		return
	}
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return
	}
	watchCtx, _ := r.emptyRemoteSkillWatchContext()
	watchKey := threadID + "\x00" + cacheKey
	go func() {
		dialCtx, cancel := context.WithTimeout(watchCtx, environmentConnectDeadlineForRecord(record))
		client, err := dialEmptyRemoteSkillWatchClient(dialCtx, record)
		if err != nil {
			cancel()
			return
		}
		capabilities, err := client.EnvironmentCapabilities(dialCtx)
		if err != nil || capabilities == nil || !capabilities.FSWatchMissing {
			cancel()
			_ = client.Close()
			return
		}
		response, changed, err := client.WatchMissing(dialCtx, &execserverclient.FSWatchMissingParams{
			WatchID: 1,
			Missing: []string{rootPath},
		})
		cancel()
		if err != nil {
			_ = client.Close()
			return
		}
		if response == nil || !response.Watching || changed == nil {
			_ = client.Close()
			// The root appeared before the observation was installed. Drop the
			// empty catalog so the next lookup rescans. Executors without the
			// capability are rejected above and keep ordinary cache behavior.
			r.invalidateSelectedSkillCatalogEntry(threadID, cacheKey, entry)
			return
		}
		watch := &runtimeEmptyRemoteSkillWatch{client: client}
		r.emptyRemoteSkillWatchMu.Lock()
		if r.emptyRemoteSkillWatches == nil {
			r.emptyRemoteSkillWatches = map[string]*runtimeEmptyRemoteSkillWatch{}
		}
		previous := r.emptyRemoteSkillWatches[watchKey]
		r.emptyRemoteSkillWatches[watchKey] = watch
		r.emptyRemoteSkillWatchMu.Unlock()
		if previous != nil && previous != watch {
			_ = previous.client.Close()
		}
		select {
		case <-watchCtx.Done():
		case <-changed:
			r.invalidateSelectedSkillCatalogEntry(threadID, cacheKey, entry)
		}
		r.forgetEmptyRemoteSkillWatch(watchKey, watch)
		_ = client.Close()
	}()
}

func dialEmptyRemoteSkillWatchClient(ctx context.Context, record *EnvironmentRecord) (*execserverclient.Client, error) {
	switch {
	case record.NoiseProvider != nil:
		return execserverclient.DialNoiseRendezvousClient(ctx, record.NoiseProvider, provisionedNoiseDialOptions(record, "codex-go-skills-watch"))
	case recordUsesStdioTransport(record):
		return execserverclient.DialClientWithOptions(ctx, "", execServerClientOptions(record, "codex-go-skills-watch"))
	default:
		return execserverclient.DialClientWithOptions(ctx, strings.TrimSpace(record.ExecServerURL), execServerClientOptions(record, "codex-go-skills-watch"))
	}
}

func (r *RuntimeRouter) invalidateSelectedSkillCatalogEntry(threadID string, cacheKey string, entry *runtimeSelectedSkillCatalog) {
	r.selectedSkillMu.Lock()
	defer r.selectedSkillMu.Unlock()
	byRoot := r.selectedSkills[threadID]
	if byRoot == nil || byRoot[cacheKey] != entry {
		return
	}
	delete(byRoot, cacheKey)
	if len(byRoot) == 0 {
		delete(r.selectedSkills, threadID)
	}
}

func (r *RuntimeRouter) emptyRemoteSkillWatchContext() (context.Context, context.CancelFunc) {
	r.emptyRemoteSkillWatchMu.Lock()
	defer r.emptyRemoteSkillWatchMu.Unlock()
	if r.emptyRemoteSkillWatchCtx == nil {
		r.emptyRemoteSkillWatchCtx, r.emptyRemoteSkillWatchCancel = context.WithCancel(context.Background())
	}
	return r.emptyRemoteSkillWatchCtx, r.emptyRemoteSkillWatchCancel
}

func (r *RuntimeRouter) forgetEmptyRemoteSkillWatch(key string, watch *runtimeEmptyRemoteSkillWatch) {
	r.emptyRemoteSkillWatchMu.Lock()
	if r.emptyRemoteSkillWatches[key] == watch {
		delete(r.emptyRemoteSkillWatches, key)
	}
	r.emptyRemoteSkillWatchMu.Unlock()
}

func (r *RuntimeRouter) stopEmptyRemoteSkillWatches() {
	if r == nil {
		return
	}
	r.emptyRemoteSkillWatchMu.Lock()
	cancel := r.emptyRemoteSkillWatchCancel
	watches := r.emptyRemoteSkillWatches
	r.emptyRemoteSkillWatches = nil
	r.emptyRemoteSkillWatchCancel = nil
	r.emptyRemoteSkillWatchCtx = nil
	r.emptyRemoteSkillWatchMu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, watch := range watches {
		if watch != nil && watch.client != nil {
			_ = watch.client.Close()
		}
	}
}
