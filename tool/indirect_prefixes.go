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
	// BuiltinToolNamespaces annotates namespace headers of registered built-in
	// tools, including headers shared with client tools. Flat listings omit
	// this selector, and external-only namespaces are ignored (Rust #62534).
	BuiltinToolNamespaces map[string]string `json:"builtin_tool_namespaces,omitempty"`
	MCPServers            map[string]string `json:"mcp_servers,omitempty"`
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
// Builtin headers annotate namespace headers only; the prefix map also applies
// to each flat entry in an indirectly presented listing.
type IndirectNamespacePrefixes struct {
	prefixes       map[string]string
	builtinHeaders map[string]string
}

// NewIndirectNamespacePrefixes expands configured MCP server selectors through
// the registered server-to-namespace mapping. Empty values participate in
// conflict detection but add no prefix. Conflicting selectors for one final
// namespace are rejected before rendering or a model request.
func NewIndirectNamespacePrefixes(
	prefixes *IndirectDescriptionPrefixes,
	serverNamespaces map[string][]string,
	builtinNamespaces []string,
) (*IndirectNamespacePrefixes, error) {
	resolved := map[string]resolvedIndirectPrefix{}
	builtinHeaders := map[string]string{}
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
			for _, namespace := range serverNamespaces[server] {
				if err := insertIndirectPrefix(&resolved, namespace, prefix, "mcp_servers."+server); err != nil {
					return nil, err
				}
			}
		}
		if len(prefixes.BuiltinToolNamespaces) > 0 {
			registered := make(map[string]struct{}, len(builtinNamespaces))
			for _, namespace := range builtinNamespaces {
				if namespace = strings.TrimSpace(namespace); namespace != "" {
					registered[namespace] = struct{}{}
				}
			}
			for namespace, prefix := range prefixes.BuiltinToolNamespaces {
				namespace = strings.TrimSpace(namespace)
				if namespace == "" {
					continue
				}
				if _, ok := registered[namespace]; !ok {
					continue
				}
				if _, ok := resolved[namespace]; ok {
					if err := insertIndirectPrefix(&resolved, namespace, prefix, "builtin_tool_namespaces."+namespace); err != nil {
						return nil, err
					}
					continue
				}
				if trimmed := strings.TrimSpace(prefix); trimmed != "" {
					builtinHeaders[namespace] = trimmed
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
	return &IndirectNamespacePrefixes{prefixes: out, builtinHeaders: builtinHeaders}, nil
}

func insertIndirectPrefix(
	resolved *map[string]resolvedIndirectPrefix,
	namespace string,
	prefix string,
	source string,
) error {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return nil
	}
	trimmed := strings.TrimSpace(prefix)
	existing, ok := (*resolved)[namespace]
	if ok && existing.value != trimmed {
		return &IndirectNamespacePrefixConflict{
			Namespace:    namespace,
			FirstSource:  existing.source,
			SecondSource: source,
		}
	}
	if !ok {
		(*resolved)[namespace] = resolvedIndirectPrefix{value: trimmed, source: source}
	}
	return nil
}

type resolvedIndirectPrefix struct {
	value  string
	source string
}

// PrefixForNamespace returns the trimmed guidance for a rendered namespace
// header. Plain function tools use the `functions` namespace selector.
// Built-in namespace guidance applies when no explicit indirect selector
// resolves the same namespace.
func (p *IndirectNamespacePrefixes) PrefixForNamespace(namespace string) string {
	if p == nil {
		return ""
	}
	namespace = renderedNamespace(namespace)
	if prefix, ok := p.prefixes[namespace]; ok {
		return prefix
	}
	return p.builtinHeaders[namespace]
}

// flatPrefix returns guidance attached to an individual entry in a flat
// indirect listing. Built-in namespace selectors annotate headers only.
func (p *IndirectNamespacePrefixes) flatPrefix(namespace string) string {
	if p == nil {
		return ""
	}
	return p.prefixes[renderedNamespace(namespace)]
}

func renderedNamespace(namespace string) string {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return defaultFunctionNamespace
	}
	return namespace
}

// ApplyToSpec returns a copy of one tool specification with indirect rendering
// guidance applied. The original registry specification is never mutated.
func (p *IndirectNamespacePrefixes) ApplyToSpec(spec Spec) Spec {
	if p == nil {
		return spec
	}
	namespace := renderedToolNamespace(spec.Name)
	if prefix := p.flatPrefix(namespace); prefix != "" {
		spec.Description = prependToolDescriptionPrefix(prefix, spec.Description)
	}
	if prefix := p.PrefixForNamespace(namespace); prefix != "" && strings.TrimSpace(spec.Name.Namespace) != "" {
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
