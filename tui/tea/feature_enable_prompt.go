package tea

// Rust parity: #43113 (fc748ab8d5) "Save subagent and memory opt-ins through the
// app server". tui/src/app/experimental_features.rs
// `enable_feature_for_new_threads` writes the opt-in through config/batchWrite
// and reports success, an override, or a failure afterwards; the current thread
// is never changed. chatwidget `open_feature_enable_prompt` shows the shared
// prompt for both Subagents and Memories, and Memories also writes the legacy
// `features.memory_tool` key for older app servers.

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/config"
	historycell "codex_go/tui/history_cell"
)

const (
	settingsWriteKindFeatureEnable = "feature_enable"
	// featureEnableLegacyMemoryKey is the pre-rename Memories key older app
	// servers still read.
	featureEnableLegacyMemoryKey = "memory_tool"
)

// featureEnableSpec is the TUI's view of one opt-in feature.
type featureEnableSpec struct {
	// key is the `features.<key>` config path.
	key string
	// label is the capitalized noun used in titles and notices.
	label string
	// name is the lower-case plural used in prompt copy.
	name string
	// legacyKey is an additional key the same opt-in must write.
	legacyKey string
	// docsURL is shown as the prompt footer when the feature documents itself.
	docsURL string
}

// featureEnableSpecFor resolves the feature names the TUI offers. Rust's
// `open_feature_enable_prompt` ignores any other feature.
func featureEnableSpecFor(feature string) (featureEnableSpec, bool) {
	switch strings.ToLower(strings.TrimSpace(feature)) {
	case "multi_agent", "collab", "subagents":
		return featureEnableSpec{key: "multi_agent", label: "Subagents", name: "subagents"}, true
	case "memories", "memory_tool":
		return featureEnableSpec{
			key:       "memories",
			label:     "Memories",
			name:      "memories",
			legacyKey: featureEnableLegacyMemoryKey,
			docsURL:   memoriesDocsURL,
		}, true
	default:
		return featureEnableSpec{}, false
	}
}

// openFeatureEnablePrompt shows Rust's shared opt-in prompt. The prompt states
// the real scope: the server saves the setting for new threads and this thread
// keeps its current features.
func (m *Model) openFeatureEnablePrompt(feature string) {
	if m == nil {
		return
	}
	spec, ok := featureEnableSpecFor(feature)
	if !ok {
		return
	}
	footer := ""
	if spec.docsURL != "" {
		footer = "Learn more: " + spec.docsURL
	}
	m.openModal(ModalRequestMsg{
		ID:         "feature-enable-" + spec.key,
		Kind:       ModalKindMemories,
		Title:      "Enable " + spec.name + "?",
		Body:       spec.label + " are disabled in this TUI session.",
		Options:    featureEnableOptions(spec),
		FooterNote: footer,
	})
	if m.modal != nil {
		m.modal.memories = &memoriesModalState{enablePrompt: true, feature: spec.key}
	}
}

func featureEnableOptions(spec featureEnableSpec) []ModalOption {
	return []ModalOption{
		{
			ID:          "enable",
			Label:       "Yes, enable",
			Description: "Save on the server for new threads without changing this thread",
		},
		{ID: "keep_disabled", Label: "Not now", Description: "Keep " + spec.name + " disabled"},
	}
}

// enableFeatureForNewThreads writes the opt-in for future threads through the
// app server. Memories write both keys so an older server still sees the change.
func (m *Model) enableFeatureForNewThreads(feature string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	spec, ok := featureEnableSpecFor(feature)
	if !ok {
		return nil
	}
	edits := []SettingsEdit{{KeyPath: "features." + spec.key, Value: true}}
	if spec.legacyKey != "" {
		edits = append(edits, SettingsEdit{KeyPath: "features." + spec.legacyKey, Value: true})
	}
	m.pendingFeatureEnableLabel = spec.label
	return m.writeSettings(settingsWriteKindFeatureEnable, edits)
}

// applyFeatureEnableWriteResult reports the finished write the way Rust does:
// a plain save still warns about project or task overrides, an overridden or
// failed save is an error, and the current thread's settings never change.
func (m *Model) applyFeatureEnableWriteResult(result SettingsWriteResult, err error) {
	if m == nil {
		return
	}
	label := firstNonEmpty(strings.TrimSpace(m.pendingFeatureEnableLabel), "Feature")
	m.pendingFeatureEnableLabel = ""
	m.notice = ""
	switch {
	case err != nil:
		m.applyHistoryCell(historycell.NewErrorEvent("Failed to save " + label + " setting: " + strings.TrimSpace(err.Error())))
	case result.WriteStatus == config.WriteOKOverridden:
		m.applyHistoryCell(historycell.NewErrorEvent(label + " setting was saved but is overridden: a higher-priority configuration layer overrides the saved value."))
	default:
		m.applyHistoryCell(historycell.NewWarningEvent(label + " setting saved on the server for new threads. This thread is unchanged. Project or task settings may override it."))
	}
	m.refreshTranscript()
}
