package appserver

import (
	"context"
	"strings"

	"codex_go/model"
	"codex_go/session"
)

// guardianCatalogRefreshAttempt is Rust #57789's GuardianModelCatalogRefresh:
// one shared model-catalog refresh per parent thread. The refresh runs on its
// own goroutine so a cancelled waiter cannot abort it, every later waiter joins
// the same attempt, and a failed fetch still completes the attempt (the caller
// keeps the parent fallback).
type guardianCatalogRefreshAttempt struct {
	done chan struct{}
}

// guardianCatalogRefreshCompleted reports whether the thread's single refresh
// attempt already finished (Rust `refresh.0.peek().is_some()`). A lookup that
// races a finishing refresh still re-resolves; later lookups do not retry.
func (r *RuntimeRouter) guardianCatalogRefreshCompleted(threadID string) bool {
	if r == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	r.guardianCatalogRefreshMu.Lock()
	defer r.guardianCatalogRefreshMu.Unlock()
	attempt := r.guardianCatalogRefreshes[threadID]
	if attempt == nil {
		return false
	}
	select {
	case <-attempt.done:
		return true
	default:
		return false
	}
}

// ensureGuardianCatalogRefresh starts the thread's catalog refresh or joins the
// one already running. It returns once the attempt finished or ctx ended; the
// attempt itself keeps running after a cancelled waiter.
func (r *RuntimeRouter) ensureGuardianCatalogRefresh(ctx context.Context, threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.guardianCatalogRefreshMu.Lock()
	if r.guardianCatalogRefreshes == nil {
		r.guardianCatalogRefreshes = map[string]*guardianCatalogRefreshAttempt{}
	}
	attempt := r.guardianCatalogRefreshes[threadID]
	if attempt == nil {
		attempt = &guardianCatalogRefreshAttempt{done: make(chan struct{})}
		r.guardianCatalogRefreshes[threadID] = attempt
		models := r.requireModels()
		go func() {
			defer close(attempt.done)
			// Rust spawns the fetch through AbortOnDropHandle and awaits it in the
			// shared future; the request keeps no caller context.
			models.Presets(model.RefreshOnline)
		}()
	}
	r.guardianCatalogRefreshMu.Unlock()
	if ctx == nil {
		<-attempt.done
		return
	}
	select {
	case <-attempt.done:
	case <-ctx.Done():
	}
}

// forgetGuardianCatalogRefresh drops a thread's shared refresh attempt with its
// runtime, like Rust's session extension data (Rust #57789).
func (r *RuntimeRouter) forgetGuardianCatalogRefresh(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	r.guardianCatalogRefreshMu.Lock()
	delete(r.guardianCatalogRefreshes, threadID)
	r.guardianCatalogRefreshMu.Unlock()
}

// guardianReviewCatalogModels mirrors Rust #57770's origin filter: a bundled
// reviewer entry does not establish that a custom gateway offers that model, so
// bundled entries are dropped unless the parent model set an explicit
// auto_review_model_override. Provider and explicitly configured entries stay.
func guardianReviewCatalogModels(catalog []model.CatalogModel, parent *model.ModelInfo) []model.ModelInfo {
	allowBundled := parent != nil && strings.TrimSpace(parent.AutoReviewModelOverride) != ""
	models := make([]model.ModelInfo, 0, len(catalog))
	for _, entry := range catalog {
		if entry.Origin == model.ModelOriginBundled && !allowBundled {
			continue
		}
		models = append(models, entry.Model)
	}
	return models
}

// guardianReviewModelNeedsCatalogRefresh mirrors Rust #57789's retry guard: only
// a missing preferred reviewer, from the built-in catalog, for a non-Guardian
// session is worth one refresh.
func guardianReviewModelNeedsCatalogRefresh(selection model.ApprovalReviewModel, cfgHasModelCatalogJSON bool, provider *model.ProviderInfo, guardianSession bool) bool {
	if guardianSession {
		return false
	}
	if selection.DefaultReviewModelID != model.DefaultApprovalReviewPreferredModel {
		return false
	}
	if selection.ModelOverridden || selection.CatalogContainsAutoReview {
		return false
	}
	if cfgHasModelCatalogJSON {
		return false
	}
	if provider != nil && strings.TrimSpace(provider.ModelCatalogURL) != "" {
		return false
	}
	return true
}

// guardianThreadIsBasicSessionSource reports whether the thread is a Guardian
// review session (Rust is_basic_session_source): those never refresh the
// catalog for their own reviewer.
func (r *RuntimeRouter) guardianThreadIsBasicSessionSource(record *session.Record) bool {
	if record == nil {
		return false
	}
	return session.IsGuardianReviewSource(record.Metadata.Source)
}
