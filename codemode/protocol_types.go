package codemode

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	ProtocolPublicToolName                    = "exec"
	ProtocolWaitToolName                      = "wait"
	ProtocolDefaultExecYieldTimeMS            = uint64(10000)
	ProtocolDefaultWaitYieldTimeMS            = uint64(10000)
	ProtocolDefaultMaxOutputTokensPerExecCall = 10000
	ProtocolMaxFrameBytes                     = 64 * 1024 * 1024
)

type CellID string

func NewCellID(value string) CellID {
	return CellID(value)
}

func (id *CellID) String() string {
	if id == nil {
		return ""
	}
	return string(*id)
}

type ToolName struct {
	Name      string  `json:"name"`
	Namespace *string `json:"namespace"`
}

func PlainToolName(name string) ToolName {
	return ToolName{Name: name}
}

func NamespacedToolName(namespace string, name string) ToolName {
	return ToolName{Name: name, Namespace: &namespace}
}

type ProtocolToolKind string

const (
	ProtocolToolKindFunction ProtocolToolKind = "function"
	ProtocolToolKindFreeform ProtocolToolKind = "freeform"
)

type ProtocolToolDefinition struct {
	Name         string           `json:"name"`
	ToolName     ToolName         `json:"tool_name"`
	Description  string           `json:"description"`
	Kind         ProtocolToolKind `json:"kind"`
	InputSchema  json.RawMessage  `json:"input_schema"`
	OutputSchema json.RawMessage  `json:"output_schema"`
}

type ImageDetail string

const (
	ImageDetailAuto     ImageDetail = "auto"
	ImageDetailLow      ImageDetail = "low"
	ImageDetailHigh     ImageDetail = "high"
	ImageDetailOriginal ImageDetail = "original"
)

const DefaultImageDetail = ImageDetailHigh

type ContentItem struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL string       `json:"image_url,omitempty"`
	Detail   *ImageDetail `json:"detail,omitempty"`
}

func InputText(text string) ContentItem {
	return ContentItem{Type: "input_text", Text: text}
}

func InputImage(imageURL string, detail *ImageDetail) ContentItem {
	return ContentItem{Type: "input_image", ImageURL: imageURL, Detail: detail}
}

type ExecuteRequest struct {
	ToolCallID      string                   `json:"tool_call_id"`
	EnabledTools    []ProtocolToolDefinition `json:"enabled_tools"`
	Source          string                   `json:"source"`
	YieldTimeMS     *uint64                  `json:"yield_time_ms"`
	MaxOutputTokens *int                     `json:"max_output_tokens"`
}

func (r *ExecuteRequest) Validate() error {
	if r == nil {
		return fmt.Errorf("execute request is nil")
	}
	if strings.TrimSpace(r.ToolCallID) == "" {
		return fmt.Errorf("tool_call_id is required")
	}
	if r.MaxOutputTokens != nil && *r.MaxOutputTokens < 0 {
		return fmt.Errorf("max_output_tokens must be non-negative")
	}
	return nil
}

type WaitRequest struct {
	CellID      CellID `json:"cell_id"`
	YieldTimeMS uint64 `json:"yield_time_ms"`
}

type WaitToPendingRequest struct {
	CellID CellID `json:"cell_id"`
}

type RuntimeResponse struct {
	Variant      string        `json:"-"`
	CellID       CellID        `json:"cell_id"`
	ContentItems []ContentItem `json:"content_items"`
	ErrorText    *string       `json:"error_text,omitempty"`
}

func Yielded(cellID CellID, items []ContentItem) RuntimeResponse {
	return RuntimeResponse{Variant: "Yielded", CellID: cellID, ContentItems: cloneContentItems(items)}
}

func Terminated(cellID CellID, items []ContentItem) RuntimeResponse {
	return RuntimeResponse{Variant: "Terminated", CellID: cellID, ContentItems: cloneContentItems(items)}
}

func Result(cellID CellID, items []ContentItem, errorText *string) RuntimeResponse {
	return RuntimeResponse{Variant: "Result", CellID: cellID, ContentItems: cloneContentItems(items), ErrorText: errorText}
}

func (r RuntimeResponse) MarshalJSON() ([]byte, error) {
	variant := r.Variant
	if variant == "" {
		variant = "Result"
	}
	body := map[string]any{
		"cell_id":       r.CellID,
		"content_items": r.ContentItems,
	}
	if variant == "Result" {
		body["error_text"] = r.ErrorText
	}
	return json.Marshal(map[string]any{variant: body})
}

