package tool

import "strings"

// Rust parity: codex-rs/core/src/tools/tool_namespaces_info.rs.

// DefaultFunctionNamespace mirrors codex_protocol::DEFAULT_FUNCTION_NAMESPACE.
const DefaultFunctionNamespace = "functions"

// ToolSearchFunctionName is the function reported for the native tool search
// tool (Rust's TOOL_SEARCH_FUNCTION_NAME).
const ToolSearchFunctionName = "tool_search_tool"

// ToolFunctionSourceKind identifies who dispatches one model-visible function
// (Rust TurnToolSource).
type ToolFunctionSourceKind string

const (
	ToolFunctionSourceHarness ToolFunctionSourceKind = "harness"
	ToolFunctionSourceMCP     ToolFunctionSourceKind = "mcp"
)

// ToolFunctionSource mirrors Rust's TurnToolSource: a harness function or one
// owned by an MCP server.
type ToolFunctionSource struct {
	Kind       ToolFunctionSourceKind `json:"kind"`
	ServerName string                 `json:"server_name,omitempty"`
}

// ToolFunctionInfo mirrors Rust's TurnToolFunctionInfo: the effective per-turn
// exposure of one model-visible function.
type ToolFunctionInfo struct {
	Name         string             `json:"name"`
	Direct       bool               `json:"direct"`
	CodeModeName *string            `json:"code_mode_name,omitempty"`
	Deferred     bool               `json:"deferred"`
	Source       ToolFunctionSource `json:"source"`
}

// ToolNamespaceInfo mirrors Rust's TurnToolNamespaceInfo: the model-visible
// functions belonging to one effective namespace.
type ToolNamespaceInfo struct {
	Name      string                      `json:"name"`
	Functions map[string]ToolFunctionInfo `json:"functions"`
}

// ToolNamespacesInfo is the model-visible tool inventory indexed by effective
// Responses Lite namespace (Rust's TurnToolNamespacesInfo).
type ToolNamespacesInfo map[string]ToolNamespaceInfo

// mcpServerNamer is implemented by executors whose functions belong to an MCP
// server (Rust's ToolRuntime::mcp_server_name).
type mcpServerNamer interface {
	MCPServerName() string
}

// toolNamespaceFunction resolves one spec's effective inventory identity, with
// the native tool search remapped to Rust's tool-search namespace and function.
func toolNamespaceFunction(spec Spec) (string, string) {
	if spec.Name.Name == ToolSearchName && spec.Search != nil {
		return ToolSearchName, ToolSearchFunctionName
	}
	namespace := spec.Name.Namespace
	if namespace == "" {
		namespace = DefaultFunctionNamespace
	}
	return namespace, spec.Name.Name
}

// CollectToolNamespacesInfo mirrors Rust's collect_tool_namespaces_info: it
// reports every non-hidden registry entry that the turn exposes directly,
// through a Code Mode name, or as a deferred model-only tool.
func CollectToolNamespacesInfo(registry *Registry, codeModeToolNames map[string]CodeModeToolNameMetadata, modelVisibleSpecs []Spec) ToolNamespacesInfo {
	direct := map[[2]string]bool{}
	nativeToolSearchVisible := false
	for index := range modelVisibleSpecs {
		namespace, function := toolNamespaceFunction(modelVisibleSpecs[index])
		if namespace == "" || function == "" {
			continue
		}
		if namespace == ToolSearchName && function == ToolSearchFunctionName {
			nativeToolSearchVisible = true
		}
		direct[[2]string{namespace, function}] = true
	}

	codeModeNameByTool := map[[2]string]string{}
	for codeModeName, metadata := range codeModeToolNames {
		namespace := metadata.Namespace
		if namespace == nil || *namespace == "" {
			namespace = stringPtr(DefaultFunctionNamespace)
		}
		if strings.TrimSpace(metadata.Name) == "" {
			continue
		}
		codeModeNameByTool[[2]string{*namespace, metadata.Name}] = codeModeName
	}

	namespaces := ToolNamespacesInfo{}
	if registry == nil {
		return namespaces
	}
	for _, name := range registry.Names() {
		spec, ok := registry.Spec(name)
		if !ok || spec.Exposure == ExposureHidden {
			continue
		}
		namespace, function := toolNamespaceFunction(spec)
		if namespace == "" || function == "" {
			continue
		}
		pair := [2]string{namespace, function}
		isDirect := direct[pair]
		codeModeName, hasCodeModeName := codeModeNameByTool[pair]
		deferred := spec.Exposure == ExposureDeferredModelOnly && nativeToolSearchVisible &&
			(spec.Freeform != nil || spec.Search != nil)
		if !isDirect && !hasCodeModeName && !deferred {
			continue
		}
		source := ToolFunctionSource{Kind: ToolFunctionSourceHarness}
		if executor, found := registry.Lookup(name); found {
			if namer, ok := executor.(mcpServerNamer); ok {
				if serverName := strings.TrimSpace(namer.MCPServerName()); serverName != "" {
					source = ToolFunctionSource{Kind: ToolFunctionSourceMCP, ServerName: serverName}
				}
			}
		}
		entry := namespaces[namespace]
		if entry.Functions == nil {
			entry = ToolNamespaceInfo{Name: namespace, Functions: map[string]ToolFunctionInfo{}}
		}
		if _, exists := entry.Functions[function]; !exists {
			info := ToolFunctionInfo{
				Name:     function,
				Direct:   isDirect,
				Deferred: deferred,
				Source:   source,
			}
			if hasCodeModeName {
				value := codeModeName
				info.CodeModeName = &value
			}
			entry.Functions[function] = info
		}
		namespaces[namespace] = entry
	}
	return namespaces
}

func stringPtr(value string) *string {
	return &value
}
