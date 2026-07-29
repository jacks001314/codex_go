package codemode

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"codex_go/tool"
)

const (
	PublicToolName = "exec"
	WaitToolName   = "wait"
)

const FreeformGrammar = `
start: pragma_source | plain_source
pragma_source: PRAGMA_LINE NEWLINE SOURCE
plain_source: SOURCE

PRAGMA_LINE: /[ \t]*\/\/ @exec:[^\r\n]*/
NEWLINE: /\r?\n/
SOURCE: /[\s\S]+/
`

const deferredNestedToolsGuidance = `Some deferred nested tools may be omitted from this description. They are still available on the global tools object and listed in ALL_TOOLS.
To find one, filter ALL_TOOLS by name and description.`

const execDescriptionTemplate = `Run JavaScript code to orchestrate/compose tool calls
- Evaluates the provided JavaScript code in a fresh V8 isolate as an async module.
- All nested tools are available on the global tools object. Tool names are exposed as normalized JavaScript identifiers.
- Nested tool methods take either a string or an object as their input argument.
- Nested tools return either an object or a string, based on the description.
- Runs raw JavaScript -- no Node, no file system, no network access, no console.
- Accepts raw JavaScript source text, not JSON, quoted strings, or markdown code fences.
- You may optionally start the tool input with a first-line pragma like // @exec: {"yield_time_ms": 10000, "max_output_tokens": 1000}.
- yield_time_ms asks exec to yield early if the script is still running. Defaults to 10000 ms.
- max_output_tokens sets the token budget for direct exec results. Defaults to 10000 tokens.
- When the JS code is fully evaluated, the isolate's lifetime ends and unawaited promises are silently discarded.

Global helpers:
- exit(): Immediately ends the current script successfully.
- text(value): Appends a text item. Non-string values are stringified when possible.
- image(value), audio(value), generatedImage(value): Append supported media results.
- store(key, value) and load(key): Persist serializable values within the code-mode session.
- notify(value): Immediately injects an extra custom_tool_call_output.
- setTimeout(callback, delayMs) and clearTimeout(id): Schedule or cancel timers.
- ALL_TOOLS: Metadata for enabled nested tools as { name, description } entries.
- yield_control(): Yields accumulated output while the script keeps running.`

var (
	ErrCellNotFound = errors.New("code mode cell not found")
	ErrCellComplete = errors.New("code mode cell already complete")
)

type ToolKind string

const (
	ToolKindFunction ToolKind = "function"
	ToolKindFreeform ToolKind = "freeform"
)

