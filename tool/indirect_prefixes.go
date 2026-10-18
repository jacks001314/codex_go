package tool

import (
	"fmt"
	"strings"
)

// IndirectDescriptionPrefixes mirrors Rust
// `model_messages.tools.indirect_description_prefixes` (#47642). Selectors are
// applied only to indirectly presented tools, never to direct specifications.
type IndirectDescriptionPrefixes struct {
	Namespaces map[string]string `json:"namespaces,omitempty"`
	MCPServers map[string]string `json:"mcp_servers,omitempty"`
}

// IndirectNamespacePrefixConflict reports two catalog selectors that resolve to
// the same rendered namespace with different trimmed values.
type IndirectNamespacePrefixConflict struct {
	Namespace    string
	FirstSource  string
	SecondSource string
}

func (e *IndirectNamespacePrefixConflict) Error() string {
	if e == nil {
		return "conflicting indirect description prefixes"
	}
	return fmt.Sprintf(
		"Conflicting indirect description prefixes for namespace `%s`: `%s` and `%s`",
		e.Namespace,
		e.FirstSource,
		e.SecondSource,
	)
}

// IndirectNamespacePrefixes is a resolved, trimmed namespace-to-prefix map.
type IndirectNamespacePrefixes struct {
	prefixes map[string]string
}

// NewIndirectNamespacePrefixes expands configured MCP server selectors through
// the registered server-to-namespace mapping. Empty values participate in
// conflict detection but add no prefix. Conflicting selectors for one final
// namespace are rejected before rendering or a model request.
func NewIndirectNamespacePrefixes(
	prefixes *IndirectDescriptionPrefixes,
	serverNamespaces map[string][]string,
) (*IndirectNamespacePrefixes, error) {
	resolved := map[string]resolvedIndirectPrefix{}
	if prefixes != nil {
		for namespace, prefix := range prefixes.Namespaces {
			namespace = strings.TrimSpace(namespace)
			if namespace == "" {
				continue
			}
			resolved[namespace] = resolvedIndirectPrefix{
				value:  strings.TrimSpace(prefix),
				source: "namespaces." + namespace,
			}
		}
		for server, prefix := range prefixes.MCPServers {
			server = strings.TrimSpace(server)
			if server == "" {
				continue
			}
			trimmed := strings.TrimSpace(prefix)
			for _, namespace := range serverNamespaces[server] {
				namespace = strings.TrimSpace(namespace)
				if namespace == "" {
					continue
				}
				source := "mcp_servers." + server
				existing, ok := resolved[namespace]
				if ok && existing.value != trimmed {
					return nil, &IndirectNamespacePrefixConflict{
						Namespace:    namespace,
						FirstSource:  existing.source,
						SecondSource: source,
					}
				}
				if !ok {
					resolved[namespace] = resolvedIndirectPrefix{value: trimmed, source: source}
				}
			}
		}
	}
	out := make(map[string]string, len(resolved))
	for namespace, prefix := range resolved {
		if prefix.value != "" {
			out[namespace] = prefix.value
		}
	}
	return &IndirectNamespacePrefixes{prefixes: out}, nil
}

type resolvedIndirectPrefix struct {
	value  string
	source string
}

// PrefixForNamespace returns the trimmed prefix for a rendered namespace.
// Plain function tools use the `functions` namespace selector.
func (p *IndirectNamespacePrefixes) PrefixForNamespace(namespace string) string {
	if p == nil {
		return ""
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = defaultFunctionNamespace
	}
	return p.prefixes[namespace]
}

// ApplyToSpec returns a copy of one tool specification with indirect rendering
// guidance applied. The original registry specification is never mutated.
func (p *IndirectNamespacePrefixes) ApplyToSpec(spec Spec) Spec {
	if p == nil {
		return spec
	}
	namespace := renderedToolNamespace(spec.Name)
	prefix := p.PrefixForNamespace(namespace)
	if prefix == "" {
		return spec
	}
	spec.Description = prependToolDescriptionPrefix(prefix, spec.Description)
	if strings.TrimSpace(spec.Name.Namespace) != "" {
		spec.NamespaceDescription = prependToolDescriptionPrefix(prefix, spec.NamespaceDescription)
	}
	return spec
}

// ApplyToSpecs applies indirect rendering guidance to cloned specifications.
func (p *IndirectNamespacePrefixes) ApplyToSpecs(specs []Spec) []Spec {
	if p == nil {
		return specs
	}
	out := make([]Spec, len(specs))
	for index := range specs {
		out[index] = p.ApplyToSpec(specs[index])
	}
	return out
}

const defaultFunctionNamespace = "functions"

func renderedToolNamespace(name ToolName) string {
	if namespace := strings.TrimSpace(name.Namespace); namespace != "" {
		return namespace
	}
	return defaultFunctionNamespace
}
