package tea

// Server-backed experimental-feature discovery, porting Rust
// tui/src/experimental_features.rs + the discovery half of
// bottom_pane/experimental_features_view.rs: the /experimental popup is
// populated from the app server's experimentalFeature/list catalog (Beta stage
// only), not from the compiled feature registry.

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/features"
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
	experimentalFeaturesSavingStatus  = "Saving experimental features…"
	// experimentalFeaturesOverriddenStatus mirrors Rust's
	// experimental_features::write readback warning.
	experimentalFeaturesOverriddenStatus = "Changes were saved, but the configured values differ from your selections. A higher-priority setting may override them."
)

// The two unmigrated controls keep their own writer in Rust
// (save_special_controls); their edits are never retried through the generic
// save path.
const (
	experimentalFeaturePreventIdleSleepKey = "prevent_idle_sleep"
	experimentalFeatureGuardianApprovalKey = "guardian_approval"
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
			Writable:       experimentalFeatureWritable(name, entry.DefaultEnabled),
		})
	}
	return out
}

// experimentalFeatureWritable ports Rust ExperimentalFeaturesView's writable
// rule: the unmigrated controls are read-only unless the compiled registry still
// advertises them with a matching default.
func experimentalFeatureWritable(name string, defaultEnabled bool) bool {
	if name != experimentalFeaturePreventIdleSleepKey && name != experimentalFeatureGuardianApprovalKey {
		return true
	}
	for _, spec := range features.Registry {
		if spec.Key != name {
			continue
		}
		if spec.Stage == features.StageExperimental &&
			strings.TrimSpace(spec.ExperimentalName) != "" &&
			spec.DefaultEnabled == defaultEnabled {
			return true
		}
	}
	return false
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

// setExperimentalItems installs the popup rows and rebases the save baseline
// (Rust initial_enabled).
func (m *Model) setExperimentalItems(items []chatwidget.ExperimentalFeatureOption) {
	if m == nil {
		return
	}
	m.experimentalItems = items
	m.experimentalFeaturesSaving = false
	m.experimentalFeatureUnconfirmed = nil
	baseline := make(map[string]bool, len(items))
	for _, item := range items {
		if key := strings.TrimSpace(item.Key); key != "" {
			baseline[key] = item.Enabled
		}
	}
	m.experimentalFeatureBaseline = baseline
}

// experimentalFeaturesHaveUpdates mirrors Rust save_changes' update predicate: a
// writable row differs from the baseline or is still unconfirmed.
func (m *Model) experimentalFeaturesHaveUpdates() bool {
	for _, item := range m.experimentalItems {
		key := strings.TrimSpace(item.Key)
		if key == "" || !item.Writable {
			continue
		}
		if baseline, ok := m.experimentalFeatureBaseline[key]; !ok || baseline != item.Enabled {
			return true
		}
		if experimentalFeatureUnconfirmedContains(m.experimentalFeatureUnconfirmed, key) {
			return true
		}
	}
	return false
}

// saveExperimentalFeatures mirrors Rust ExperimentalFeaturesView::save_changes:
// the dirty writable rows are written (the unmigrated controls through their own
// edit rule and never retried generically) and remembered as unconfirmed while
// the popup stays open.
func (m *Model) saveExperimentalFeatures() bubbletea.Cmd {
	if m == nil || m.onWriteSettings == nil || m.experimentalFeaturesSaving {
		return nil
	}
	edits := make([]SettingsEdit, 0, len(m.experimentalItems))
	requested := make(map[string]bool, len(m.experimentalItems))
	unconfirmed := make([]string, 0, len(m.experimentalItems))
	if m.experimentalFeatureBaseline == nil {
		m.experimentalFeatureBaseline = map[string]bool{}
	}
	for _, item := range m.experimentalItems {
		key := strings.TrimSpace(item.Key)
		if key == "" || !item.Writable {
			continue
		}
		baseline, known := m.experimentalFeatureBaseline[key]
		dirty := (known && baseline != item.Enabled) ||
			experimentalFeatureUnconfirmedContains(m.experimentalFeatureUnconfirmed, key)
		if !dirty {
			continue
		}
		requested[key] = item.Enabled
		edits = append(edits, experimentalFeatureEdit(item))
		if key == experimentalFeaturePreventIdleSleepKey || key == experimentalFeatureGuardianApprovalKey {
			// Rust save_special_controls resets the special controls' baseline and
			// never retries them through the generic path.
			m.experimentalFeatureBaseline[key] = item.Enabled
			continue
		}
		unconfirmed = append(unconfirmed, key)
	}
	if len(edits) == 0 {
		return nil
	}
	m.experimentalFeaturesSaving = true
	m.experimentalFeatureUnconfirmed = unconfirmed
	m.experimentalFeaturesStatus = experimentalFeaturesSavingStatus
	m.pendingExperimentalFeatureUpdates = requested
	m.refreshExperimentalModal()
	return m.writeSettings(settingsWriteKindExperimental, edits)
}

// applyExperimentalWriteReadback mirrors Rust ExperimentalFeaturesView's
// write-result handling: the readback enablement replaces the selections (the
// unmigrated controls are skipped), the unconfirmed keys clear, and the popup
// closes unless the readback warned.
func (m *Model) applyExperimentalWriteReadback(overridden bool) {
	if m == nil {
		return
	}
	m.experimentalFeaturesSaving = false
	m.experimentalFeatureUnconfirmed = nil
	if m.experimentalFeatureBaseline == nil {
		m.experimentalFeatureBaseline = map[string]bool{}
	}
	for index := range m.experimentalItems {
		item := m.experimentalItems[index]
		key := strings.TrimSpace(item.Key)
		if key == "" || key == experimentalFeaturePreventIdleSleepKey || key == experimentalFeatureGuardianApprovalKey {
			continue
		}
		item.Enabled = features.Enabled(m.featureSettings, key)
		m.experimentalItems[index] = item
		m.experimentalFeatureBaseline[key] = item.Enabled
	}
	if overridden {
		m.experimentalFeaturesStatus = experimentalFeaturesOverriddenStatus
		m.refreshExperimentalModal()
		return
	}
	m.experimentalFeaturesStatus = ""
	if m.modal != nil && m.modal.kind == ModalKindExperimental {
		m.modal = nil
		m.refreshTranscript()
	}
}

func experimentalFeatureUnconfirmedContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// experimentalFeatureEdit builds the config edit for one feature row. Enabling
// writes true; disabling a default-enabled feature clears the override (Rust
// experimental_features::write), and the unmigrated controls follow Rust's
// build_feature_enabled_edit instead (clear when disabling a default-off
// feature).
func experimentalFeatureEdit(item chatwidget.ExperimentalFeatureOption) SettingsEdit {
	key := strings.TrimSpace(item.Key)
	value := any(item.Enabled)
	if key == experimentalFeaturePreventIdleSleepKey || key == experimentalFeatureGuardianApprovalKey {
		if !item.Enabled && !item.DefaultEnabled {
			value = nil
		}
		return SettingsEdit{KeyPath: "features." + key, Value: value}
	}
	if !item.Enabled && item.DefaultEnabled {
		value = nil
	}
	return SettingsEdit{KeyPath: "features." + key, Value: value}
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
		m.setExperimentalItems(nil)
		m.experimentalFeaturesStatus = experimentalFeaturesEmptyStatus
	default:
		m.setExperimentalItems(experimentalFeatureOptionsFromCatalog(message.Features))
		m.experimentalFeaturesStatus = ""
	}
	m.refreshExperimentalModal()
}
