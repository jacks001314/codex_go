package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ToolSearchName                      = "tool_search"
	DefaultToolSearchLimit              = 8
	maxToolSearchSourceDescriptionBytes = 4 * 1024
)

type ToolSearchArgs struct {
	Query string `json:"query"`
	Limit *int   `json:"limit,omitempty"`
}

type ToolSearchResult struct {
	Tools []Spec `json:"tools"`
}

type ToolSearchHandler struct {
	specs       []Spec
	index       []toolSearchDocument
	omitSources bool
}

func NewToolSearchHandler(specs []Spec) *ToolSearchHandler {
	return NewToolSearchHandlerWithOptions(specs, false)
}

func NewToolSearchHandlerWithOptions(specs []Spec, omitSources bool) *ToolSearchHandler {
	searchable := make([]Spec, 0, len(specs))
	for _, spec := range specs {
		if !IsDeferred(spec.Exposure) {
			continue
		}
		searchable = append(searchable, cloneSpec(spec))
	}
	sortSpecs(searchable)
	handler := &ToolSearchHandler{specs: searchable, omitSources: omitSources}
	handler.rebuildIndex()
	return handler
}

func RegisterToolSearchHandler(registry *Registry, specs []Spec) error {
	return RegisterToolSearchHandlerWithOptions(registry, specs, false)
}

func RegisterToolSearchHandlerWithOptions(registry *Registry, specs []Spec, omitSources bool) error {
	if registry == nil {
		return fmt.Errorf("%w: registry is nil", ErrToolInvalidCall)
	}
	return registry.Register(NewToolSearchHandlerWithOptions(specs, omitSources))
}

func RegisterToolSearchFromRegistry(registry *Registry) error {
	return RegisterToolSearchFromRegistryWithOptions(registry, false)
}

func RegisterToolSearchFromRegistryWithOptions(registry *Registry, omitSources bool) error {
	if registry == nil {
		return fmt.Errorf("%w: registry is nil", ErrToolInvalidCall)
	}
	return RegisterToolSearchHandlerWithOptions(registry, registry.DiscoverableSpecs(), omitSources)
}

func (h *ToolSearchHandler) Spec() Spec {
	return Spec{
		Name:        PlainName(ToolSearchName),
		Description: BuildToolSearchDescriptionWithOptions(searchSources(h.specs), DefaultToolSearchLimit, h.omitSources),
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"query"},
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query for deferred tools.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"description": fmt.Sprintf("Maximum number of tools to return. Defaults to %d.", DefaultToolSearchLimit),
				},
			},
			"additionalProperties": false,
		},
		Parallel: true,
	}
}

func (h *ToolSearchHandler) Execute(ctx context.Context, invocation *Invocation) (*Output, error) {
	_ = ctx
	var args ToolSearchArgs
	if invocation == nil {
		return nil, fmt.Errorf("%w: invocation is nil", ErrToolInvalidCall)
	}
	switch invocation.Payload.Kind {
	case PayloadToolSearch:
		args.Query = firstToolSearchQuery(invocation.Payload.Search)
		if limit, ok := numberFromAny(invocation.Payload.Search["limit"]); ok {
			args.Limit = &limit
		}
	case PayloadFunction:
		if err := invocation.DecodeArguments(&args); err != nil {
			return nil, err
		}
	default:
		return nil, Fatal(ToolSearchName + " handler received unsupported payload")
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return nil, RespondToModel("query must not be empty")
	}
	limit := DefaultToolSearchLimit
	if args.Limit != nil {
		limit = *args.Limit
	}
	if limit <= 0 {
		return nil, RespondToModel("limit must be greater than zero")
	}
	results := h.search(args.Query, limit)
	body, err := json.Marshal(ToolSearchResult{Tools: results})
	if err != nil {
		return nil, err
	}
	return &Output{
		Success: true,
		Body:    string(body),
		Data: map[string]any{
			"tools": specsAsAny(results),
		},
	}, nil
}

