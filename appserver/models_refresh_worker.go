package appserver

import (
	"context"
	"log/slog"
	"time"

	"codex_go/model"
)

// modelsRefreshInterval mirrors Rust #40498's
// MODELS_REFRESH_INTERVAL (4 minutes 30 seconds).
const modelsRefreshInterval = 4*time.Minute + 30*time.Second

// modelsRefreshWorker mirrors codex-rs/app-server/src/models_refresh_worker.rs:
// refresh the online model catalog immediately, then once per interval until
// the owning router shuts down.
type modelsRefreshWorker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// shutdown stops future refreshes without waiting for an in-flight online
// refresh, matching Rust's cancel-and-drop worker teardown.
func (w *modelsRefreshWorker) shutdown() {
	if w == nil {
		return
	}
	w.cancel()
}

// wait blocks until the worker loop exits; tests use it to observe shutdown.
func (w *modelsRefreshWorker) wait() {
	if w == nil {
		return
	}
	<-w.done
}

func (r *RuntimeRouter) spawnModelsRefreshWorker(interval time.Duration) *modelsRefreshWorker {
	if r == nil {
		return nil
	}
	if interval <= 0 {
		interval = modelsRefreshInterval
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &modelsRefreshWorker{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(worker.done)
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			r.refreshModelCatalogOnline()
			timer.Reset(interval)
		}
	}()
	return worker
}

// refreshModelCatalogOnline mirrors ModelCatalog::list_models(RefreshStrategy::
// Online). Parser diagnostics can contain provider credentials, so only the
// bounded failure classification is logged, as upstream does.
func (r *RuntimeRouter) refreshModelCatalogOnline() {
	if r == nil {
		return
	}
	r.servicesMu.Lock()
	service := r.services.Models
	r.servicesMu.Unlock()
	if service == nil {
		return
	}
	if _, err := service.List(&model.ModelListParams{RefreshStrategy: string(model.RefreshOnline)}); err != nil {
		slog.Warn("model catalog refresh blocked by provider requirements")
	}
}