type ToolDefinition struct {
	Name         string         `json:"name"`
	ToolName     tool.ToolName  `json:"tool_name"`
	Description  string         `json:"description"`
	Kind         ToolKind       `json:"kind"`
	InputSchema  map[string]any `json:"input_schema,omitempty"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
}

type NamespaceDescription struct {
	Name        string
	Description string
}

type FreeformSpec struct {
	Name        string
	Description string
	Syntax      string
	Grammar     string
}

type WaitSpec struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type ToolSurface struct {
	Definitions            []ToolDefinition
	Exec                   FreeformSpec
	Wait                   WaitSpec
	DeferredToolsAvailable bool
}

type ExecRequest struct {
	Source string
	Pragma string
}

type WaitParams struct {
	CellID      string `json:"cell_id"`
	YieldTimeMS int    `json:"yield_time_ms,omitempty"`
	MaxTokens   int    `json:"max_tokens,omitempty"`
	Terminate   bool   `json:"terminate,omitempty"`
}

func IsNestedTool(name string) bool {
	return name != "" && name != PublicToolName && name != WaitToolName
}

func IsPublicToolName(name tool.ToolName) bool {
	return name.Namespace == "" && name.Name == PublicToolName
}

func HasExecTool(specs []tool.Spec) bool {
	for _, spec := range specs {
		if IsPublicToolName(spec.Name) && spec.Freeform != nil {
			return true
		}
	}
	return false
}

func NameForToolName(name tool.ToolName) string {
	if name.Namespace == "" {
		return name.Name
	}
	if strings.HasSuffix(name.Namespace, "_") || strings.HasPrefix(name.Name, "_") {
		return name.Namespace + name.Name
	}
	return name.Namespace + "__" + name.Name
}

func BuildExecTool(definitions []ToolDefinition, namespaces map[string]NamespaceDescription, codeModeOnly bool, deferredToolsAvailable bool) FreeformSpec {
	return FreeformSpec{
		Name:        PublicToolName,
		Description: BuildExecToolDescription(definitions, namespaces, codeModeOnly, deferredToolsAvailable),
		Syntax:      "lark",
		Grammar:     FreeformGrammar,
	}
}

func BuildWaitTool() WaitSpec {
	return WaitSpec{
		Name:        WaitToolName,
		Description: fmt.Sprintf("Waits on a yielded `%s` cell and returns new output or completion.\n%s", PublicToolName, BuildWaitToolDescription()),
		Parameters: map[string]any{
			"required": []string{"cell_id"},
			"properties": map[string]any{
				"cell_id":       "Identifier of the running exec cell.",
				"yield_time_ms": "Wait before yielding more output. Defaults to 10000 ms.",
				"max_tokens":    "Output token budget for this wait call. Defaults to 10000 tokens.",
				"terminate":     "True stops the running exec cell; false or omitted waits for output.",
			},
		},
	}
}

func BuildToolSurface(registry *tool.Registry, namespaces map[string]NamespaceDescription, codeModeOnly bool) *ToolSurface {
	specs := []tool.Spec{}
	deferred := false
	if registry != nil {
		for _, spec := range registry.NamesAsSpecs() {
			if spec.Exposure == tool.ExposureHidden {
				continue
			}
			if spec.Exposure == tool.ExposureDiscoverable {
				deferred = true
				continue
			}
			specs = append(specs, spec)
		}
	}
	definitions := CollectPromptDefinitions(specs)
	return &ToolSurface{
		Definitions:            definitions,
		Exec:                   BuildExecTool(definitions, namespaces, codeModeOnly, deferred),
		Wait:                   BuildWaitTool(),
		DeferredToolsAvailable: deferred,
	}
}

func BuildExecToolDescription(definitions []ToolDefinition, namespaces map[string]NamespaceDescription, codeModeOnly bool, deferredToolsAvailable bool) string {
	return BuildExecToolDescriptionWithDeferred(definitions, nil, namespaces, codeModeOnly, deferredToolsAvailable)
}

func BuildExecToolDescriptionWithDeferred(definitions []ToolDefinition, deferredDefinitions []ToolDefinition, namespaces map[string]NamespaceDescription, codeModeOnly bool, deferredToolsAvailable bool) string {
	sections := []string{execDescriptionTemplate}
	if deferredToolsAvailable || len(deferredDefinitions) > 0 {
		sections = append(sections, deferredNestedToolsGuidance)
	}
	if !codeModeOnly {
		return strings.Join(sections, "\n\n")
	}

	definitions = SortDefinitions(definitions)
	currentNamespace := ""
	for _, definition := range definitions {
		if !IsNestedTool(definition.Name) {
			continue
		}
		namespace := definition.ToolName.Namespace
		if namespace != currentNamespace {
			if namespaceDescription, ok := namespaces[namespace]; ok {
				description := strings.TrimSpace(namespaceDescription.Description)
				if description != "" {
					name := strings.TrimSpace(namespaceDescription.Name)
					if name == "" {
						name = namespace
					}
					sections = append(sections, "## "+name+"\n"+description)
				}
			}
			currentNamespace = namespace
		}
		globalName := NormalizeIdentifier(definition.Name)
		heading := "### `" + globalName + "`"
		if globalName != definition.Name {
			heading += " (`" + definition.Name + "`)"
		}
		sections = append(sections, heading+"\n"+strings.TrimSpace(RenderSample(definition)))
	}
	return strings.Join(sections, "\n\n")
}

func BuildWaitToolDescription() string {
	return "Use it after an exec call yields a cell id. Set terminate to stop a still-running cell."
}

func AugmentToolDefinition(definition ToolDefinition) ToolDefinition {
	if definition.Name == PublicToolName {
		return definition
	}
	definition.Description = RenderSample(definition)
	return definition
}

func RenderSample(definition ToolDefinition) string {
	if definition.Name == "" {
		return ""
	}
	inputName := "args"
	inputType := RenderJSONSchemaToTypeScript(definition.InputSchema)
	if definition.Kind == ToolKindFreeform {
		inputName = "input"
		inputType = "string"
	}
	outputType := RenderJSONSchemaToTypeScript(definition.OutputSchema)
	if structured, ok := mcpStructuredContentSchema(definition.OutputSchema); ok {
		structuredType := RenderJSONSchemaToTypeScript(structured)
		if structuredType == "unknown" {
			outputType = "CallToolResult"
		} else {
			outputType = "CallToolResult<" + structuredType + ">"
		}
	}
	declaration := fmt.Sprintf(
		"declare const tools: { %s(%s: %s): Promise<%s>; };",
		NormalizeIdentifier(definition.Name), inputName, inputType, outputType,
	)
	description := strings.TrimSpace(definition.Description)
	if description == "" {
		return "exec tool declaration:\n```ts\n" + declaration + "\n```"
	}
	return description + "\n\nexec tool declaration:\n```ts\n" + declaration + "\n```"
}

func AugmentToolSpec(spec tool.Spec) tool.Spec {
	if spec.Exposure == tool.ExposureDirectModelOnly {
		return spec
	}
	if spec.Name.Key() == tool.ToolSearchName {
		return spec
	}
	definition := ToolDefinition{
		Name:         NameForToolName(spec.Name),
		ToolName:     spec.Name,
		Description:  spec.Description,
		Kind:         ToolKindFunction,
		InputSchema:  spec.InputSchema,
		OutputSchema: spec.OutputSchema,
	}
	if spec.Freeform != nil {
		definition.Kind = ToolKindFreeform
		definition.InputSchema = nil
	}
	if !IsNestedTool(definition.Name) {
		return spec
	}
	spec.Description = AugmentToolDefinition(definition).Description
	return spec
}

func AugmentToolSpecs(specs []tool.Spec) []tool.Spec {
	out := append([]tool.Spec(nil), specs...)
	for index := range out {
		out[index] = AugmentToolSpec(out[index])
	}
	return out
}

func RenderJSONSchemaToTypeScript(schema any) string {
	return renderJSONSchemaToTypeScript(schema)
}

func renderJSONSchemaToTypeScript(schema any) string {
	if schema == nil {
		return "unknown"
	}
	if boolean, ok := schema.(bool); ok {
		if boolean {
			return "unknown"
		}
		return "never"
	}
	object, ok := schemaObject(schema)
	if !ok {
		return "unknown"
	}
	if value, exists := object["const"]; exists {
		return renderJSONSchemaLiteral(value)
	}
	if values := schemaArray(object["enum"]); len(values) > 0 {
		rendered := make([]string, 0, len(values))
		for _, value := range values {
			rendered = append(rendered, renderJSONSchemaLiteral(value))
		}
		return strings.Join(rendered, " | ")
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if variants := schemaArray(object[key]); len(variants) > 0 {
			rendered := make([]string, 0, len(variants))
			for _, variant := range variants {
				rendered = append(rendered, renderJSONSchemaToTypeScript(variant))
			}
			return strings.Join(rendered, " | ")
		}
	}
	if variants := schemaArray(object["allOf"]); len(variants) > 0 {
		rendered := make([]string, 0, len(variants))
		for _, variant := range variants {
			rendered = append(rendered, renderJSONSchemaToTypeScript(variant))
		}
		return strings.Join(rendered, " & ")
	}
	if schemaTypes := schemaArray(object["type"]); len(schemaTypes) > 0 {
		rendered := make([]string, 0, len(schemaTypes))
		for _, value := range schemaTypes {
			if schemaType, ok := value.(string); ok {
				rendered = append(rendered, renderJSONSchemaType(object, schemaType))
			}
		}
		if len(rendered) > 0 {
			return strings.Join(rendered, " | ")
		}
	}
	if schemaType, ok := object["type"].(string); ok {
		return renderJSONSchemaType(object, schemaType)
	}
	if _, ok := object["properties"]; ok {
		return renderJSONSchemaObject(object)
	}
	if _, ok := object["additionalProperties"]; ok {
		return renderJSONSchemaObject(object)
	}
	if _, ok := object["required"]; ok {
		return renderJSONSchemaObject(object)
	}
	if _, ok := object["items"]; ok {
		return renderJSONSchemaArray(object)
	}
	if _, ok := object["prefixItems"]; ok {
		return renderJSONSchemaArray(object)
	}
	return "unknown"
}

func renderJSONSchemaType(object map[string]any, schemaType string) string {
	switch schemaType {
	case "string":
		return "string"
	case "number", "integer":
		return "number"
	case "boolean":
		return "boolean"
	case "null":
		return "null"
	case "array":
		return renderJSONSchemaArray(object)
	case "object":
		return renderJSONSchemaObject(object)
	default:
		return "unknown"
	}
}

func renderJSONSchemaArray(object map[string]any) string {
	if items, ok := object["items"]; ok {
		return "Array<" + renderJSONSchemaToTypeScript(items) + ">"
	}
	if items := schemaArray(object["prefixItems"]); len(items) > 0 {
		rendered := make([]string, 0, len(items))
		for _, item := range items {
			rendered = append(rendered, renderJSONSchemaToTypeScript(item))
		}
		return "[" + strings.Join(rendered, ", ") + "]"
	}
	return "unknown[]"
}

func renderJSONSchemaObject(object map[string]any) string {
	required := map[string]bool{}
	for _, value := range schemaArray(object["required"]) {
		if name, ok := value.(string); ok {
			required[name] = true
		}
	}
	properties, _ := schemaObject(object["properties"])
	names := make([]string, 0, len(properties))
	hasDescription := false
	for name, value := range properties {
		names = append(names, name)
		if property, ok := schemaObject(value); ok {
			if description, ok := property["description"].(string); ok && description != "" {
				hasDescription = true
			}
		}
	}
	sort.Strings(names)
	if hasDescription {
		lines := []string{"{"}
		for _, name := range names {
			value := properties[name]
			if property, ok := schemaObject(value); ok {
				if description, ok := property["description"].(string); ok {
					for _, line := range strings.Split(description, "\n") {
						if line = strings.TrimSpace(line); line != "" {
							lines = append(lines, "  // "+line)
						}
					}
				}
			}
			lines = append(lines, "  "+renderJSONSchemaProperty(name, value, required))
		}
		appendAdditionalProperties(&lines, object, properties, "  ")
		lines = append(lines, "}")
		return strings.Join(lines, "\n")
	}
	lines := make([]string, 0, len(names)+1)
	for _, name := range names {
		lines = append(lines, renderJSONSchemaProperty(name, properties[name], required))
	}
	appendAdditionalProperties(&lines, object, properties, "")
	if len(lines) == 0 {
		return "{}"
	}
	return "{ " + strings.Join(lines, " ") + " }"
}

func appendAdditionalProperties(lines *[]string, object map[string]any, properties map[string]any, prefix string) {
	additional, exists := object["additionalProperties"]
	if exists {
		switch value := additional.(type) {
		case bool:
			if value {
				*lines = append(*lines, prefix+"[key: string]: unknown;")
			}
		default:
			*lines = append(*lines, prefix+"[key: string]: "+renderJSONSchemaToTypeScript(value)+";")
		}
	} else if len(properties) == 0 {
		*lines = append(*lines, prefix+"[key: string]: unknown;")
	}
}

func renderJSONSchemaProperty(name string, schema any, required map[string]bool) string {
	optional := "?"
	if required[name] {
		optional = ""
	}
	return renderJSONSchemaPropertyName(name) + optional + ": " + renderJSONSchemaToTypeScript(schema) + ";"
}

func renderJSONSchemaPropertyName(name string) string {
	if NormalizeIdentifier(name) == name {
		return name
	}
	encoded, err := json.Marshal(name)
	if err != nil {
		return fmt.Sprintf("%q", name)
	}
	return string(encoded)
}

func renderJSONSchemaLiteral(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "unknown"
	}
	return string(encoded)
}

func schemaObject(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

func schemaArray(value any) []any {
	switch values := value.(type) {
	case []any:
		return values
	case []string:
		out := make([]any, len(values))
		for index := range values {
			out[index] = values[index]
		}
		return out
	default:
		return nil
	}
}

func mcpStructuredContentSchema(outputSchema map[string]any) (any, bool) {
	properties, ok := schemaObject(outputSchema["properties"])
	if !ok {
		return nil, false
	}
	content, ok := schemaObject(properties["content"])
	if !ok || content["type"] != "array" {
		return nil, false
	}
	items, ok := schemaObject(content["items"])
	if !ok || items["type"] != "object" {
		return nil, false
	}
	isError, ok := schemaObject(properties["isError"])
	if !ok || isError["type"] != "boolean" {
		return nil, false
	}
	metadata, ok := schemaObject(properties["_meta"])
	if !ok || metadata["type"] != "object" {
		return nil, false
	}
	structured, exists := properties["structuredContent"]
	if !exists {
		return true, true
	}
	return structured, true
}

func CollectDefinitions(specs []tool.Spec) []ToolDefinition {
	definitions := CollectPromptDefinitions(specs)
	for index := range definitions {
		definitions[index] = AugmentToolDefinition(definitions[index])
	}
	return definitions
}

func CollectPromptDefinitions(specs []tool.Spec) []ToolDefinition {
	definitions := make([]ToolDefinition, 0, len(specs))
	for _, spec := range specs {
		name := spec.Name
		definition := ToolDefinition{
			Name:         NameForToolName(name),
			ToolName:     name,
			Description:  spec.Description,
			Kind:         ToolKindFunction,
			InputSchema:  spec.InputSchema,
			OutputSchema: spec.OutputSchema,
		}
		if spec.Freeform != nil {
			definition.Kind = ToolKindFreeform
			definition.InputSchema = map[string]any{
				"type":       "grammar",
				"syntax":     spec.Freeform.Syntax,
				"definition": spec.Freeform.Definition,
			}
		}
		if IsNestedTool(definition.Name) {
			definitions = append(definitions, definition)
		}
	}
	return SortDefinitions(definitions)
}

func SortDefinitions(definitions []ToolDefinition) []ToolDefinition {
	out := append([]ToolDefinition(nil), definitions...)
	sort.SliceStable(out, func(i int, j int) bool {
		return out[i].Name < out[j].Name
	})
	deduped := out[:0]
	for _, definition := range out {
		if len(deduped) > 0 && deduped[len(deduped)-1].Name == definition.Name {
			continue
		}
		deduped = append(deduped, definition)
	}
	return deduped
}

func ParseExecRequest(source string) ExecRequest {
	source = strings.TrimPrefix(source, "\ufeff")
	lines := strings.SplitN(source, "\n", 2)
	if len(lines) == 2 && strings.HasPrefix(strings.TrimSpace(lines[0]), "// @exec:") {
		return ExecRequest{Pragma: strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[0]), "// @exec:")), Source: lines[1]}
	}
	return ExecRequest{Source: source}
}

func ParseWaitParams(arguments string) (WaitParams, error) {
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	var params WaitParams
	if err := json.Unmarshal([]byte(arguments), &params); err != nil {
		return WaitParams{}, err
	}
	return params, params.Validate()
}

func (p *WaitParams) Validate() error {
	if p == nil {
		return fmt.Errorf("wait params are nil")
	}
	if strings.TrimSpace(p.CellID) == "" {
		return fmt.Errorf("cell_id is required")
	}
	if p.YieldTimeMS < 0 {
		return fmt.Errorf("yield_time_ms must be non-negative")
	}
	if p.MaxTokens < 0 {
		return fmt.Errorf("max_tokens must be non-negative")
	}
	return nil
}

type CellStatus string

const (
	CellRunning    CellStatus = "running"
	CellCompleted  CellStatus = "completed"
	CellTerminated CellStatus = "terminated"
	CellFailed     CellStatus = "failed"
)

type Cell struct {
	ID        string
	Source    string
	Output    string
	Status    CellStatus
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CellStore struct {
	mu    sync.Mutex
	cells map[string]*Cell
	now   func() time.Time
}

func NewCellStore() *CellStore {
	return &CellStore{cells: map[string]*Cell{}, now: time.Now}
}

func (s *CellStore) SetClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if clock == nil {
		s.now = time.Now
		return
	}
	s.now = clock
}

func (s *CellStore) Start(id string, source string) (*Cell, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("cell id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	cell := &Cell{ID: id, Source: source, Status: CellRunning, CreatedAt: now, UpdatedAt: now}
	s.cells[id] = cell
	return cloneCell(cell), nil
}

func (s *CellStore) AppendOutput(id string, output string) (*Cell, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cell := s.cells[id]
	if cell == nil {
		return nil, fmt.Errorf("%w: %s", ErrCellNotFound, id)
	}
	if cell.Status != CellRunning {
		return nil, fmt.Errorf("%w: %s", ErrCellComplete, id)
	}
	cell.Output += output
	cell.UpdatedAt = s.now().UTC()
	return cloneCell(cell), nil
}

func (s *CellStore) Complete(id string, output string, err error) (*Cell, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cell := s.cells[id]
	if cell == nil {
		return nil, fmt.Errorf("%w: %s", ErrCellNotFound, id)
	}
	if output != "" {
		cell.Output += output
	}
	cell.UpdatedAt = s.now().UTC()
	if err != nil {
		cell.Status = CellFailed
		cell.Error = err.Error()
	} else {
		cell.Status = CellCompleted
	}
	return cloneCell(cell), nil
}

func (s *CellStore) Terminate(id string) (*Cell, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cell := s.cells[id]
	if cell == nil {
		return nil, fmt.Errorf("%w: %s", ErrCellNotFound, id)
	}
	cell.Status = CellTerminated
	cell.UpdatedAt = s.now().UTC()
	return cloneCell(cell), nil
}

func (s *CellStore) Get(id string) (*Cell, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cell := s.cells[id]
	if cell == nil {
		return nil, false
	}
	return cloneCell(cell), true
}

func (s *CellStore) List() []Cell {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Cell, 0, len(s.cells))
	for _, cell := range s.cells {
		out = append(out, *cloneCell(cell))
	}
	sort.SliceStable(out, func(i int, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func cloneCell(cell *Cell) *Cell {
	if cell == nil {
		return nil
	}
	cloned := *cell
	return &cloned
}
