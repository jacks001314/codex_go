package mcp

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

var ErrInvalidMCPResourceArguments = errors.New("invalid MCP resource arguments")

type MCPResourceToolSpec struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Required    []string          `json:"required,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
}

func ListMCPResourcesTool() MCPResourceToolSpec {
	return MCPResourceToolSpec{
		Name:        "list_mcp_resources",
		Description: "Lists resources provided by MCP servers.",
		Properties:  map[string]string{"server": "MCP server name", "cursor": "Opaque cursor"},
	}
}

func ListMCPResourceTemplatesTool() MCPResourceToolSpec {
	return MCPResourceToolSpec{
		Name:        "list_mcp_resource_templates",
		Description: "Lists resource templates provided by MCP servers.",
		Properties:  map[string]string{"server": "MCP server name", "cursor": "Opaque cursor"},
	}
}

func ReadMCPResourceTool() MCPResourceToolSpec {
	return MCPResourceToolSpec{
		Name:        "read_mcp_resource",
		Description: "Read a specific resource from an MCP server.",
		Required:    []string{"server", "uri"},
		Properties:  map[string]string{"server": "MCP server name", "uri": "Resource URI"},
	}
}

type ListMCPResourcesArgs struct {
	Server *string `json:"server,omitempty"`
	Cursor *string `json:"cursor,omitempty"`
}

func (a *ListMCPResourcesArgs) Normalize() {
	if a == nil {
		return
	}
	a.Server = normalizeOptionalString(a.Server)
	a.Cursor = normalizeOptionalString(a.Cursor)
}

type ListMCPResourceTemplatesArgs struct {
	Server *string `json:"server,omitempty"`
	Cursor *string `json:"cursor,omitempty"`
}

func (a *ListMCPResourceTemplatesArgs) Normalize() {
	if a == nil {
		return
	}
	a.Server = normalizeOptionalString(a.Server)
	a.Cursor = normalizeOptionalString(a.Cursor)
}

type ReadMCPResourceArgs struct {
	Server string `json:"server"`
	URI    string `json:"uri"`
}

func (a *ReadMCPResourceArgs) Validate() error {
	if a == nil {
		return ErrInvalidMCPResourceArguments
	}
	a.Server = strings.TrimSpace(a.Server)
	a.URI = strings.TrimSpace(a.URI)
	if a.Server == "" || a.URI == "" {
		return ErrInvalidMCPResourceArguments
	}
	return nil
}

type MCPResource struct {
	Server      string `json:"server,omitempty"`
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	Size        *int64 `json:"size,omitempty"`
	Annotations any    `json:"annotations,omitempty"`
	Icons       []any  `json:"icons,omitempty"`
	Meta        any    `json:"_meta,omitempty"`
}

type MCPResourceTemplate struct {
	Server      string `json:"server,omitempty"`
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	Annotations any    `json:"annotations,omitempty"`
	Meta        any    `json:"_meta,omitempty"`
}

func (t *MCPResourceTemplate) MarshalJSON() ([]byte, error) {
	if t == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		Server      string `json:"server,omitempty"`
		URITemplate string `json:"uriTemplate"`
		Name        string `json:"name"`
		Title       string `json:"title,omitempty"`
		Description string `json:"description,omitempty"`
		MimeType    string `json:"mimeType,omitempty"`
		Annotations any    `json:"annotations,omitempty"`
		Meta        any    `json:"_meta,omitempty"`
	}{
		Server:      t.Server,
		URITemplate: t.URITemplate,
		Name:        t.Name,
		Title:       t.Title,
		Description: t.Description,
		MimeType:    t.MimeType,
		Annotations: cloneJSONValue(t.Annotations),
		Meta:        cloneJSONValue(t.Meta),
	})
}

type ListMCPResourcesPayload struct {
	Server     *string       `json:"server,omitempty"`
	Resources  []MCPResource `json:"resources"`
	NextCursor *string       `json:"nextCursor,omitempty"`
}

type ListMCPResourceTemplatesPayload struct {
	Server            *string               `json:"server,omitempty"`
	ResourceTemplates []MCPResourceTemplate `json:"resourceTemplates"`
	NextCursor        *string               `json:"nextCursor,omitempty"`
}

type ReadMCPResourcePayload struct {
	Server   string `json:"server"`
	URI      string `json:"uri"`
	Contents []any  `json:"contents,omitempty"`
}

func MCPResourcesFromAllServers(resourcesByServer map[string][]MCPResource) ListMCPResourcesPayload {
	servers := sortedKeys(resourcesByServer)
	var resources []MCPResource
	for _, server := range servers {
		for _, resource := range resourcesByServer[server] {
			resource.Server = server
			resources = append(resources, resource)
		}
	}
	return ListMCPResourcesPayload{Resources: resources}
}

func MCPResourceTemplatesFromAllServers(templatesByServer map[string][]MCPResourceTemplate) ListMCPResourceTemplatesPayload {
	servers := sortedKeys(templatesByServer)
	var templates []MCPResourceTemplate
	for _, server := range servers {
		for _, template := range templatesByServer[server] {
			template.Server = server
			templates = append(templates, template)
		}
	}
	return ListMCPResourceTemplatesPayload{ResourceTemplates: templates}
}

func ParseMCPResourceArguments(raw string) (map[string]any, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, false, nil
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, false, err
	}
	if value == nil {
		return nil, false, nil
	}
	return value, true, nil
}

func ReadResourcePayloadFromResponse(server string, response *MCPResourceReadResponse) ReadMCPResourcePayload {
	payload := ReadMCPResourcePayload{Server: strings.TrimSpace(server)}
	if response == nil {
		return payload
	}
	contents := make([]any, 0, len(response.Contents))
	for _, content := range response.Contents {
		if payload.URI == "" {
			payload.URI = content.URI
		}
		contents = append(contents, resourceContentMap(content))
	}
	payload.Contents = contents
	return payload
}

func resourceContentMap(content MCPResourceContent) map[string]any {
	entry := map[string]any{"uri": content.URI}
	if strings.TrimSpace(content.MimeType) != "" {
		entry["mimeType"] = content.MimeType
	}
	if content.Text != "" {
		entry["text"] = content.Text
	}
	if content.Blob != "" {
		entry["blob"] = content.Blob
	}
	if content.Meta != nil {
		entry["_meta"] = cloneResourceMeta(content.Meta)
	}
	return entry
}

func cloneResourceMeta(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	cloned := cloneJSONValue(in)
	if out, ok := cloned.(map[string]any); ok {
		return out
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONSlice(values []any) []any {
	if values == nil {
		return nil
	}
	out := make([]any, len(values))
	for i := range values {
		out[i] = cloneJSONValue(values[i])
	}
	return out
}

func cloneMCPResourceReadResponse(in *MCPResourceReadResponse) *MCPResourceReadResponse {
	if in == nil {
		return nil
	}
	out := &MCPResourceReadResponse{Contents: make([]MCPResourceContent, len(in.Contents))}
	for i := range in.Contents {
		out.Contents[i] = cloneMCPResourceContent(&in.Contents[i])
	}
	if in.Contents == nil {
		out.Contents = nil
	}
	return out
}

func cloneMCPResourceContent(in *MCPResourceContent) MCPResourceContent {
	if in == nil {
		return MCPResourceContent{}
	}
	out := *in
	out.Meta = cloneResourceMeta(in.Meta)
	return out
}

func SerializeMCPFunctionOutput(payload any, maxBytes int) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	if maxBytes > 0 && len(data) > maxBytes {
		if maxBytes <= len("...") {
			return string(data[:maxBytes]), nil
		}
		data = append(data[:maxBytes-len("...")], '.', '.', '.')
	}
	return string(data), nil
}

func normalizeOptionalString(input *string) *string {
	if input == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*input)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
