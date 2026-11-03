package appserver

import (
	"crypto/sha256"
	"encoding/hex"
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

// pluginInstructionsSnapshot mirrors the current Rust snapshot: the hash of the
// generic wording the model has seen and whether the catalog supplied it.
type pluginInstructionsSnapshot struct {
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

func pluginGuidanceHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
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
		if err := json.Unmarshal(state.PluginsInstructions, &previous); err != nil {
			previous = pluginInstructionsSnapshot{}
		}
	}
	guidance, catalog := r.pluginGenericGuidanceForThread(threadID, modelInfo)
	if strings.TrimSpace(guidance) == "" {
		if !hadPrevious || previous.Hash == "" {
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
	if hadPrevious && previous.Hash == hash {
		return nil, nil
	}
	display := guidance
	if previous.Hash != "" {
		display = pluginInstructionsReplacementPrefix + guidance
	}
	state.PluginsInstructions, err = json.Marshal(pluginInstructionsSnapshot{Hash: hash, Catalog: catalog})
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
