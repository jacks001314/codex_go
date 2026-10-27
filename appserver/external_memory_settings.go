package appserver

import (
	"context"
	"errors"
	"time"

	"codex_go/config"
)

// applyExternalMemoryImportSettings supplies the current memory pipeline and
// consolidation store to external-agent memory detection and import (Rust
// #56414/#56462). The app-server reads them per request, like Rust's
// ExternalAgentConfigRequestProcessor.
func (r *RuntimeRouter) applyExternalMemoryImportSettings(service *config.ConfigService) {
	if r == nil || service == nil {
		return
	}
	settings := config.MemoryImportSettings{
		Version:              config.MemoryVersionV1,
		EnqueueConsolidation: r.enqueueExternalMemoryConsolidation,
	}
	if cfg, err := r.effectiveConfigForThreadStart(nil); err == nil && cfg != nil {
		settings.Version = cfg.Memories().MemoryVersion()
	}
	service.SetMemoryImportSettings(settings)
}

func (r *RuntimeRouter) enqueueExternalMemoryConsolidation(ctx context.Context, version config.MemoryVersion) error {
	if r == nil || r.services.StateRuntime == nil {
		return errors.New("memory import requires the Codex state database")
	}
	store, err := r.services.StateRuntime.MemoryStoreForVersion(ctx, string(version))
	if err != nil {
		return err
	}
	return store.EnqueueGlobalConsolidation(ctx, time.Now().UTC().Unix())
}
