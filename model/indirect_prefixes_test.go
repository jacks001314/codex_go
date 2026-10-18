package model

import (
	"encoding/json"
	"testing"
)

func TestModelInfoIndirectDescriptionPrefixesParseAndCloneLikeRust(t *testing.T) {
	var info ModelInfo
	if err := json.Unmarshal([]byte(`{
		"slug": "gpt-test",
		"model_messages": {
			"tools": {
				"indirect_description_prefixes": {
					"namespaces": {"functions": "Function guidance."},
					"mcp_servers": {"drive": "Drive guidance."}
				}
			}
		}
	}`), &info); err != nil {
		t.Fatalf("unmarshal ModelInfo: %v", err)
	}
	if info.ModelMessages == nil || info.ModelMessages.Tools == nil || info.ModelMessages.Tools.IndirectDescriptionPrefixes == nil {
		t.Fatal("indirect description prefixes were not decoded")
	}
	prefixes := info.ModelMessages.Tools.IndirectDescriptionPrefixes
	if prefixes.Namespaces["functions"] != "Function guidance." || prefixes.MCPServers["drive"] != "Drive guidance." {
		t.Fatalf("decoded prefixes = %#v", prefixes)
	}
	cloned := cloneModelInfo(info)
	cloned.ModelMessages.Tools.IndirectDescriptionPrefixes.Namespaces["functions"] = "changed"
	if prefixes.Namespaces["functions"] != "Function guidance." {
		t.Fatalf("clone aliased the namespace map: %#v", prefixes.Namespaces)
	}
}
