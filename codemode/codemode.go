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
	definitions := CollectDefinitions(specs)
	return &ToolSurface{
		Definitions:            definitions,
		Exec:                   BuildExecTool(definitions, namespaces, codeModeOnly, deferred),
		Wait:                   BuildWaitTool(),
		DeferredToolsAvailable: deferred,
	}
}

func BuildExecToolDescription(definitions []ToolDefinition, namespaces map[string]NamespaceDescription, codeModeOnly bool, deferredToolsAvailable bool) string {
	var builder strings.Builder
	builder.WriteString("Execute JavaScript-like code that may call enabled tools by name.\n")
	if codeModeOnly {
		builder.WriteString("Use this as the primary execution surface for tool orchestration.\n")
	}
	if deferredToolsAvailable {
		builder.WriteString("Deferred tools may be discovered before invocation.\n")
	}
	if len(namespaces) > 0 {
		keys := make([]string, 0, len(namespaces))
		for key := range namespaces {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			description := strings.TrimSpace(namespaces[key].Description)
			if description != "" {
				builder.WriteString("\n")
				builder.WriteString(key)
				builder.WriteString(": ")
				builder.WriteString(description)
				builder.WriteString("\n")
			}
		}
	}
	definitions = SortDefinitions(definitions)
	if len(definitions) > 0 {
		builder.WriteString("\nEnabled tools:\n")
		for _, definition := range definitions {
			if !IsNestedTool(definition.Name) {
				continue
			}
			builder.WriteString("- ")
			builder.WriteString(definition.Name)
			if strings.TrimSpace(definition.Description) != "" {
				builder.WriteString(": ")
				builder.WriteString(firstLine(definition.Description))
			}
			builder.WriteString("\n")
		}
	}
	return strings.TrimSpace(builder.String())
}

func BuildWaitToolDescription() string {
	return "Use it after an exec call yields a cell id. Set terminate to stop a still-running cell."
}

func AugmentToolDefinition(definition ToolDefinition) ToolDefinition {
	if definition.Name == PublicToolName {
		return definition
	}
	sample := RenderSample(definition)
	if sample != "" && !strings.Contains(definition.Description, sample) {
		definition.Description = strings.TrimSpace(definition.Description + "\n\n" + sample)
	}
	return definition
}

func RenderSample(definition ToolDefinition) string {
	if definition.Name == "" {
		return ""
	}
	switch definition.Kind {
	case ToolKindFreeform:
		return fmt.Sprintf("Code mode sample:\nawait %s(`...`);", definition.Name)
	default:
		return fmt.Sprintf("Code mode sample:\nawait %s({});", definition.Name)
	}
}

func CollectDefinitions(specs []tool.Spec) []ToolDefinition {
	definitions := make([]ToolDefinition, 0, len(specs))
	for _, spec := range specs {
		name := spec.Name
		definition := ToolDefinition{
			Name:        NameForToolName(name),
			ToolName:    name,
			Description: spec.Description,
			Kind:        ToolKindFunction,
			InputSchema: spec.InputSchema,
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
			definitions = append(definitions, AugmentToolDefinition(definition))
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

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return value[:index]
	}
	return value
}

func cloneCell(cell *Cell) *Cell {
	if cell == nil {
		return nil
	}
	cloned := *cell
	return &cloned
}
