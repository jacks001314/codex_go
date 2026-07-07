package bottompane

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"codex_go/internal/tui"
)

// Rust parity: codex-rs/tui/src/bottom_pane/mcp_server_elicitation.rs.

type ElicitationAction string

const (
	ElicitationAccept  ElicitationAction = "accept"
	ElicitationDecline ElicitationAction = "decline"
	ElicitationCancel  ElicitationAction = "cancel"
)

type ElicitationResponseMode string

const (
	ElicitationFormContent    ElicitationResponseMode = "form_content"
	ElicitationApprovalAction ElicitationResponseMode = "approval_action"
)

type ElicitationFieldKind string

const (
	ElicitationText        ElicitationFieldKind = "text"
	ElicitationSecret      ElicitationFieldKind = "secret"
	ElicitationBoolean     ElicitationFieldKind = "boolean"
	ElicitationSelect      ElicitationFieldKind = "select"
	ElicitationMultiSelect ElicitationFieldKind = "multi_select"
)

const (
	ApprovalAcceptOnceValue    = "approve_once"
	ApprovalAcceptSessionValue = "approve_session"
	ApprovalAcceptAlwaysValue  = "approve_always"
	ApprovalDeclineValue       = "decline"
	ApprovalCancelValue        = "cancel"
)

type ElicitationOption struct {
	Value string
	Label string
}

type ElicitationField struct {
	Name        string
	Title       string
	Description string
	Required    bool
	Kind        ElicitationFieldKind
	Options     []ElicitationOption
	Value       string
	BoolValue   bool
	Selected    []string
}

type ElicitationFormRequest struct {
	ThreadID     string
	TurnID       string
	ServerName   string
	RequestID    string
	Message      string
	ResponseMode ElicitationResponseMode
	Fields       []ElicitationField
	Meta         map[string]any
}

type ElicitationDecision struct {
	Action  ElicitationAction
	Content map[string]any
	Persist string
}

func NewElicitationFormRequest(serverName string, requestID string, message string, schema any, meta map[string]any) (*ElicitationFormRequest, error) {
	fields, err := ElicitationFieldsFromSchema(schema)
	if err != nil {
		return nil, err
	}
	mode := ElicitationFormContent
	if isApprovalSchema(fields, meta) {
		mode = ElicitationApprovalAction
	}
	return &ElicitationFormRequest{
		ServerName:   strings.TrimSpace(serverName),
		RequestID:    strings.TrimSpace(requestID),
		Message:      strings.TrimSpace(message),
		ResponseMode: mode,
		Fields:       fields,
		Meta:         cloneMeta(meta),
	}, nil
}

func ElicitationFieldsFromSchema(schema any) ([]ElicitationField, error) {
	raw, err := schemaAsMap(schema)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	properties, _ := raw["properties"].(map[string]any)
	required := map[string]bool{}
	if values, ok := raw["required"].([]any); ok {
		for _, value := range values {
			name := strings.TrimSpace(fmt.Sprint(value))
			if name != "" {
				required[name] = true
			}
		}
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]ElicitationField, 0, len(names))
	for _, name := range names {
		property, _ := properties[name].(map[string]any)
		field := fieldFromProperty(name, property)
		field.Required = required[name]
		fields = append(fields, field)
	}
	return fields, nil
}

func (r *ElicitationFormRequest) SetValue(name string, value string) bool {
	if r == nil {
		return false
	}
	for i := range r.Fields {
		if r.Fields[i].Name != name {
			continue
		}
		switch r.Fields[i].Kind {
		case ElicitationBoolean:
			r.Fields[i].BoolValue = value == "true" || value == "1" || strings.EqualFold(value, "yes")
		case ElicitationSelect:
			if optionExists(r.Fields[i].Options, value) {
				r.Fields[i].Value = value
			} else {
				return false
			}
		case ElicitationMultiSelect:
			if optionExists(r.Fields[i].Options, value) {
				r.Fields[i].Selected = []string{value}
			} else {
				return false
			}
		default:
			r.Fields[i].Value = value
		}
		return true
	}
	return false
}