func (r *RuntimeResponse) UnmarshalJSON(data []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if len(envelope) != 1 {
		return fmt.Errorf("runtime response must contain exactly one variant")
	}
	for variant, body := range envelope {
		var decoded struct {
			CellID       CellID        `json:"cell_id"`
			ContentItems []ContentItem `json:"content_items"`
			ErrorText    *string       `json:"error_text"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			return err
		}
		switch variant {
		case "Yielded", "Terminated", "Result":
			r.Variant = variant
			r.CellID = decoded.CellID
			r.ContentItems = decoded.ContentItems
			r.ErrorText = decoded.ErrorText
			return nil
		default:
			return fmt.Errorf("unknown runtime response variant %q", variant)
		}
	}
	return nil
}

type WaitOutcome struct {
	Variant  string          `json:"-"`
	Response RuntimeResponse `json:"-"`
}

func LiveCell(response RuntimeResponse) WaitOutcome {
	return WaitOutcome{Variant: "LiveCell", Response: response}
}

func MissingCell(response RuntimeResponse) WaitOutcome {
	return WaitOutcome{Variant: "MissingCell", Response: response}
}

func (o *WaitOutcome) RuntimeResponse() RuntimeResponse {
	if o == nil {
		return RuntimeResponse{}
	}
	return o.Response
}

func (o WaitOutcome) MarshalJSON() ([]byte, error) {
	variant := o.Variant
	if variant == "" {
		variant = "LiveCell"
	}
	return json.Marshal(map[string]RuntimeResponse{variant: o.Response})
}

func (o *WaitOutcome) UnmarshalJSON(data []byte) error {
	var envelope map[string]RuntimeResponse
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if len(envelope) != 1 {
		return fmt.Errorf("wait outcome must contain exactly one variant")
	}
	for variant, response := range envelope {
		switch variant {
		case "LiveCell", "MissingCell":
			o.Variant = variant
			o.Response = response
			return nil
		default:
			return fmt.Errorf("unknown wait outcome variant %q", variant)
		}
	}
	return nil
}

type ExecuteToPendingOutcome struct {
	Variant            string        `json:"-"`
	CellID             CellID        `json:"cell_id,omitempty"`
	ContentItems       []ContentItem `json:"content_items,omitempty"`
	PendingToolCallIDs []string      `json:"pending_tool_call_ids,omitempty"`
	Response           RuntimeResponse
}

type WaitToPendingOutcome struct {
	Variant  string
	Outcome  ExecuteToPendingOutcome
	Response RuntimeResponse
}

type NestedToolCall struct {
	CellID            CellID           `json:"cell_id"`
	RuntimeToolCallID string           `json:"runtime_tool_call_id"`
	ToolName          ToolName         `json:"tool_name"`
	ProtocolToolKind  ProtocolToolKind `json:"tool_kind"`
	Input             json.RawMessage  `json:"input"`
}

type EnabledToolMetadata struct {
	ToolName    ToolName         `json:"tool_name"`
	GlobalName  string           `json:"global_name"`
	Description string           `json:"description"`
	Kind        ProtocolToolKind `json:"kind"`
}

func IsProtocolNestedTool(toolName string) bool {
	return toolName != "" && toolName != ProtocolPublicToolName && toolName != ProtocolWaitToolName
}

func NormalizeIdentifier(toolKey string) string {
	var b strings.Builder
	for index, ch := range toolKey {
		valid := false
		if index == 0 {
			valid = ch == '_' || ch == '$' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
		} else {
			valid = ch == '_' || ch == '$' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
		}
		if valid {
			b.WriteRune(ch)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

func EnabledMetadata(definition ProtocolToolDefinition) EnabledToolMetadata {
	return EnabledToolMetadata{
		ToolName:    definition.ToolName,
		GlobalName:  NormalizeIdentifier(definition.Name),
		Description: definition.Description,
		Kind:        definition.Kind,
	}
}

func SortProtocolToolDefinitions(definitions []ProtocolToolDefinition) []ProtocolToolDefinition {
	out := append([]ProtocolToolDefinition(nil), definitions...)
	sort.SliceStable(out, func(i int, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func cloneContentItems(items []ContentItem) []ContentItem {
	out := make([]ContentItem, len(items))
	copy(out, items)
	return out
}
