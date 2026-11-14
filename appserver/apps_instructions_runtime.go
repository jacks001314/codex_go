package appserver

import (
	"encoding/json"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
	"codex_go/session"
)

func (r *RuntimeRouter) appsGuidanceWorldStateInputItem(threadID string, cfg *config.Config, info *model.ModelInfo) (any, error) {
	return r.appsGuidanceWorldStateItem(threadID, cfg, info, false)
}
func (r *RuntimeRouter) appsGuidanceWorldStateItem(threadID string, cfg *config.Config, info *model.ModelInfo, freshWindow bool) (any, error) {
	available := info != nil && info.IncludeAppsUsageInstructions && cfg != nil && features.Enabled(cfg.FeatureSettings(), "apps")
	if cfg != nil {
		if include, ok := cfg.Values["include_apps_instructions"].(bool); ok && !include {
			available = false
		}
	}
	if available {
		available = false
		service := r.requireApps()
		r.configureAppAccessibleProvider(service)
		connectors, err := service.AvailableForGuidance(threadID, cfg.Values)
		if err != nil {
			return nil, err
		}
		for _, app := range connectors {
			if app.IsAccessible && app.IsEnabled {
				available = true
				break
			}
		}
	}
	var messages *model.AppsMessages
	if cfg != nil && features.Enabled(cfg.FeatureSettings(), "catalog_apps_instructions") && info != nil && info.ModelMessages != nil {
		messages = info.ModelMessages.Apps
	}
	resolved := model.ResolveAppsInstructions(messages, "codex_apps")
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil {
		return nil, err
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return nil, err
	}
	retained := latestAppsGuidance(record.Items)
	previous := state.AppsInstructions
	if freshWindow || retained == nil {
		previous = nil
		retained = nil
	}
	snapshot, text := appsInstructionsTransition(previous, retained, available, resolved)
	state.AppsInstructions = snapshot
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		return nil, err
	}
	if err = r.runtimeSaveThreadRecord(record); err != nil {
		return nil, err
	}
	if text == "" {
		return nil, nil
	}
	return modelInputTextMessage("developer", text), nil
}

func isAppsGuidanceText(text string) bool {
	text = strings.TrimSpace(text)
	for _, tag := range []string{"apps_instructions", "catalog_apps_instructions"} {
		if strings.HasPrefix(text, "<"+tag+">") && strings.HasSuffix(text, "</"+tag+">") {
			return true
		}
	}
	return false
}

func appsGuidanceSessionItem(turnID string, input any, now time.Time) session.Item {
	raw := input.(map[string]any)
	text := textFromInputItemContent(raw["content"])
	encoded, _ := json.Marshal(raw)
	return session.Item{ID: "apps-guidance-" + safeIdentifier(turnID), Type: "message", Role: "developer", Text: text,
		Content: []session.ContentPart{{Type: "input_text", Text: text}}, Raw: encoded, CreatedAt: now,
		Data:     map[string]any{"kind": "apps_instructions", "hiddenFromThread": true},
		Metadata: appTurnMetadata(turnID, map[string]any{"kind": "apps_instructions", "hiddenFromThread": true}),
	}
}

// latestAppsGuidance accepts both decoded and raw-only rollout messages. Role
// checks apply to the representation that supplied the text.
func latestAppsGuidance(items []session.Item) *string {
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		role, text := item.Role, item.Text
		if text == "" {
			for partIndex := len(item.Content) - 1; partIndex >= 0; partIndex-- {
				part := item.Content[partIndex]
				if part.Type == "input_text" && isAppsGuidanceText(part.Text) {
					text = part.Text
					break
				}
			}
		}
		if text == "" && len(item.Raw) > 0 {
			var raw map[string]any
			if json.Unmarshal(item.Raw, &raw) == nil && raw["type"] == "message" {
				role, _ = raw["role"].(string)
				text = textFromInputItemContent(raw["content"])
			}
		}
		if role == "developer" && isAppsGuidanceText(text) {
			return &text
		}
	}
	return nil
}
