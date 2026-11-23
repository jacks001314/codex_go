package tool

import (
	"errors"
	"testing"
)

// Rust #62534: builtin_tool_namespaces annotates namespace headers only,
// including a header shared with client tools, and is omitted from flat
// listings that have no header.
func TestBuiltinToolNamespacePrefixesAnnotateHeadersOnlyLikeRust(t *testing.T) {
	prefixes, err := NewIndirectNamespacePrefixes(&IndirectDescriptionPrefixes{
		BuiltinToolNamespaces: map[string]string{
			"functions":          "  Catalog guidance.  ",
			"mcp__shared":        "Shared guidance.",
			"mcp__external_only": "Ignored.",
			"mcp__empty":         "   ",
		},
	}, nil, []string{"functions", "mcp__shared", "mcp__empty"})
	if err != nil {
		t.Fatalf("NewIndirectNamespacePrefixes() error = %v", err)
	}
	if got := prefixes.PrefixForNamespace(""); got != "Catalog guidance." {
		t.Fatalf("functions header prefix = %q", got)
	}
	if got := prefixes.PrefixForNamespace("mcp__external_only"); got != "" {
		t.Fatalf("external-only namespace prefix = %q, want none", got)
	}
	if got := prefixes.PrefixForNamespace("mcp__empty"); got != "" {
		t.Fatalf("empty builtin prefix = %q, want none", got)
	}

	flat := prefixes.ApplyToSpec(Spec{Name: PlainName("lookup"), Description: "Lookup."})
	if flat.Description != "Lookup." {
		t.Fatalf("flat listing description = %q, builtin guidance must not repeat per entry", flat.Description)
	}

	client := prefixes.ApplyToSpec(Spec{
		Name:                 NamespacedName("mcp__shared", "search"),
		Description:          "Search.",
		NamespaceDescription: "Client tools.",
	})
	if client.Description != "Search." {
		t.Fatalf("client flat description = %q", client.Description)
	}
	if client.NamespaceDescription != "Shared guidance.\n\nClient tools." {
		t.Fatalf("client shared header = %q", client.NamespaceDescription)
	}
}

// Rust #62534: overlapping selectors must agree after trimming, regardless of
// which table contributed first.
func TestBuiltinToolNamespacePrefixConflictsLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		prefixes *IndirectDescriptionPrefixes
		servers  map[string][]string
		want     string
	}{
		{
			name: "shared header with matching server guidance",
			prefixes: &IndirectDescriptionPrefixes{
				MCPServers:            map[string]string{"calendar": " Catalog guidance. "},
				BuiltinToolNamespaces: map[string]string{"functions": "Catalog guidance."},
			},
			servers: map[string][]string{"calendar": {"functions"}},
		},
		{
			name: "conflicting server guidance",
			prefixes: &IndirectDescriptionPrefixes{
				MCPServers:            map[string]string{"calendar": "Different guidance."},
				BuiltinToolNamespaces: map[string]string{"functions": "Catalog guidance."},
			},
			servers: map[string][]string{"calendar": {"functions"}},
			want:    "Conflicting indirect description prefixes for namespace `functions`: `mcp_servers.calendar` and `builtin_tool_namespaces.functions`",
		},
		{
			name: "explicit empty selector conflicts",
			prefixes: &IndirectDescriptionPrefixes{
				Namespaces:            map[string]string{"functions": " "},
				BuiltinToolNamespaces: map[string]string{"functions": "Catalog guidance."},
			},
			want: "Conflicting indirect description prefixes for namespace `functions`: `namespaces.functions` and `builtin_tool_namespaces.functions`",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			prefixes, err := NewIndirectNamespacePrefixes(test.prefixes, test.servers, []string{"functions"})
			if test.want == "" {
				if err != nil {
					t.Fatalf("NewIndirectNamespacePrefixes() error = %v", err)
				}
				if got := prefixes.PrefixForNamespace("functions"); got != "Catalog guidance." {
					t.Fatalf("merged functions prefix = %q", got)
				}
				return
			}
			var conflict *IndirectNamespacePrefixConflict
			if !errors.As(err, &conflict) {
				t.Fatalf("error = %v, want IndirectNamespacePrefixConflict", err)
			}
			if err.Error() != test.want {
				t.Fatalf("error = %q, want %q", err.Error(), test.want)
			}
		})
	}
}

func TestRegistryBuiltinToolNamespacesLikeRust(t *testing.T) {
	registry := NewRegistry()
	for _, executor := range []Executor{
		NewExecutorFunc(Spec{Name: PlainName("lookup"), Exposure: ExposureModelVisible}, nil),
		NewExecutorFunc(Spec{Name: NamespacedName("time", "read"), Exposure: ExposureModelVisible}, nil),
		NewExecutorFunc(Spec{Name: NamespacedName("mcp__drive", "search"), Exposure: ExposureModelVisible, ThirdParty: true}, nil),
	} {
		if err := registry.Register(executor); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	got := map[string]bool{}
	for _, namespace := range registry.BuiltinToolNamespaces() {
		got[namespace] = true
	}
	if !got["functions"] || !got["time"] {
		t.Fatalf("builtin namespaces = %#v, want functions and time", got)
	}
	if got["mcp__drive"] {
		t.Fatalf("third-party namespace was reported as builtin: %#v", got)
	}
}
