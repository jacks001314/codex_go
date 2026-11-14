package model

import "strings"

// ResolvedAppsInstructions contains literal Apps guidance and its provenance.
type ResolvedAppsInstructions struct {
	UsageInstructions string
	CatalogOverride   bool
}

// ResolveAppsInstructions applies the catalog's UTF-8 byte limit. Availability
// and model gates belong to the caller; an empty override intentionally clears
// guidance and must remain distinguishable from a missing override.
func ResolveAppsInstructions(messages *AppsMessages, serverName string) ResolvedAppsInstructions {
	if messages != nil && messages.UsageInstructions != nil {
		text := *messages.UsageInstructions
		if text == "" || (len(text) <= 800 && strings.TrimSpace(text) != "") {
			return ResolvedAppsInstructions{UsageInstructions: text, CatalogOverride: true}
		}
	}
	return ResolvedAppsInstructions{UsageInstructions: "\n## Apps (Connectors)\nApps (Connectors) can be explicitly triggered in user messages in the format `[$app-name](app://{connector_id})`. Apps can also be implicitly triggered as long as the context suggests usage of available apps.\n" +
		"An app is equivalent to a set of MCP tools within the `" + serverName + "` MCP.\n" +
		"An installed app's MCP tools are either provided to you already, or can be lazy-loaded through the `tool_search` tool. If `tool_search` is available, the apps that are searchable by `tools_search` will be listed by it.\n" +
		"Do not additionally call list_mcp_resources or list_mcp_resource_templates for apps.\n",
	}
}
