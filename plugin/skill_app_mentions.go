package plugin

import (
	"strings"

	"codex_go/apps"
)

// SkillAppConnector is one available connector's mention identity.
type SkillAppConnector struct {
	ID   string
	Name string
}

// CollectExplicitAppIDsFromSkillItems mirrors Rust's
// `collect_explicit_app_ids_from_skill_items`: a connector named inside the
// selected skills' instructions counts as explicitly selected. An `app://` link
// always counts; a plain mention counts only when exactly one available connector
// carries that mention slug and no skill shares the name, so an ambiguous name is
// never attributed.
func CollectExplicitAppIDsFromSkillItems(messages []string, connectors func() []SkillAppConnector, skillNames []string) map[string]bool {
	if len(messages) == 0 || connectors == nil {
		return nil
	}
	mentions := CollectToolMentionsFromMessages(messages)
	if mentions == nil || (len(mentions.Paths) == 0 && len(mentions.PlainNames) == 0) {
		return nil
	}
	connectorIDs := map[string]bool{}
	for path := range mentions.Paths {
		if toolKindForPath(path) != "app" {
			continue
		}
		if id := appIDFromPath(path); id != "" {
			connectorIDs[id] = true
		}
	}
	// The catalog lookup is lazy: a skill that mentions nothing an app could
	// answer to must not pay for it.
	available := connectors()
	if len(available) == 0 {
		return nil
	}
	slugCounts := map[string]int{}
	for _, connector := range available {
		slugCounts[apps.ConnectorMentionSlugFromName(connector.Name)]++
	}
	skillNameCounts := map[string]int{}
	for _, name := range skillNames {
		if trimmed := strings.ToLower(strings.TrimSpace(name)); trimmed != "" {
			skillNameCounts[trimmed]++
		}
	}
	for _, connector := range available {
		slug := apps.ConnectorMentionSlugFromName(connector.Name)
		if slug == "" || slugCounts[slug] != 1 || skillNameCounts[slug] != 0 {
			continue
		}
		if !mentions.PlainNames[slug] && !mentions.PlainNames[strings.ToLower(slug)] {
			continue
		}
		if id := strings.TrimSpace(connector.ID); id != "" {
			connectorIDs[id] = true
		}
	}
	if len(connectorIDs) == 0 {
		return nil
	}
	return connectorIDs
}
