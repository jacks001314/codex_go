package mcp

import (
	"encoding/json"
	"strings"
)

// MCPToolAccountError reports an invalid explicit Apps account selector.
type MCPToolAccountError struct{}

func (MCPToolAccountError) Error() string {
	return "This app tool requires a non-empty string link_id argument"
}

// resolveMCPToolAccount mirrors Rust #42054: a Codex Apps tool that requires an
// explicit account selector must receive a non-empty string link_id argument;
// other Apps tools keep using the catalog-provided identity.
func resolveMCPToolAccount(toolInfo *MCPToolInfo, arguments any) (string, error) {
	if toolInfo == nil {
		return "", nil
	}
	meta := codexAppsToolMeta(toolInfo.Meta)
	requiresExplicit := false
	if meta != nil {
		requiresExplicit, _ = meta["requires_explicit_link_id"].(bool)
	}
	if requiresExplicit {
		linkID := linkIDFromArguments(arguments)
		if linkID == "" {
			return "", MCPToolAccountError{}
		}
		return linkID, nil
	}
	return MCPToolCallLinkID(toolInfo.Meta), nil
}

func codexAppsToolMeta(meta any) map[string]any {
	base := metadataMap(meta)
	if base == nil {
		return nil
	}
	for _, key := range []string{"_codex_apps", "codex_apps", "codexApps"} {
		if nested := metadataMap(base[key]); nested != nil {
			return nested
		}
	}
	return nil
}

func linkIDFromArguments(arguments any) string {
	switch value := arguments.(type) {
	case map[string]any:
		linkID, _ := value["link_id"].(string)
		return strings.TrimSpace(linkID)
	case json.RawMessage:
		var decoded map[string]any
		if json.Unmarshal(value, &decoded) == nil {
			return linkIDFromArguments(decoded)
		}
	case string:
		var decoded map[string]any
		if json.Unmarshal([]byte(value), &decoded) == nil {
			return linkIDFromArguments(decoded)
		}
	}
	return ""
}