func BuildToolSearchDescription(sources []SearchSourceInfo, defaultLimit int) string {
	return BuildToolSearchDescriptionWithOptions(sources, defaultLimit, false)
}

func BuildToolSearchDescriptionWithOptions(sources []SearchSourceInfo, defaultLimit int, omitSources bool) string {
	if omitSources {
		return fmt.Sprintf("# Tool discovery\n\nSearches over deferred tool metadata with BM25 and exposes matching tools for the next model call.\n\nSome of the tools may not have been provided to you upfront, and you should use this tool (`%s`) to search for the required tools. For MCP tool discovery, always use `%s` instead of `list_mcp_resources` or `list_mcp_resource_templates`.", ToolSearchName, ToolSearchName)
	}
	sourceDescriptions := renderToolSearchSources(sources)
	return fmt.Sprintf("# Tool discovery\n\nSearches over deferred tool metadata with BM25 and exposes matching tools for the next model call.\n\nYou have access to tools from the following sources:\n%s\nSome of the tools may not have been provided to you upfront, and you should use this tool (`%s`) to search for the required tools. For MCP tool discovery, always use `%s` instead of `list_mcp_resources` or `list_mcp_resource_templates`.", sourceDescriptions, ToolSearchName, ToolSearchName)
}

func (h *ToolSearchHandler) search(query string, limit int) []Spec {
	if h == nil || limit <= 0 {
		return nil
	}
	queryTokens := tokenizeSearchText(query)
	if len(queryTokens) == 0 {
		return nil
	}
	type scored struct {
		index int
		score int
	}
	var scoredDocs []scored
	for _, document := range h.index {
		score := document.score(queryTokens)
		if score <= 0 {
			continue
		}
		scoredDocs = append(scoredDocs, scored{index: document.index, score: score})
	}
	sort.SliceStable(scoredDocs, func(i, j int) bool {
		if scoredDocs[i].score == scoredDocs[j].score {
			return h.specs[scoredDocs[i].index].Name.Key() < h.specs[scoredDocs[j].index].Name.Key()
		}
		return scoredDocs[i].score > scoredDocs[j].score
	})
	if len(scoredDocs) > limit {
		scoredDocs = scoredDocs[:limit]
	}
	out := make([]Spec, 0, len(scoredDocs))
	for _, scored := range scoredDocs {
		out = append(out, cloneSpecForSearchOutput(h.specs[scored.index]))
	}
	return out
}

func (h *ToolSearchHandler) rebuildIndex() {
	if h == nil {
		return
	}
	h.index = make([]toolSearchDocument, 0, len(h.specs))
	for i, spec := range h.specs {
		text := searchTextForSpec(spec)
		h.index = append(h.index, newToolSearchDocument(i, text))
	}
}

type toolSearchDocument struct {
	index  int
	counts map[string]int
}

func newToolSearchDocument(index int, text string) toolSearchDocument {
	counts := map[string]int{}
	for _, token := range tokenizeSearchText(text) {
		counts[token]++
	}
	return toolSearchDocument{index: index, counts: counts}
}

func (d *toolSearchDocument) score(queryTokens []string) int {
	if d == nil || len(d.counts) == 0 {
		return 0
	}
	score := 0
	covered := 0
	for _, token := range queryTokens {
		count := d.counts[token]
		if count == 0 {
			continue
		}
		covered++
		score += 10 + count
	}
	if covered == len(queryTokens) {
		score += 20
	}
	return score
}

func searchTextForSpec(spec Spec) string {
	if spec.Search != nil && strings.TrimSpace(spec.Search.Text) != "" {
		return spec.Search.Text
	}
	parts := []string{spec.Name.Key(), spec.Name.Namespace, spec.Name.Name, spec.Description}
	if spec.Freeform != nil {
		parts = append(parts, spec.Freeform.Syntax, spec.Freeform.Definition)
	}
	if spec.InputSchema != nil {
		if encoded, err := json.Marshal(spec.InputSchema); err == nil {
			parts = append(parts, string(encoded))
		}
	}
	return strings.Join(parts, " ")
}

