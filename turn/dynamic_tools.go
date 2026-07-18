package turn

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	DynamicToolNameMaxLen                 = 128
	DynamicToolNamespaceMaxLen            = 64
	DynamicToolNamespaceDescriptionMaxLen = 1024
)

var reservedDynamicToolNamespaces = map[string]bool{
	"api_tool":            true,
	"browser":             true,
	"computer":            true,
	"container":           true,
	"file_search":         true,
	"functions":           true,
	"image_gen":           true,
	"multi_tool_use":      true,
	"python":              true,
	"python_user_visible": true,
	"submodel_delegator":  true,
	"terminal":            true,
	"tool_search":         true,
	"web":                 true,
}

type DynamicToolSpec struct {
	Type         string                    `json:"type,omitempty"`
	Function     *DynamicToolFunctionSpec  `json:"function,omitempty"`
	Namespace    *DynamicToolNamespaceSpec `json:"namespace,omitempty"`
	Name         string                    `json:"name,omitempty"`
	Description  string                    `json:"description,omitempty"`
	InputSchema  any                       `json:"inputSchema,omitempty"`
	DeferLoading bool                      `json:"deferLoading,omitempty"`
	Tools        []DynamicToolFunctionSpec `json:"tools,omitempty"`
}

func (s *DynamicToolSpec) MarshalJSON() ([]byte, error) {
	if s.Namespace != nil || strings.EqualFold(s.Type, "namespace") {
		namespace := s.Namespace
		if namespace == nil {
			namespace = &DynamicToolNamespaceSpec{
				Name:        s.Name,
				Description: s.Description,
				Tools:       s.Tools,
			}
		}
		tools := make([]DynamicToolNamespaceTool, 0, len(namespace.Tools))
		for i := range namespace.Tools {
			tools = append(tools, *NewDynamicToolNamespaceTool(&namespace.Tools[i]))
		}
		return json.Marshal(struct {
			Type        string                     `json:"type"`
			Name        string                     `json:"name"`
			Description string                     `json:"description"`
			Tools       []DynamicToolNamespaceTool `json:"tools"`
		}{
			Type:        "namespace",
			Name:        namespace.Name,
			Description: namespace.Description,
			Tools:       tools,
		})
	}
	tool := s.Function
	if tool == nil {
		tool = &DynamicToolFunctionSpec{
			Name:         s.Name,
			Description:  s.Description,
			InputSchema:  s.InputSchema,
			DeferLoading: s.DeferLoading,
		}
	}
	return json.Marshal(struct {
		Type         string `json:"type"`
		Name         string `json:"name"`
		Description  string `json:"description"`
		InputSchema  any    `json:"inputSchema"`
		DeferLoading bool   `json:"deferLoading,omitempty"`
	}{
		Type:         "function",
		Name:         tool.Name,
		Description:  tool.Description,
		InputSchema:  tool.InputSchema,
		DeferLoading: tool.DeferLoading,
	})
}

type DynamicToolFunctionSpec struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	InputSchema  any    `json:"inputSchema,omitempty"`
	DeferLoading bool   `json:"deferLoading,omitempty"`
}

type DynamicToolCallStatus string

const (
	DynamicToolCallInProgress DynamicToolCallStatus = "inProgress"
	DynamicToolCallCompleted  DynamicToolCallStatus = "completed"
	DynamicToolCallFailed     DynamicToolCallStatus = "failed"
)

type DynamicToolNamespaceTool struct {
	Type string `json:"type"`
	DynamicToolFunctionSpec
}

func (t *DynamicToolNamespaceTool) MarshalJSON() ([]byte, error) {
	toolType := t.Type
	if toolType == "" {
		toolType = "function"
	}
	return json.Marshal(struct {
		Type         string `json:"type"`
		Name         string `json:"name"`
		Description  string `json:"description"`
		InputSchema  any    `json:"inputSchema"`
		DeferLoading bool   `json:"deferLoading,omitempty"`
	}{
		Type:         toolType,
		Name:         t.Name,
		Description:  t.Description,
		InputSchema:  t.InputSchema,
		DeferLoading: t.DeferLoading,
	})
}

func NewDynamicToolNamespaceTool(spec *DynamicToolFunctionSpec) *DynamicToolNamespaceTool {
	if spec == nil {
		return &DynamicToolNamespaceTool{Type: "function"}
	}
	return &DynamicToolNamespaceTool{
		Type:                    "function",
		DynamicToolFunctionSpec: *spec,
	}
}

