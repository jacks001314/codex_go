package appserver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"codex_go/plugin"
)

// Published catalog size metrics. Rust #56530 records one histogram sample per
// published skill or plugin catalog, tagged by the catalog stage (`cloud` or
// `executor`) and the publication status (`complete`, `warning`, or `partial`).
const (
	skillCatalogEntriesMetric  = "codex.skills.catalog.entries"
	pluginCatalogEntriesMetric = "codex.plugins.catalog.entries"

	catalogStageCloud    = "cloud"
	catalogStageExecutor = "executor"

	catalogStatusComplete = "complete"
	catalogStatusWarning  = "warning"
	catalogStatusPartial  = "partial"
)

// catalogEntriesBucketBoundaries mirrors Rust's record_catalog_entries
// boundaries.
var catalogEntriesBucketBoundaries = []float64{0, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000}

// catalogStatus mirrors Rust: a partial catalog outranks a warning because a
// warning can accompany a complete catalog.
func catalogStatus(partial bool, warningCount int) string {
	if partial {
		return catalogStatusPartial
	}
	if warningCount > 0 {
		return catalogStatusWarning
	}
	return catalogStatusComplete
}

// recordCatalogEntries publishes one catalog size sample
// (Rust record_catalog_entries).
func (r *RuntimeRouter) recordCatalogEntries(name string, stage string, entries int, partial bool, warningCount int) {
	if r == nil || r.metrics == nil {
		return
	}
	r.metrics.HistogramWithBounds(name, entries, catalogEntriesBucketBoundaries, map[string]string{
		"stage":  stage,
		"status": catalogStatus(partial, warningCount),
	})
}

// recordCatalogEntriesOnce drops repeated observations of an unchanged catalog.
// Rust #56530 excludes cached reads and repeated observations of the same
// failed discovery, which the executor catalogs would otherwise produce on
// every model step even though their discovery was reused.
func (r *RuntimeRouter) recordCatalogEntriesOnce(threadID string, name string, stage string, signature string, entries int, partial bool, warningCount int) {
	if r == nil || r.metrics == nil {
		return
	}
	key := strings.TrimSpace(threadID) + "\x00" + name
	r.catalogMetricsMu.Lock()
	if r.catalogMetricsSeen == nil {
		r.catalogMetricsSeen = map[string]string{}
	}
	previous, ok := r.catalogMetricsSeen[key]
	if ok && previous == signature {
		r.catalogMetricsMu.Unlock()
		return
	}
	r.catalogMetricsSeen[key] = signature
	r.catalogMetricsMu.Unlock()
	r.recordCatalogEntries(name, stage, entries, partial, warningCount)
}

func (r *RuntimeRouter) forgetCatalogMetrics(threadID string) {
	if r == nil {
		return
	}
	prefix := strings.TrimSpace(threadID) + "\x00"
	r.catalogMetricsMu.Lock()
	for key := range r.catalogMetricsSeen {
		if strings.HasPrefix(key, prefix) {
			delete(r.catalogMetricsSeen, key)
		}
	}
	r.catalogMetricsMu.Unlock()
}

// catalogSignature identifies a catalog observation without retaining it.
func catalogSignature(stage string, entries []string, warnings []string, partial bool) string {
	digest := sha256.New()
	fmt.Fprintf(digest, "%s\x00%t\x00", stage, partial)
	for _, entry := range entries {
		_, _ = io.WriteString(digest, entry)
		_, _ = digest.Write([]byte{0})
	}
	_, _ = io.WriteString(digest, "\x01")
	for _, warning := range warnings {
		_, _ = io.WriteString(digest, warning)
		_, _ = digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// recordExecutorSkillCatalog measures one executor skill catalog. Repeated
// steps that reuse the same discovery are not measured again (Rust #56530).
func (r *RuntimeRouter) recordExecutorSkillCatalog(threadID string, entries []SkillsListEntry, warnings []string, partial bool) {
	if r == nil {
		return
	}
	identities := make([]string, 0, len(entries))
	for _, entry := range entries {
		identities = append(identities, entry.Scope+"\x00"+entry.Name+"\x00"+entry.Path)
	}
	signature := catalogSignature(catalogStageExecutor, identities, warnings, partial)
	r.recordCatalogEntriesOnce(threadID, skillCatalogEntriesMetric, catalogStageExecutor, signature, len(entries), partial, len(warnings))
}

// recordExecutorPluginCatalog measures one executor plugin catalog. Rust
// records it from update_executor_catalog, which returns early for an unchanged
// discovery, so the signature keeps cached projections out of the samples.
func (r *RuntimeRouter) recordExecutorPluginCatalog(threadID string, catalog *plugin.PluginCatalog) {
	if r == nil || catalog == nil {
		return
	}
	identities := make([]string, 0, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		identity := entry.ID.AsString()
		for _, location := range entry.Locations {
			identity += "\x00" + string(location.Kind) + "\x00" + location.EnvironmentID + "\x00" + location.Root + "\x00" + location.PluginID
		}
		identities = append(identities, identity)
	}
	signature := catalogSignature(catalogStageExecutor, identities, catalog.Warnings, catalog.IsPartial)
	r.recordCatalogEntriesOnce(threadID, pluginCatalogEntriesMetric, catalogStageExecutor, signature, len(catalog.Entries), catalog.IsPartial, len(catalog.Warnings))
}
