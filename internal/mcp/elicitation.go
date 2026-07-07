package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type McpElicitationStringType string

const McpElicitationTypeString McpElicitationStringType = "string"

type McpElicitationNumberType string

const (
	McpElicitationTypeNumber  McpElicitationNumberType = "number"
	McpElicitationTypeInteger McpElicitationNumberType = "integer"
)

type McpElicitationBooleanType string

const McpElicitationTypeBoolean McpElicitationBooleanType = "boolean"

type McpElicitationObjectType string

const McpElicitationTypeObject McpElicitationObjectType = "object"

type McpElicitationArrayType string

const McpElicitationTypeArray McpElicitationArrayType = "array"

type McpElicitationStringFormat string

const (
	McpElicitationFormatEmail    McpElicitationStringFormat = "email"
	McpElicitationFormatURI      McpElicitationStringFormat = "uri"
	McpElicitationFormatDate     McpElicitationStringFormat = "date"
	McpElicitationFormatDateTime McpElicitationStringFormat = "date-time"
)

type McpElicitationConstOption struct {
	Const string `json:"const"`
	Title string `json:"title"`
}

type McpElicitationTitledEnumItems struct {
	AnyOf []McpElicitationConstOption `json:"anyOf"`
}

func (i *McpElicitationTitledEnumItems) MarshalJSON() ([]byte, error) {
	anyOf := append([]McpElicitationConstOption(nil), i.AnyOf...)
	if anyOf == nil {
		anyOf = []McpElicitationConstOption{}
	}
	return json.Marshal(struct {
		AnyOf []McpElicitationConstOption `json:"anyOf"`
	}{AnyOf: anyOf})
}

type McpElicitationUntitledEnumItems struct {
	Type McpElicitationStringType `json:"type"`
	Enum []string                 `json:"enum"`
}

func (i *McpElicitationUntitledEnumItems) MarshalJSON() ([]byte, error) {
	enum := append([]string(nil), i.Enum...)
	if enum == nil {
		enum = []string{}
	}
	itemType := i.Type
	if itemType == "" {
		itemType = McpElicitationTypeString
	}
	return json.Marshal(struct {
		Type McpElicitationStringType `json:"type"`
		Enum []string                 `json:"enum"`
	}{
		Type: itemType,
		Enum: enum,
	})
}

type McpElicitationPrimitiveSchema struct {
	Type        string                      `json:"type"`
	Title       string                      `json:"title,omitempty"`
	Description string                      `json:"description,omitempty"`
	MinLength   *uint64                     `json:"minLength,omitempty"`
	MaxLength   *uint64                     `json:"maxLength,omitempty"`
	Format      McpElicitationStringFormat  `json:"format,omitempty"`
	Default     any                         `json:"default,omitempty"`
	Minimum     *float64                    `json:"minimum,omitempty"`
	Maximum     *float64                    `json:"maximum,omitempty"`
	Enum        []string                    `json:"enum,omitempty"`
	AnyOf       []McpElicitationConstOption `json:"anyOf,omitempty"`
	UniqueItems *bool                       `json:"uniqueItems,omitempty"`
	Items       any                         `json:"items,omitempty"`
}

type McpElicitationStringSchema = McpElicitationPrimitiveSchema
type McpElicitationNumberSchema = McpElicitationPrimitiveSchema
type McpElicitationBooleanSchema = McpElicitationPrimitiveSchema
type McpElicitationSingleSelectEnumSchema = McpElicitationPrimitiveSchema
type McpElicitationMultiSelectEnumSchema = McpElicitationPrimitiveSchema
type McpElicitationLegacyTitledEnumSchema = McpElicitationPrimitiveSchema
type McpElicitationEnumSchema = McpElicitationPrimitiveSchema
type McpElicitationUntitledSingleSelectEnumSchema = McpElicitationPrimitiveSchema
type McpElicitationTitledSingleSelectEnumSchema = McpElicitationPrimitiveSchema
type McpElicitationUntitledMultiSelectEnumSchema = McpElicitationPrimitiveSchema
type McpElicitationTitledMultiSelectEnumSchema = McpElicitationPrimitiveSchema

type McpElicitationSchema struct {
	Schema     string                                   `json:"$schema,omitempty"`
	Type       McpElicitationObjectType                 `json:"type"`
	Properties map[string]McpElicitationPrimitiveSchema `json:"properties"`
	Required   []string                                 `json:"required,omitempty"`
}

func (s *McpElicitationSchema) MarshalJSON() ([]byte, error) {
	properties := map[string]McpElicitationPrimitiveSchema{}
	for key, value := range s.Properties {
		properties[key] = value
	}
	schemaType := s.Type
	if schemaType == "" {
		schemaType = McpElicitationTypeObject
	}
	return json.Marshal(struct {
		Schema     string                                   `json:"$schema,omitempty"`
		Type       McpElicitationObjectType                 `json:"type"`
		Properties map[string]McpElicitationPrimitiveSchema `json:"properties"`
		Required   []string                                 `json:"required,omitempty"`
	}{
		Schema:     s.Schema,
		Type:       schemaType,
		Properties: properties,
		Required:   append([]string(nil), s.Required...),
	})
}

func (s *McpElicitationSchema) Validate() error {
	if s == nil {
		return fmt.Errorf("elicitation schema is required")
	}
	if s.Type != "" && s.Type != McpElicitationTypeObject {
		return fmt.Errorf("elicitation schema type must be object")
	}
	for name, property := range s.Properties {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("elicitation schema property name is required")
		}
		switch property.Type {
		case "string", "number", "integer", "boolean", "array", "":
		default:
			return fmt.Errorf("unsupported elicitation property type %q", property.Type)
		}
	}
	required := map[string]bool{}
	for _, name := range s.Required {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("elicitation required property is empty")
		}
		if _, ok := s.Properties[name]; !ok {
			return fmt.Errorf("elicitation required property %q is not declared", name)
		}
		if required[name] {
			return fmt.Errorf("elicitation required property %q is duplicated", name)
		}
		required[name] = true
	}
	return nil
}

func (s *McpElicitationSchema) Normalize() *McpElicitationSchema {
	if s == nil {
		return nil
	}
	out := &McpElicitationSchema{
		Schema:     strings.TrimSpace(s.Schema),
		Type:       s.Type,
		Properties: map[string]McpElicitationPrimitiveSchema{},
		Required:   append([]string(nil), s.Required...),
	}
	if out.Type == "" {
		out.Type = McpElicitationTypeObject
	}
	for key, value := range s.Properties {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if value.Type == "" {
			value.Type = "string"
		}
		out.Properties[key] = value
	}
	sort.Strings(out.Required)
	return out
}