type DynamicToolNamespaceSpec struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description,omitempty"`
	Tools       []DynamicToolFunctionSpec `json:"tools,omitempty"`
}

func ValidateDynamicTools(tools []DynamicToolSpec) error {
	seenTools := map[string]bool{}
	seenNamespaces := map[string]bool{}
	for i := range tools {
		spec := &tools[i]
		if spec.Namespace != nil || strings.EqualFold(spec.Type, "namespace") {
			namespace := spec.Namespace
			if namespace == nil {
				namespace = &DynamicToolNamespaceSpec{Name: spec.Name, Description: spec.Description, Tools: spec.Tools}
			}
			if err := validateDynamicNamespace(namespace, seenNamespaces); err != nil {
				return err
			}
			seenInNamespace := map[string]bool{}
			for j := range namespace.Tools {
				if err := validateDynamicTool(&namespace.Tools[j], namespace.Name, seenInNamespace); err != nil {
					return err
				}
			}
			continue
		}
		tool := spec.Function
		if tool == nil {
			tool = &DynamicToolFunctionSpec{
				Name:         spec.Name,
				Description:  spec.Description,
				InputSchema:  spec.InputSchema,
				DeferLoading: spec.DeferLoading,
			}
		}
		if err := validateDynamicTool(tool, "", seenTools); err != nil {
			return err
		}
	}
	return nil
}

func validateDynamicNamespace(namespace *DynamicToolNamespaceSpec, seen map[string]bool) error {
	name := strings.TrimSpace(namespace.Name)
	if name == "" {
		return fmt.Errorf("%w: dynamic tool namespace must not be empty", ErrInvalidTurnRequest)
	}
	if name != namespace.Name {
		return fmt.Errorf("%w: dynamic tool namespace has leading/trailing whitespace: %q", ErrInvalidTurnRequest, namespace.Name)
	}
	if err := validateDynamicIdentifier(name, "dynamic tool namespace", DynamicToolNamespaceMaxLen); err != nil {
		return err
	}
	if reservedDynamicToolNamespaces[name] {
		return fmt.Errorf("%w: dynamic tool namespace is reserved: %s", ErrInvalidTurnRequest, name)
	}
	if seen[name] {
		return fmt.Errorf("%w: duplicate dynamic tool namespace: %s", ErrInvalidTurnRequest, name)
	}
	seen[name] = true
	if utf8.RuneCountInString(namespace.Description) > DynamicToolNamespaceDescriptionMaxLen {
		return fmt.Errorf("%w: dynamic tool namespace description must be at most %d characters", ErrInvalidTurnRequest, DynamicToolNamespaceDescriptionMaxLen)
	}
	if len(namespace.Tools) == 0 {
		return fmt.Errorf("%w: dynamic tool namespace must contain at least one tool: %s", ErrInvalidTurnRequest, name)
	}
	return nil
}

func validateDynamicTool(tool *DynamicToolFunctionSpec, namespace string, seen map[string]bool) error {
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		return fmt.Errorf("%w: dynamic tool name must not be empty", ErrInvalidTurnRequest)
	}
	if name != tool.Name {
		return fmt.Errorf("%w: dynamic tool name has leading/trailing whitespace: %q", ErrInvalidTurnRequest, tool.Name)
	}
	if err := validateDynamicIdentifier(name, "dynamic tool name", DynamicToolNameMaxLen); err != nil {
		return err
	}
	if name == "mcp" || strings.HasPrefix(name, "mcp__") {
		return fmt.Errorf("%w: dynamic tool name is reserved: %s", ErrInvalidTurnRequest, name)
	}
	if seen[name] {
		if namespace != "" {
			return fmt.Errorf("%w: duplicate dynamic tool name in namespace %s: %s", ErrInvalidTurnRequest, namespace, name)
		}
		return fmt.Errorf("%w: duplicate dynamic tool name: %s", ErrInvalidTurnRequest, name)
	}
	seen[name] = true
	if tool.DeferLoading && namespace == "" {
		return fmt.Errorf("%w: deferred dynamic tool must include a namespace: %s", ErrInvalidTurnRequest, name)
	}
	return nil
}

func validateDynamicIdentifier(value string, label string, maxLen int) error {
	if utf8.RuneCountInString(value) > maxLen {
		return fmt.Errorf("%w: %s must be at most %d characters: %q", ErrInvalidTurnRequest, label, maxLen, value)
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("%w: %s must match ^[a-zA-Z0-9_-]+$: %q", ErrInvalidTurnRequest, label, value)
	}
	return nil
}