func tokenizeSearchText(text string) []string {
	text = strings.ToLower(text)
	var tokens []string
	var builder strings.Builder
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		tokens = append(tokens, builder.String())
		builder.Reset()
	}
	for _, ch := range text {
		if unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_' {
			builder.WriteRune(ch)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func searchSources(specs []Spec) []SearchSourceInfo {
	seen := map[string]int{}
	var sources []SearchSourceInfo
	for _, spec := range specs {
		if spec.Search == nil || spec.Search.Source == nil || strings.TrimSpace(spec.Search.Source.Name) == "" {
			continue
		}
		name := spec.Search.Source.Name
		if idx, ok := seen[name]; ok {
			if sources[idx].Description == "" {
				sources[idx].Description = spec.Search.Source.Description
			}
			continue
		}
		seen[name] = len(sources)
		sources = append(sources, *spec.Search.Source)
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	return sources
}

func renderToolSearchSources(sources []SearchSourceInfo) string {
	if len(sources) == 0 {
		return "None currently enabled."
	}
	sources = dedupeToolSearchSources(sources)
	reservedNameBytes := len(sources) - 1
	for _, source := range sources {
		reservedNameBytes += 2 + len(source.Name)
	}
	descriptionBudget := maxToolSearchSourceDescriptionBytes - reservedNameBytes
	if descriptionBudget < 0 {
		descriptionBudget = 0
	}
	var rendered strings.Builder
	for _, source := range sources {
		separatorBytes := 0
		if rendered.Len() > 0 {
			separatorBytes = 1
		}
		required := separatorBytes + 2 + len(source.Name)
		if required > maxToolSearchSourceDescriptionBytes-rendered.Len() {
			continue
		}
		if rendered.Len() > 0 {
			rendered.WriteByte('\n')
		}
		rendered.WriteString("- ")
		rendered.WriteString(source.Name)
		if strings.TrimSpace(source.Description) != "" && descriptionBudget >= 2 {
			rendered.WriteString(": ")
			descriptionBudget -= 2
			bounded := utf8PrefixByBytes(source.Description, descriptionBudget)
			rendered.WriteString(bounded)
			descriptionBudget -= len(bounded)
		}
	}
	return rendered.String()
}

func utf8PrefixByBytes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func dedupeToolSearchSources(sources []SearchSourceInfo) []SearchSourceInfo {
	seen := map[string]int{}
	out := make([]SearchSourceInfo, 0, len(sources))
	for _, source := range sources {
		if strings.TrimSpace(source.Name) == "" {
			continue
		}
		if idx, ok := seen[source.Name]; ok {
			if out[idx].Description == "" {
				out[idx].Description = source.Description
			}
			continue
		}
		seen[source.Name] = len(out)
		out = append(out, source)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func firstToolSearchQuery(values map[string]any) string {
	for _, key := range []string{"query", "q"} {
		if value, ok := values[key].(string); ok {
			return value
		}
	}
	return ""
}

func numberFromAny(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	default:
		return 0, false
	}
}

func cloneSpecForSearchOutput(spec Spec) Spec {
	out := cloneSpec(spec)
	out.Exposure = ""
	out.Search = nil
	return out
}

func cloneSpec(spec Spec) Spec {
	out := spec
	if spec.InputSchema != nil {
		out.InputSchema = cloneMapAny(spec.InputSchema)
	}
	if spec.Freeform != nil {
		freeform := *spec.Freeform
		out.Freeform = &freeform
	}
	if spec.Search != nil {
		search := *spec.Search
		if spec.Search.Source != nil {
			source := *spec.Search.Source
			search.Source = &source
		}
		out.Search = &search
	}
	return out
}

func specsAsAny(specs []Spec) []any {
	out := make([]any, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec)
	}
	return out
}

func cloneMapAny(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (r *Registry) NamesAsSpecs() []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Spec, 0, len(r.order))
	for _, key := range r.order {
		out = append(out, cloneSpec(r.specs[key]))
	}
	return out
}

var _ Executor = (*ToolSearchHandler)(nil)
