package tool

import (
	"context"
	"errors"
	"testing"
)

func TestIndirectNamespacePrefixesResolveServerNamespacesLikeRust(t *testing.T) {
	prefixes, err := NewIndirectNamespacePrefixes(&IndirectDescriptionPrefixes{
		Namespaces: map[string]string{
			"functions":  " Use functions carefully. ",
			"mcp__empty": "",
		},
		MCPServers: map[string]string{
			"drive": " Drive guidance. ",
		},
	}, map[string][]string{
		"drive": {"mcp__drive"},
	})
	if err != nil {
		t.Fatalf("NewIndirectNamespacePrefixes() error = %v", err)
	}
	if got := prefixes.PrefixForNamespace(""); got != "Use functions carefully." {
		t.Fatalf("functions prefix = %q", got)
	}
	if got := prefixes.PrefixForNamespace("mcp__drive"); got != "Drive guidance." {
		t.Fatalf("registered server prefix = %q", got)
	}
	if got := prefixes.PrefixForNamespace("mcp__empty"); got != "" {
		t.Fatalf("explicit empty prefix = %q, want none", got)
	}
}

func TestIndirectNamespacePrefixesRejectConflictingMCPSelectorsLikeRust(t *testing.T) {
	_, err := NewIndirectNamespacePrefixes(&IndirectDescriptionPrefixes{
		Namespaces: map[string]string{"mcp__drive": "namespace guidance"},
		MCPServers: map[string]string{"drive": "server guidance"},
	}, map[string][]string{"drive": {"mcp__drive"}})
	var conflict *IndirectNamespacePrefixConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want IndirectNamespacePrefixConflict", err)
	}
	if conflict.Namespace != "mcp__drive" || conflict.FirstSource != "namespaces.mcp__drive" || conflict.SecondSource != "mcp_servers.drive" {
		t.Fatalf("conflict = %#v", conflict)
	}
}

func TestIndirectNamespacePrefixesApplyOnlyToRenderedSpecCopiesLikeRust(t *testing.T) {
	prefixes, err := NewIndirectNamespacePrefixes(&IndirectDescriptionPrefixes{
		Namespaces: map[string]string{"functions": "Function guidance.", "mcp__drive": "Drive guidance."},
	}, nil)
	if err != nil {
		t.Fatalf("NewIndirectNamespacePrefixes() error = %v", err)
	}
	original := Spec{
		Name:                 NamespacedName("mcp__drive", "search"),
		Description:          "Search Drive.",
		NamespaceDescription: "Drive tools.",
	}
	prefixed := prefixes.ApplyToSpec(original)
	if prefixed.Description != "Drive guidance.\n\nSearch Drive." || prefixed.NamespaceDescription != "Drive guidance.\n\nDrive tools." {
		t.Fatalf("prefixed spec = %#v", prefixed)
	}
	if original.Description != "Search Drive." || original.NamespaceDescription != "Drive tools." {
		t.Fatalf("ApplyToSpec mutated the original = %#v", original)
	}
	plain := prefixes.ApplyToSpec(Spec{Name: PlainName("lookup"), Description: "Lookup."})
	if plain.Description != "Function guidance.\n\nLookup." {
		t.Fatalf("plain function description = %q", plain.Description)
	}
}

func TestRegistryMCPServerNamespacesLikeRust(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(stubMCPServerExecutor{
		spec:       Spec{Name: NamespacedName("mcp__drive", "search")},
		serverName: "drive",
	}); err != nil {
		t.Fatalf("register MCP tool: %v", err)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: NamespacedName("mcp__drive", "read")}, func(context.Context, *Invocation) (*Output, error) {
		return &Output{}, nil
	})); err != nil {
		t.Fatalf("register non-MCP namespace tool: %v", err)
	}
	namespaces := registry.MCPServerNamespaces()
	if len(namespaces["drive"]) != 1 || namespaces["drive"][0] != "mcp__drive" {
		t.Fatalf("MCP server namespaces = %#v", namespaces)
	}
}
