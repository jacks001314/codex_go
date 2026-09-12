package tea

// Server-backed experimental-feature discovery, porting Rust
// tui/src/experimental_features.rs + the discovery half of
// bottom_pane/experimental_features_view.rs: the /experimental popup is
// populated from the app server's experimentalFeature/list catalog (Beta stage
// only), not from the compiled feature registry.

import (
	"strings"

	"codex_go/tui/chatwidget"
)

// ExperimentalFeatureEntry mirrors one app-server experimentalFeature/list item
// (Rust ExperimentalFeature).
type ExperimentalFeatureEntry struct {
	Name           string
	DisplayName    string
	Description    string
	Enabled        bool
	DefaultEnabled bool
	Stage          string
}

// ExperimentalFeaturesReaderFunc reads the server's feature catalog for a thread
// (Rust experimental_features::fetch).
type ExperimentalFeaturesReaderFunc func(threadID string) ([]ExperimentalFeatureEntry, error)

// ExperimentalFeaturesResultMsg delivers one catalog read.
type ExperimentalFeaturesResultMsg struct {
	Generation uint64
	Features   []ExperimentalFeatureEntry
	Err        error
}

// Rust ExperimentalFeatureStage wire value for the only stage the popup lists.
const experimentalFeatureStageBeta = "beta"

const (
	// Rust experimental_features_view discovery_status strings.
	experimentalFeaturesLoadingStatus = "Loading server experiments…"
	experimentalFeaturesEmptyStatus   = "No server experiments available."
	experimentalFeaturesFailedStatus  = "Server experiments unavailable. Reopen /experimental to retry; restart this Codex client if requests remain unanswered."
)

// experimentalFeatureOptionsFromCatalog ports the catalog loop in Rust
// ExperimentalFeaturesView::pre_draw_tick: only Beta-stage features are listed,
// keyed by the feature name with the server's display/description metadata.
func experimentalFeatureOptionsFromCatalog(entries []ExperimentalFeatureEntry) []chatwidget.ExperimentalFeatureOption {
	out := make([]chatwidget.ExperimentalFeatureOption, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name == "" || entry.Stage != experimentalFeatureStageBeta {
			continue
		}
		out = append(out, chatwidget.ExperimentalFeatureOption{
			Key:            name,
			Name:           firstNonEmpty(strings.TrimSpace(entry.DisplayName), name),
			Description:    strings.TrimSpace(entry.Description),
			Enabled:        entry.Enabled,
			DefaultEnabled: entry.DefaultEnabled,
		})
	}
	return out
}

// experimentalModalBody renders the popup's body plus the discovery status line
// (Rust ExperimentalFeaturesView::header).
func experimentalModalBody(status string) string {
	body := "Toggle experimental features. Changes are saved to config.toml."
	if status = strings.TrimSpace(status); status != "" {
		body += "\n" + status
	}
	return body
}

// refreshExperimentalModal re-renders an open /experimental popup after the
// catalog or status changed.
func (m *Model) refreshExperimentalModal() {
	if m == nil || m.modal == nil || m.modal.kind != ModalKindExperimental {
		return
	}
	m.modal.options = experimentalModalOptions(m.experimentalItems)
	m.modal.body = experimentalModalBody(m.experimentalFeaturesStatus)
	m.modal.footerNote = ""
	m.modal.selected = 0
}

// applyExperimentalFeaturesResult ports Rust ExperimentalFeaturesView's catalog
// delivery: a stale generation is dropped, a failure keeps the current rows and
// shows the retry status, an empty catalog reports that no experiments are
// available, and a populated catalog replaces the rows.
func (m *Model) applyExperimentalFeaturesResult(message ExperimentalFeaturesResultMsg) {
	if m == nil || message.Generation != m.experimentalFeaturesGeneration {
		return
	}
	switch {
	case message.Err != nil:
		m.experimentalFeaturesStatus = experimentalFeaturesFailedStatus
	case len(experimentalFeatureOptionsFromCatalog(message.Features)) == 0:
		m.experimentalItems = nil
		m.experimentalFeaturesStatus = experimentalFeaturesEmptyStatus
	default:
		m.experimentalItems = experimentalFeatureOptionsFromCatalog(message.Features)
		m.experimentalFeaturesStatus = ""
	}
	m.refreshExperimentalModal()
}
