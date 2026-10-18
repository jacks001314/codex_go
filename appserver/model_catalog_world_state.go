package appserver

import (
	"encoding/json"
	"sort"
	"strings"

	"codex_go/agent"
	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/features"
	"codex_go/model"
	"codex_go/session"
)

const (
	modelCatalogMaxRenderedBytes = 1000
	modelCatalogMaxDescription   = 250
	modelCatalogOmittedNotice    = "Additional model choices omitted.\n"
)

// modelCatalogWorldStateInputItem keeps spawn_agent model choices in an
// append-only developer section instead of rewriting tool definitions when the
// catalog refreshes (Rust #49560).
func (r *RuntimeRouter) modelCatalogWorldStateInputItem(threadID string, cfg *config.Config) (any, error) {
	if r == nil || cfg == nil ||
		!features.Enabled(cfg.FeatureSettings(), "model_catalog_in_context") {
		return nil, nil
	}
	agentsConfig, err := cfg.AgentsConfig(r.configBaseDirForAgents())
	if err != nil {
		return nil, err
	}
	version := r.runtimeMultiAgentVersionForThread(threadID, cfg, agentsConfig)
	if version != agent.VersionV2 {
		return nil, nil
	}
	v2Config, err := cfg.MultiAgentV2Config(agentsConfig.MaxConcurrentThreadsPerSession)
	if err != nil {
		return nil, err
	}
	if !v2Config.ExposeSpawnAgentModelOverrides {
		return nil, nil
	}
	limit := uint32(model.MaxSpawnAgentModelOverrides)
	response, err := r.requireModels().List(&model.ModelListParams{
		Limit:           &limit,
		RefreshStrategy: string(model.RefreshOffline),
	})
	if err != nil {
		return nil, err
	}
	catalog := renderModelCatalog(response.Data, version)
	if catalog == "" {
		catalog = "No picker-visible model overrides are currently loaded.\n"
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return nil, err
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		return nil, err
	}
	encoded := json.RawMessage(data)
	if string(state.ModelCatalog) == string(encoded) {
		return nil, nil
	}
	state.ModelCatalog = encoded
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		return nil, err
	}
	if err := r.runtimeSaveThreadRecord(record); err != nil {
		return nil, err
	}
	rendered := contextfrag.Render(contextfrag.NewSimpleFragment(
		contextfrag.RoleDeveloper,
		"<model_catalog>",
		"</model_catalog>",
		catalog,
	))
	return buildRenderedMessage(rendered), nil
}

func renderModelCatalog(models []model.ModelSummary, version agent.MultiAgentVersion) string {
	compatible := make([]model.ModelSummary, 0, len(models))
	for _, info := range models {
		if info.Hidden {
			continue
		}
		if info.MultiAgentVersion != nil && strings.TrimSpace(*info.MultiAgentVersion) != string(version) {
			continue
		}
		compatible = append(compatible, info)
	}
	sort.SliceStable(compatible, func(i, j int) bool {
		return compatible[i].Model < compatible[j].Model
	})
	if len(compatible) > model.MaxSpawnAgentModelOverrides {
		compatible = compatible[:model.MaxSpawnAgentModelOverrides]
	}
	var builder strings.Builder
	for _, info := range compatible {
		description := []rune(strings.TrimSpace(info.Description))
		if len(description) > modelCatalogMaxDescription {
			description = append(description[:modelCatalogMaxDescription], '…')
		}
		line := "- `" + info.Model + "`: " + string(description)
		if len(info.SupportedReasoningEfforts) > 0 {
			efforts := make([]string, 0, len(info.SupportedReasoningEfforts))
			for _, effort := range info.SupportedReasoningEfforts {
				value := effort.ReasoningEffort
				if effort.ReasoningEffort == info.DefaultReasoningEffort {
					value += " (default)"
				}
				efforts = append(efforts, value)
			}
			line += " Reasoning efforts: " + strings.Join(efforts, ", ") + "."
		}
		if len(info.ServiceTiers) > 0 {
			tiers := make([]string, 0, len(info.ServiceTiers))
			for _, tier := range info.ServiceTiers {
				tiers = append(tiers, tier.ID)
			}
			line += " Service tiers: " + strings.Join(tiers, ", ") + "."
		}
		line = escapeModelCatalogText(line) + "\n"
		if builder.Len()+len(line)+len(modelCatalogOmittedNotice) > modelCatalogMaxRenderedBytes {
			builder.WriteString(modelCatalogOmittedNotice)
			break
		}
		builder.WriteString(line)
	}
	return builder.String()
}

func escapeModelCatalogText(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
}