func (r *ElicitationFormRequest) ToggleBool(name string) bool {
	if r == nil {
		return false
	}
	for i := range r.Fields {
		if r.Fields[i].Name == name && r.Fields[i].Kind == ElicitationBoolean {
			r.Fields[i].BoolValue = !r.Fields[i].BoolValue
			return true
		}
	}
	return false
}

func (r *ElicitationFormRequest) ToggleOption(name string, value string) bool {
	if r == nil {
		return false
	}
	for i := range r.Fields {
		field := &r.Fields[i]
		if field.Name != name || field.Kind != ElicitationMultiSelect || !optionExists(field.Options, value) {
			continue
		}
		for j, selected := range field.Selected {
			if selected == value {
				field.Selected = append(field.Selected[:j], field.Selected[j+1:]...)
				return true
			}
		}
		field.Selected = append(field.Selected, value)
		sort.Strings(field.Selected)
		return true
	}
	return false
}

func (r *ElicitationFormRequest) Submit() (ElicitationDecision, error) {
	if r == nil {
		return ElicitationDecision{Action: ElicitationCancel}, nil
	}
	if r.ResponseMode == ElicitationApprovalAction {
		value := firstFieldValue(r.Fields)
		action, persist := approvalDecision(value)
		return ElicitationDecision{Action: action, Persist: persist}, nil
	}
	content := map[string]any{}
	for _, field := range r.Fields {
		switch field.Kind {
		case ElicitationBoolean:
			content[field.Name] = field.BoolValue
		case ElicitationMultiSelect:
			content[field.Name] = append([]string(nil), field.Selected...)
		case ElicitationSelect:
			if field.Required && strings.TrimSpace(field.Value) == "" {
				return ElicitationDecision{}, fmt.Errorf("field %q is required", field.Name)
			}
			content[field.Name] = field.Value
		default:
			if field.Required && strings.TrimSpace(field.Value) == "" {
				return ElicitationDecision{}, fmt.Errorf("field %q is required", field.Name)
			}
			content[field.Name] = field.Value
		}
	}
	return ElicitationDecision{Action: ElicitationAccept, Content: content}, nil
}

func (r *ElicitationFormRequest) Cancel() ElicitationDecision {
	return ElicitationDecision{Action: ElicitationCancel}
}

func (r *ElicitationFormRequest) RenderLines(width int) []string {
	if r == nil {
		return nil
	}
	lines := []string{}
	if r.Message != "" {
		lines = append(lines, tui.AdaptiveWrapLine(r.Message, tui.WrapOptions{Width: width, BreakWords: true})...)
	}
	for _, field := range r.Fields {
		label := field.Title
		if label == "" {
			label = field.Name
		}
		if field.Required {
			label += " *"
		}
		value := field.DisplayValue()
		row := label
		if value != "" {
			row += ": " + value
		}
		lines = append(lines, tui.AdaptiveWrapLine("  "+row, tui.WrapOptions{
			Width:            width,
			SubsequentIndent: "    ",
			BreakWords:       true,
		})...)
		if field.Description != "" {
			lines = append(lines, tui.AdaptiveWrapLine("    "+field.Description, tui.WrapOptions{
				Width:            width,
				SubsequentIndent: "    ",
				BreakWords:       true,
			})...)
		}
	}
	return lines
}

func (f ElicitationField) DisplayValue() string {
	switch f.Kind {
	case ElicitationSecret:
		if f.Value == "" {
			return ""
		}
		return strings.Repeat("*", len([]rune(f.Value)))
	case ElicitationBoolean:
		if f.BoolValue {
			return "true"
		}
		return "false"
	case ElicitationMultiSelect:
		return strings.Join(f.Selected, ", ")
	default:
		return f.Value
	}
}

