package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"codex_go/jsonschema"
	"codex_go/tool"
)

const dynamicToolServerRequestMethod = "item/tool/call"
const remoteImageURLError = "remote image URLs are not supported; use an inline data URL instead"

type DynamicToolCaller interface {
	Request(ctx context.Context, method string, params any, target any) error
}

type DynamicToolRegistryOptions struct {
	Caller    DynamicToolCaller
	ThreadID  string
	TurnID    string
	Tools     []DynamicToolSpec
	Now       func() time.Time
	EnableAll bool
}

type DynamicToolCallParams struct {
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	CallID    string `json:"callId"`
	Namespace any    `json:"namespace"`
	Tool      string `json:"tool"`
	Arguments any    `json:"arguments"`
}

type DynamicToolCallResponse struct {
	ContentItems []DynamicToolCallOutputContentItem `json:"contentItems"`
	Success      bool                               `json:"success"`
}

type DynamicToolCallOutputContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"imageUrl,omitempty"`
	AudioURL string `json:"audioUrl,omitempty"`
}

func RegisterDynamicToolHandlers(registry *tool.Registry, options *DynamicToolRegistryOptions) error {
	if registry == nil || options == nil || len(options.Tools) == 0 {
		return nil
	}
	if err := ValidateDynamicTools(options.Tools); err != nil {
		return err
	}
	for i := range options.Tools {
		spec := &options.Tools[i]
		if spec.Namespace != nil || strings.EqualFold(spec.Type, "namespace") {
			namespace := spec.Namespace
			if namespace == nil {
				namespace = &DynamicToolNamespaceSpec{Name: spec.Name, Description: spec.Description, Tools: spec.Tools}
			}
			for j := range namespace.Tools {
				if err := registerDynamicToolHandler(registry, options, namespace, &namespace.Tools[j]); err != nil {
					return err
				}
			}
			continue
		}
		function := spec.Function
		if function == nil {
			function = &DynamicToolFunctionSpec{
				Name:         spec.Name,
				Description:  spec.Description,
				InputSchema:  spec.InputSchema,
				DeferLoading: spec.DeferLoading,
			}
		}
		if err := registerDynamicToolHandler(registry, options, nil, function); err != nil {
			return err
		}
	}
	return nil
}

func registerDynamicToolHandler(registry *tool.Registry, options *DynamicToolRegistryOptions, namespace *DynamicToolNamespaceSpec, function *DynamicToolFunctionSpec) error {
	if function == nil {
		return nil
	}
	name := tool.PlainName(function.Name)
	if namespace != nil {
		name = tool.NamespacedName(namespace.Name, function.Name)
	}
	inputSchema, ok := dynamicToolInputSchema(function.InputSchema)
	if !ok {
		return fmt.Errorf("%w: dynamic tool input schema is not an object: %s", ErrInvalidTurnRequest, function.Name)
	}
	exposure := tool.ExposureModelVisible
	if function.DeferLoading {
		exposure = tool.ExposureDiscoverable
	}
	spec := tool.Spec{
		Name:                 name,
		Description:          function.Description,
		InputSchema:          inputSchema,
		Exposure:             exposure,
		Parallel:             true,
		NamespaceDescription: dynamicToolNamespaceDescription(namespace),
		Search: &tool.SearchInfo{
			Source: &tool.SearchSourceInfo{
				Name:        "Dynamic tools",
				Description: "Tools provided by the current Codex thread.",
			},
		},
	}
	_, err := registry.RegisterExternal(&dynamicToolExecutor{
		spec:      spec,
		caller:    options.Caller,
		threadID:  strings.TrimSpace(options.ThreadID),
		turnID:    strings.TrimSpace(options.TurnID),
		namespace: dynamicToolNamespaceName(namespace),
		tool:      function.Name,
		now:       options.Now,
	})
	return err
}

type dynamicToolExecutor struct {
	spec      tool.Spec
	caller    DynamicToolCaller
	threadID  string
	turnID    string
	namespace *string
	tool      string
	now       func() time.Time
}

func (e *dynamicToolExecutor) Spec() tool.Spec {
	if e == nil {
		return tool.Spec{}
	}
	return e.spec
}

func (e *dynamicToolExecutor) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	if e == nil || invocation == nil {
		return nil, fmt.Errorf("%w: dynamic tool invocation is nil", tool.ErrToolInvalidCall)
	}
	if invocation.Payload.Kind != tool.PayloadFunction {
		return nil, tool.RespondToModel("dynamic tool handler received unsupported payload")
	}
	if e.caller == nil {
		return nil, tool.RespondToModel("dynamic tool request sink is not configured")
	}
	arguments := dynamicToolArguments(invocation.Payload.Arguments)
	params := &DynamicToolCallParams{
		ThreadID:  e.threadID,
		TurnID:    e.turnID,
		CallID:    invocation.CallID,
		Namespace: e.namespace,
		Tool:      e.tool,
		Arguments: arguments,
	}
	started := e.timeNow()
	var response DynamicToolCallResponse
	if err := e.caller.Request(ctx, dynamicToolServerRequestMethod, params, &response); err != nil {
		return e.fallbackOutput(invocation, arguments, started, "dynamic tool request failed"), nil
	}
	var valid bool
	response.ContentItems, valid = normalizeDynamicToolContentItems(response.ContentItems)
	if !valid {
		response.Success = false
	}
	body := dynamicToolContentItemsText(response.ContentItems)
	if body == "" && !response.Success {
		body = "dynamic tool response was invalid"
		response.ContentItems = []DynamicToolCallOutputContentItem{{Type: "inputText", Text: body}}
	}
	return &tool.Output{
		CallID:      invocation.CallID,
		ToolName:    invocation.ToolName,
		Success:     response.Success,
		Body:        body,
		Data:        e.outputData(arguments, response.ContentItems, response.Success, started),
		CompletedAt: e.timeNow(),
	}, nil
}

func (e *dynamicToolExecutor) fallbackOutput(invocation *tool.Invocation, arguments any, started time.Time, message string) *tool.Output {
	if strings.TrimSpace(message) == "" {
		message = "dynamic tool request failed"
	}
	items := []DynamicToolCallOutputContentItem{{Type: "inputText", Text: message}}
	return &tool.Output{
		CallID:      invocation.CallID,
		ToolName:    invocation.ToolName,
		Success:     false,
		Body:        message,
		Error:       message,
		Data:        e.outputData(arguments, items, false, started),
		CompletedAt: e.timeNow(),
	}
}

func (e *dynamicToolExecutor) outputData(arguments any, contentItems []DynamicToolCallOutputContentItem, success bool, started time.Time) map[string]any {
	data := map[string]any{
		"dynamicToolCall": true,
		"namespace":       e.namespace,
		"tool":            e.tool,
		"arguments":       arguments,
		"contentItems":    dynamicToolContentItemsAny(contentItems),
		"content_items":   dynamicToolModelContentItemsAny(contentItems),
		"success":         success,
	}
	if !started.IsZero() {
		data["durationMs"] = e.timeNow().Sub(started).Milliseconds()
		data["duration_ms"] = data["durationMs"]
	}
	return data
}

func (e *dynamicToolExecutor) timeNow() time.Time {
	if e != nil && e.now != nil {
		return e.now().UTC()
	}
	return time.Now().UTC()
}

func dynamicToolInputSchema(value any) (map[string]any, bool) {
	if value == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}, true
	}
	if schema, ok := value.(map[string]any); ok {
		// Mirrors Rust parse_dynamic_tool: the model-visible parameters go
		// through the JsonSchema subset policy (sanitize, prune unreachable
		// $defs, drop non-subset fields, compact oversized schemas).
		return jsonschema.Normalize(cloneMapAny(schema)), true
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, false
	}
	return jsonschema.Normalize(schema), true
}

func dynamicToolNamespaceName(namespace *DynamicToolNamespaceSpec) *string {
	if namespace == nil || strings.TrimSpace(namespace.Name) == "" {
		return nil
	}
	value := strings.TrimSpace(namespace.Name)
	return &value
}

func dynamicToolNamespaceDescription(namespace *DynamicToolNamespaceSpec) string {
	if namespace == nil || strings.TrimSpace(namespace.Description) == "" {
		return ""
	}
	return strings.TrimSpace(namespace.Description)
}

func dynamicToolArguments(arguments string) any {
	if strings.TrimSpace(arguments) == "" {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal([]byte(arguments), &value); err != nil {
		return arguments
	}
	return value
}

func normalizeDynamicToolContentItems(items []DynamicToolCallOutputContentItem) ([]DynamicToolCallOutputContentItem, bool) {
	out := make([]DynamicToolCallOutputContentItem, 0, len(items))
	for i := range items {
		item := items[i]
		switch item.Type {
		case "inputImage", "input_image":
			item.Type = "inputImage"
			if isRemoteImageURL(item.ImageURL) {
				return []DynamicToolCallOutputContentItem{{Type: "inputText", Text: remoteImageURLError}}, false
			}
		case "inputAudio", "input_audio":
			item.Type = "inputAudio"
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.AudioURL)), "data:audio/") {
				return []DynamicToolCallOutputContentItem{{Type: "inputText", Text: "audio output must use an inline data:audio URL"}}, false
			}
		default:
			item.Type = "inputText"
		}
		out = append(out, item)
	}
	if out == nil {
		return []DynamicToolCallOutputContentItem{}, true
	}
	return out, true
}

func dynamicToolContentItemsText(items []DynamicToolCallOutputContentItem) string {
	parts := make([]string, 0, len(items))
	for i := range items {
		if items[i].Type != "inputText" {
			continue
		}
		if strings.TrimSpace(items[i].Text) != "" {
			parts = append(parts, items[i].Text)
		}
	}
	return strings.Join(parts, "\n")
}

func dynamicToolContentItemsAny(items []DynamicToolCallOutputContentItem) []any {
	out := make([]any, 0, len(items))
	for i := range items {
		if items[i].Type == "inputImage" {
			out = append(out, map[string]any{"type": "inputImage", "imageUrl": items[i].ImageURL})
			continue
		}
		if items[i].Type == "inputAudio" {
			out = append(out, map[string]any{"type": "inputAudio", "audioUrl": items[i].AudioURL})
			continue
		}
		out = append(out, map[string]any{"type": "inputText", "text": items[i].Text})
	}
	return out
}

func dynamicToolModelContentItemsAny(items []DynamicToolCallOutputContentItem) []any {
	out := make([]any, 0, len(items))
	for i := range items {
		if items[i].Type == "inputImage" {
			out = append(out, map[string]any{"type": "input_image", "image_url": items[i].ImageURL, "detail": "auto"})
			continue
		}
		if items[i].Type == "inputAudio" {
			out = append(out, map[string]any{"type": "input_audio", "audio_url": items[i].AudioURL})
			continue
		}
		out = append(out, map[string]any{"type": "input_text", "text": items[i].Text})
	}
	return out
}

func isRemoteImageURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func cloneMapAny(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
