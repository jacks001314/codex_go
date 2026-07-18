package app

import (
	"strings"

	"codex_go/plugin"
)

// Rust parity subset: codex-rs/tui/src/app/plugin_mentions.rs.

type PluginMention struct {
	ID   string
	Name string
}

func PluginMentionsFromListResponse(response plugin.PluginListResponse) []plugin.CapabilitySummary {
	mentions := []plugin.CapabilitySummary{}
	for _, marketplace := range response.Marketplaces {
		marketplaceName := marketplace.Name
		for _, summary := range marketplace.Plugins {
			mention, ok := PluginMentionFromSummary(marketplaceName, summary)
			if ok {
				mentions = append(mentions, mention)
			}
		}
	}
	return mentions
}

func PluginIsEligibleForMentions(summary *plugin.PluginSummary) bool {
	return summary != nil &&
		summary.Installed &&
		summary.Enabled &&
		summary.Availability != plugin.PluginDisabledByAdmin
}

func PluginMentionFromSummary(marketplaceName string, summary plugin.PluginSummary) (plugin.CapabilitySummary, bool) {
	if !PluginIsEligibleForMentions(&summary) {
		return plugin.CapabilitySummary{}, false
	}
	return plugin.CapabilitySummary{
		Name:           summary.Name,
		ConfigName:     summary.ID,
		DisplayName:    PluginMentionDisplayName(&summary),
		RemotePluginID: summary.RemotePluginID,
		Description:    PluginMentionDescription(marketplaceName, &summary),
		HasSkills:      false,
		MCPServers:     []string{},
		AppConnectors:  []string{},
	}, true
}

func PluginMentionDisplayName(summary *plugin.PluginSummary) string {
	if summary == nil {
		return ""
	}
	if summary.Interface != nil && summary.Interface.DisplayName != nil {
		if displayName := strings.TrimSpace(*summary.Interface.DisplayName); displayName != "" {
			return displayName
		}
	}
	return summary.Name
}

func PluginMentionDescription(marketplaceName string, summary *plugin.PluginSummary) string {
	if summary != nil && summary.Interface != nil && summary.Interface.ShortDescription != nil {
		if description := strings.TrimSpace(*summary.Interface.ShortDescription); description != "" {
			return description
		}
	}
	if marketplaceName := strings.TrimSpace(marketplaceName); marketplaceName != "" {
		return marketplaceName
	}
	return ""
}