func fieldFromProperty(name string, property map[string]any) ElicitationField {
	field := ElicitationField{
		Name:        strings.TrimSpace(name),
		Title:       stringFromMap(property, "title"),
		Description: stringFromMap(property, "description"),
		Kind:        ElicitationText,
		Value:       stringFromMap(property, "default"),
	}
	propertyType := strings.TrimSpace(stringFromMap(property, "type"))
	format := strings.TrimSpace(stringFromMap(property, "format"))
	options := optionsFromProperty(property)
	switch {
	case propertyType == "boolean":
		field.Kind = ElicitationBoolean
		field.BoolValue = boolFromMap(property, "default")
	case propertyType == "array":
		field.Kind = ElicitationMultiSelect
		field.Options = options
		field.Selected = stringSliceFromAny(property["default"])
	case len(options) > 0:
		field.Kind = ElicitationSelect
		field.Options = options
		if field.Value == "" {
			field.Value = options[0].Value
		}
	case format == "password" || format == "secret":
		field.Kind = ElicitationSecret
	default:
		field.Kind = ElicitationText
	}
	return field
}

func optionsFromProperty(property map[string]any) []ElicitationOption {
	if values, ok := property["enum"].([]any); ok {
		options := make([]ElicitationOption, 0, len(values))
		for _, value := range values {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				options = append(options, ElicitationOption{Value: text, Label: text})
			}
		}
		return options
	}
	if values, ok := property["anyOf"].([]any); ok {
		return optionsFromAnyOf(values)
	}
	if items, ok := property["items"].(map[string]any); ok {
		return optionsFromProperty(items)
	}
	return nil
}

func optionsFromAnyOf(values []any) []ElicitationOption {
	options := make([]ElicitationOption, 0, len(values))
	for _, value := range values {
		entry, _ := value.(map[string]any)
		constValue := strings.TrimSpace(stringFromMap(entry, "const"))
		if constValue == "" {
			continue
		}
		label := stringFromMap(entry, "title")
		if label == "" {
			label = constValue
		}
		options = append(options, ElicitationOption{Value: constValue, Label: label})
	}
	return options
}

func isApprovalSchema(fields []ElicitationField, meta map[string]any) bool {
	if strings.EqualFold(fmt.Sprint(meta["response_mode"]), string(ElicitationApprovalAction)) {
		return true
	}
	if len(fields) != 1 || fields[0].Kind != ElicitationSelect {
		return false
	}
	values := map[string]bool{}
	for _, option := range fields[0].Options {
		values[option.Value] = true
	}
	return values[ApprovalAcceptOnceValue] &&
		values[ApprovalDeclineValue] &&
		values[ApprovalCancelValue]
}

func approvalDecision(value string) (ElicitationAction, string) {
	switch value {
	case ApprovalAcceptOnceValue:
		return ElicitationAccept, ""
	case ApprovalAcceptSessionValue:
		return ElicitationAccept, "session"
	case ApprovalAcceptAlwaysValue:
		return ElicitationAccept, "always"
	case ApprovalDeclineValue:
		return ElicitationDecline, ""
	default:
		return ElicitationCancel, ""
	}
}

func firstFieldValue(fields []ElicitationField) string {
	if len(fields) == 0 {
		return ApprovalCancelValue
	}
	if fields[0].Value != "" {
		return fields[0].Value
	}
	if len(fields[0].Selected) > 0 {
		return fields[0].Selected[0]
	}
	return ApprovalCancelValue
}

func schemaAsMap(schema any) (map[string]any, error) {
	if schema == nil {
		return nil, nil
	}
	switch typed := schema.(type) {
	case map[string]any:
		return typed, nil
	case []byte:
		var out map[string]any
		if err := json.Unmarshal(typed, &out); err != nil {
			return nil, err
		}
		return out, nil
	case json.RawMessage:
		var out map[string]any
		if err := json.Unmarshal(typed, &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		data, err := json.Marshal(schema)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

func optionExists(options []ElicitationOption, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func boolFromMap(values map[string]any, key string) bool {
	value, ok := values[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "true" || typed == "1" || strings.EqualFold(typed, "yes")
	default:
		return false
	}
}

func stringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func cloneMeta(meta map[string]any) map[string]any {
	if meta == nil {
		return nil
	}
	out := map[string]any{}
	for key, value := range meta {
		out[key] = value
	}
	return out
}
