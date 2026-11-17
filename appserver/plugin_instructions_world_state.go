package appserver

import (
	"encoding/json"
	"strings"

	contextfrag "codex_go/context"
	"codex_go/model"
	"codex_go/session"
)

const (
	pluginInstructionsOpenTag           = "<plugins_instructions>"
	pluginInstructionsCloseTag          = "</plugins_instructions>"
	pluginInstructionsReplacementPrefix = "\nThese plugin usage instructions replace all previously provided plugin usage instructions.\n\n"
	pluginInstructionsRemovalBody       = "\nThe previously provided plugin usage instructions no longer apply.\n"
)

// pluginInstructionsSnapshot mirrors Rust's PluginsInstructionsSnapshot::Current:
// the hash of the generic wording the model has seen and whether the catalog
// supplied it. Older Go stores used `hash` for the fingerprint; the reader
// still accepts it.
type pluginInstructionsSnapshot struct {
	Instructions string `json:"instructions,omitempty"`
	Catalog      bool   `json:"catalog"`
}

// legacyPluginInstructionsSnapshot is the pre-alignment Go wire form.
type legacyPluginInstructionsSnapshot struct {
	Hash    string `json:"hash,omitempty"`
	Catalog bool   `json:"catalog"`
}

// pluginGenericGuidanceForThread renders the body of the generic plugin usage
// guidance when the current model and installed capabilities include it.
func (r *RuntimeRouter) pluginGenericGuidanceForThread(threadID string, modelInfo *model.ModelInfo) (string, bool) {
	if r == nil || r.services.Plugins == nil {
		return "", false
	}
	if modelInfo != nil && !modelInfo.IncludePluginUsageInstructions {
		return "", false
	}
	capabilities := r.pluginCapabilitiesForThread(threadID)
	if len(capabilities) == 0 {
		return "", false
	}
	intro, usage, _ := pluginGuidanceForModel(modelInfo)
	catalog := intro != contextfrag.PluginGuidanceIntroDefault || usage != contextfrag.PluginGuidanceUsageDefault
	fragment := contextfrag.NewAvailablePluginsInstructionsWithGuidance(contextPluginSummaries(capabilities), intro, usage)
	if fragment == nil || strings.TrimSpace(fragment.Body()) == "" {
		return "", catalog
	}
	return fragment.Body(), catalog
}

// pluginGuidanceHash mirrors Rust's `WorldStateHash::from_json(Value::String)`:
// JSON hashing preserves CRLF and every other literal byte of the body.
func pluginGuidanceHash(body string) string {
	return session.WorldStateJSONHash(body)
}

// pluginBundledGuidanceHash is the slice of Rust's bundled generic plugin
// wording (`AvailablePluginsInstructions::default()`) that legacy snapshots
// stand for.
func pluginBundledGuidanceHash() string {
	return pluginGuidanceHash(contextfrag.PluginBundledGuidanceBody())
}

// decodePluginInstructionsSnapshot reads Rust's snapshot wire form: the current
// object, Rust's legacy bare bool (pre-catalog availability), or the legacy Go
// object. An unreadable snapshot is treated as `Unknown`, which Rust resolves
// to the bundled guidance fingerprint.
func decodePluginInstructionsSnapshot(raw json.RawMessage) pluginInstructionsSnapshot {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "{"):
		var current pluginInstructionsSnapshot
		if err := json.Unmarshal(raw, &current); err != nil {
			return pluginInstructionsSnapshot{Instructions: pluginBundledGuidanceHash()}
		}
		if current.Instructions != "" {
			return current
		}
		var legacy legacyPluginInstructionsSnapshot
		if err := json.Unmarshal(raw, &legacy); err == nil && legacy.Hash != "" {
			return pluginInstructionsSnapshot{Instructions: legacy.Hash, Catalog: legacy.Catalog}
		}
		return current
	case trimmed == "true":
		return pluginInstructionsSnapshot{Instructions: pluginBundledGuidanceHash()}
	case trimmed == "false":
		return pluginInstructionsSnapshot{}
	default:
		// Rust resolves unreadable retained state to the bundled guidance.
		return pluginInstructionsSnapshot{Instructions: pluginBundledGuidanceHash()}
	}
}

func pluginInstructionsItem(body string) any {
	rendered := contextfrag.Render(contextfrag.NewSimpleFragment(
		contextfrag.RoleDeveloper, pluginInstructionsOpenTag, pluginInstructionsCloseTag, body,
	))
	return buildRenderedMessage(rendered)
}

// pluginGuidanceWorldStateInputItem emits the generic plugin guidance once and
// later reports replacement or removal instead of repeating stale wording
// (Rust #58894 plugins_instructions world-state section).
func (r *RuntimeRouter) pluginGuidanceWorldStateInputItem(threadID string, modelInfo *model.ModelInfo) (any, error) {
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return nil, err
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return nil, err
	}
	var previous pluginInstructionsSnapshot
	hadPrevious := len(state.PluginsInstructions) > 0
	if hadPrevious {
		previous = decodePluginInstructionsSnapshot(state.PluginsInstructions)
	}
	guidance, catalog := r.pluginGenericGuidanceForThread(threadID, modelInfo)
	if strings.TrimSpace(guidance) == "" {
		if !hadPrevious || previous.Instructions == "" {
			return nil, nil
		}
		if !previous.Catalog {
			// Bundled guidance disappears silently when it stops applying.
			state.PluginsInstructions = nil
			record.Metadata.WorldState, err = session.EncodeWorldState(state)
			if err != nil {
				return nil, err
			}
			return nil, r.runtimeSaveThreadRecord(record)
		}
		state.PluginsInstructions, err = json.Marshal(pluginInstructionsSnapshot{Catalog: true})
		if err != nil {
			return nil, err
		}
		record.Metadata.WorldState, err = session.EncodeWorldState(state)
		if err != nil {
			return nil, err
		}
		if err := r.runtimeSaveThreadRecord(record); err != nil {
			return nil, err
		}
		return pluginInstructionsItem(pluginInstructionsRemovalBody), nil
	}
	hash := pluginGuidanceHash(guidance)
	if hadPrevious && previous.Instructions == hash {
		return nil, nil
	}
	display := guidance
	if previous.Instructions != "" {
		display = pluginInstructionsReplacementPrefix + guidance
	}
	state.PluginsInstructions, err = json.Marshal(pluginInstructionsSnapshot{Instructions: hash, Catalog: catalog})
	if err != nil {
		return nil, err
	}
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		return nil, err
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return nil, err
	}
	return pluginInstructionsItem(display), nil
}
