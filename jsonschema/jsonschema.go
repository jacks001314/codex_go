// Package jsonschema mirrors the Rust Codex tool-input-schema policy
// (codex-rs/tools/src/json_schema.rs): sanitize badly-formed JSON Schema for
// the Responses API, prune unreachable definition tables, drop fields outside
// the supported subset, and compact oversized schemas.
//
// The normalization is the single source of truth for the model-visible tool
// parameters of imported (MCP / dynamic / agent-plugin) tools; the Rust
// fixtures under tools/tests/fixtures/json_schema_policy pin its behavior
// (see parity/tool_schema_policy_test.go).
package jsonschema

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

var definitionTableKeys = []string{"$defs", "definitions"}

// schemaChildKeys are the composition/array keywords that carry nested
// schemas during traversal (mirrors Rust SCHEMA_CHILD_KEYS).
var schemaChildKeys = []string{"items", "anyOf", "oneOf", "allOf"}

var compositionSchemaKeys = []string{"anyOf", "oneOf", "allOf"}

// jsonSchemaSubsetKeys is the serde field surface of Rust's JsonSchema struct:
// every other key (pattern, format, title, default, minimum, ...) is dropped
// when a tool schema is deserialized into the subset.
var jsonSchemaSubsetKeys = map[string]bool{
	"$ref":                 true,
	"type":                 true,
	"description":          true,
	"encrypted":            true,
	"enum":                 true,
	"items":                true,
	"properties":           true,
	"required":             true,
	"additionalProperties": true,
	"anyOf":                true,
	"oneOf":                true,
	"allOf":                true,
	"$defs":                true,
	"definitions":          true,
}

const (
	maxCompactToolSchemaBytes = 5_000
	maxCompactToolSchemaDepth = 3
)

// Normalize mirrors Rust parse_tool_input_schema: sanitize, prune unreachable
// definitions, compact oversized schemas, then drop non-subset fields. The
// input is not mutated.
func Normalize(input map[string]any) map[string]any {
	value := cloneJSON(input)
	if obj, ok := value.(map[string]any); ok {
		sanitizeJSONSchema(obj)
		pruneUnreachableDefinitions(obj)
		compactLargeToolSchema(obj)
		return subsetFilter(obj).(map[string]any)
	}
	return subsetFilter(value).(map[string]any)
}

// sanitizeJSONSchema mirrors Rust sanitize_json_schema: coerce boolean
// schemas, recurse into schema children and definition tables, rewrite const
// to enum, infer missing types, and default object/array children. Maps are
// mutated in place; other values are returned as their replacement.
func sanitizeJSONSchema(value any) any {
	switch v := value.(type) {
	case bool:
		// JSON Schema boolean form: true/false. Coerce to an accept-all string
		// because the baseline enum model cannot represent boolean schemas.
		return map[string]any{"type": "string"}
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sanitizeJSONSchema(item)
		}
		return out
	case map[string]any:
		sanitizeSchemaMap(v)
		return v
	default:
		return value
	}
}

func sanitizeSchemaMap(m map[string]any) {
	if props, ok := m["properties"].(map[string]any); ok {
		for key, value := range props {
			props[key] = sanitizeJSONSchema(value)
		}
	}
	if items, ok := m["items"]; ok {
		m["items"] = sanitizeJSONSchema(items)
	}
	if additional, ok := m["additionalProperties"]; ok {
		if _, isBool := additional.(bool); !isBool {
			m["additionalProperties"] = sanitizeJSONSchema(additional)
		}
	}
	if prefix, ok := m["prefixItems"]; ok {
		m["prefixItems"] = sanitizeJSONSchema(prefix)
	}
	for _, key := range compositionSchemaKeys {
		if value, ok := m[key]; ok {
			m[key] = sanitizeJSONSchema(value)
		}
	}
	for _, table := range definitionTableKeys {
		sanitizeSchemaTable(m, table)
	}

	if constValue, ok := m["const"]; ok {
		delete(m, "const")
		m["enum"] = []any{constValue}
	}

	schemaTypes := normalizedSchemaTypes(m)
	if len(schemaTypes) == 0 {
		if hasRefOrComposition(m) {
			return
		}
		switch {
		case hasAny(m, "properties", "required", "additionalProperties"):
			schemaTypes = []string{"object"}
		case hasAny(m, "items", "prefixItems"):
			schemaTypes = []string{"array"}
		case hasAny(m, "enum", "format"):
			schemaTypes = []string{"string"}
		case hasAny(m, "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"):
			schemaTypes = []string{"number"}
		default:
			clear(m)
			return
		}
	}
	writeSchemaTypes(m, schemaTypes)
	ensureDefaultChildrenForSchemaTypes(m, schemaTypes)
}

func sanitizeSchemaTable(m map[string]any, key string) {
	definitions, ok := m[key].(map[string]any)
	if !ok {
		if _, exists := m[key]; exists {
			delete(m, key)
		}
		return
	}
	for name, value := range definitions {
		definitions[name] = sanitizeJSONSchema(value)
	}
}

func normalizedSchemaTypes(m map[string]any) []string {
	schemaType, ok := m["type"]
	if !ok {
		return nil
	}
	switch t := schemaType.(type) {
	case string:
		if valid := validPrimitiveType(t); valid {
			return []string{t}
		}
		return nil
	case []any:
		var out []string
		for _, item := range t {
			if s, ok := item.(string); ok && validPrimitiveType(s) {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func validPrimitiveType(name string) bool {
	switch name {
	case "string", "number", "boolean", "integer", "object", "array", "null":
		return true
	default:
		return false
	}
}

func writeSchemaTypes(m map[string]any, types []string) {
	switch len(types) {
	case 0:
		delete(m, "type")
	case 1:
		m["type"] = types[0]
	default:
		values := make([]any, 0, len(types))
		for _, t := range types {
			values = append(values, t)
		}
		m["type"] = values
	}
}

func ensureDefaultChildrenForSchemaTypes(m map[string]any, types []string) {
	if containsString(types, "object") {
		if _, ok := m["properties"]; !ok {
			m["properties"] = map[string]any{}
		}
	}
	if containsString(types, "array") {
		if _, ok := m["items"]; !ok {
			m["items"] = map[string]any{"type": "string"}
		}
	}
}

func hasRefOrComposition(m map[string]any) bool {
	if _, ok := m["$ref"]; ok {
		return true
	}
	for _, key := range compositionSchemaKeys {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}

func hasAny(m map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// definitionPointer identifies one entry in a $defs/definitions table
// (mirrors Rust DefinitionPointer).
type definitionPointer struct {
	table string
	name  string
}

// pruneUnreachableDefinitions removes definition-table entries that are not
// reachable from the schema root through $ref, following refs transitively.
func pruneUnreachableDefinitions(root map[string]any) {
	reachable := collectReachableDefinitions(root)
	for _, table := range definitionTableKeys {
		definitions, ok := root[table].(map[string]any)
		if !ok {
			continue
		}
		for name := range definitions {
			if !reachable[definitionPointer{table: table, name: name}] {
				delete(definitions, name)
			}
		}
		if len(definitions) == 0 {
			delete(root, table)
		}
	}
}

func collectReachableDefinitions(root map[string]any) map[definitionPointer]bool {
	reachable := map[definitionPointer]bool{}
	var pending []definitionPointer

	collectRefsOutsideDefinitions(root, &pending)
	for len(pending) > 0 {
		pointer := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if reachable[pointer] {
			continue
		}
		reachable[pointer] = true
		if definition := definitionForPointer(root, pointer); definition != nil {
			collectRefs(definition, &pending)
		}
	}
	return reachable
}

// collectRefsOutsideDefinitions mirrors Rust collect_refs_outside_definitions:
// traverse schema children (skipping definition tables) collecting local refs.
func collectRefsOutsideDefinitions(value any, refs *[]definitionPointer) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			collectRefsOutsideDefinitions(item, refs)
		}
	case map[string]any:
		collectRefFromMap(v, refs)
		forEachSchemaChild(v, func(child any) {
			collectRefsOutsideDefinitions(child, refs)
		})
	}
}

// collectRefs mirrors Rust collect_refs: full traversal (including inside
// nested definition tables) used for definitions.
func collectRefs(value any, refs *[]definitionPointer) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			collectRefs(item, refs)
		}
	case map[string]any:
		collectRefFromMap(v, refs)
		for _, child := range v {
			collectRefs(child, refs)
		}
	}
}

func collectRefFromMap(m map[string]any, refs *[]definitionPointer) {
	if ref, ok := m["$ref"].(string); ok {
		if pointer, ok := parseLocalDefinitionRef(ref); ok {
			*refs = append(*refs, pointer)
		}
	}
}

// forEachSchemaChild mirrors Rust for_each_schema_child with
// DefinitionTraversal::Skip: properties values, items, anyOf/oneOf/allOf and
// non-boolean additionalProperties.
func forEachSchemaChild(m map[string]any, visitor func(any)) {
	if props, ok := m["properties"].(map[string]any); ok {
		for _, value := range props {
			visitor(value)
		}
	}
	for _, key := range schemaChildKeys {
		if value, ok := m[key]; ok {
			visitor(value)
		}
	}
	if additional, ok := m["additionalProperties"]; ok {
		if _, isBool := additional.(bool); !isBool {
			visitor(additional)
		}
	}
}

func definitionForPointer(root map[string]any, pointer definitionPointer) any {
	definitions, ok := root[pointer.table].(map[string]any)
	if !ok {
		return nil
	}
	return definitions[pointer.name]
}

// parseLocalDefinitionRef mirrors Rust parse_local_definition_ref: decode the
// fragment, split the JSON pointer, and take the first table token plus the
// next token as the definition name (deeper refs keep the parent reachable).
func parseLocalDefinitionRef(schemaRef string) (definitionPointer, bool) {
	if !strings.HasPrefix(schemaRef, "#") {
		return definitionPointer{}, false
	}
	fragment := strings.TrimPrefix(schemaRef, "#")
	decoded, err := url.PathUnescape(fragment)
	if err != nil {
		return definitionPointer{}, false
	}
	tokens := splitJSONPointer(decoded)
	if len(tokens) < 2 {
		return definitionPointer{}, false
	}
	table := ""
	for _, candidate := range definitionTableKeys {
		if tokens[0] == candidate {
			table = candidate
			break
		}
	}
	if table == "" {
		return definitionPointer{}, false
	}
	return definitionPointer{table: table, name: tokens[1]}, true
}

func splitJSONPointer(pointer string) []string {
	pointer = strings.TrimPrefix(pointer, "/")
	if pointer == "" {
		return nil
	}
	raw := strings.Split(pointer, "/")
	out := make([]string, 0, len(raw))
	for _, token := range raw {
		token = strings.ReplaceAll(token, "~1", "/")
		token = strings.ReplaceAll(token, "~0", "~")
		out = append(out, token)
	}
	return out
}

// subsetFilter keeps only the JsonSchema subset keys, recursively. The result
// is a plain map[string]any ready for JSON serialization.
func subsetFilter(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, subsetFilter(item))
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			if !jsonSchemaSubsetKeys[key] {
				continue
			}
			switch key {
			case "properties", "$defs", "definitions":
				// Name -> schema maps: every name is preserved; only the
				// schema values are subset-filtered.
				if names, ok := item.(map[string]any); ok {
					filtered := make(map[string]any, len(names))
					for name, schema := range names {
						filtered[name] = subsetFilter(schema)
					}
					out[key] = filtered
					continue
				}
				out[key] = subsetFilter(item)
			case "enum":
				// Raw JSON values, not schemas.
				out[key] = item
			default:
				out[key] = subsetFilter(item)
			}
		}
		return out
	default:
		return value
	}
}

// compactLargeToolSchema mirrors Rust compact_large_tool_schema: apply
// increasingly lossy passes while the normalized schema exceeds the budget.
func compactLargeToolSchema(value map[string]any) {
	if compactSchemaFitsBudget(value) {
		return
	}
	passes := []func(map[string]any){
		stripSchemaDescriptions,
		dropSchemaDefinitions,
		collapseDeepSchemaObjectsFromRoot,
		pruneSchemaCompositions,
	}
	for _, pass := range passes {
		if compactSchemaFitsBudget(value) {
			break
		}
		pass(value)
	}
}

func compactSchemaFitsBudget(value map[string]any) bool {
	return compactNormalizedSchemaLen(value) <= maxCompactToolSchemaBytes
}

// compactNormalizedSchemaLen mirrors Rust compact_normalized_schema_len: the
// byte length of the subset-normalized JSON.
func compactNormalizedSchemaLen(value map[string]any) int {
	normalized := subsetFilter(value)
	return approximateJSONLen(normalized)
}

// approximateJSONLen returns the compact JSON byte length without allocation
// churn; it is equivalent to len(json.Marshal(v)) for the values produced by
// subsetFilter.
func approximateJSONLen(value any) int {
	switch v := value.(type) {
	case nil:
		return 4 // null
	case bool:
		if v {
			return 4
		}
		return 5
	case string:
		return 2 + escapedLen(v)
	case float64:
		return len(formatFloat(v))
	case []any:
		total := 2 // []
		for i, item := range v {
			if i > 0 {
				total++
			}
			total += approximateJSONLen(item)
		}
		return total
	case map[string]any:
		total := 2 // {}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for i, key := range keys {
			if i > 0 {
				total++
			}
			total += 2 + escapedLen(key) + 1 // "key":
			total += approximateJSONLen(v[key])
		}
		return total
	default:
		return 0
	}
}

func escapedLen(s string) int {
	total := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\\':
			total += 2
		case '\n':
			total += 2
		case '\r', '\t':
			total += 2
		default:
			total++
		}
	}
	return total
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func stripSchemaDescriptions(value map[string]any) {
	delete(value, "description")
	forEachSchemaChildIncludingDefs(value, func(child any) {
		if m, ok := child.(map[string]any); ok {
			stripSchemaDescriptions(m)
		}
	})
}

func dropSchemaDefinitions(value map[string]any) {
	rewriteDefinitionRefsToEmptySchemas(value)
	for _, table := range definitionTableKeys {
		delete(value, table)
	}
}

func rewriteDefinitionRefsToEmptySchemas(value any) {
	switch v := value.(type) {
	case []any:
		for i := range v {
			rewriteDefinitionRefsToEmptySchemas(v[i])
		}
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok {
			if _, local := parseLocalDefinitionRef(ref); local {
				for key := range v {
					delete(v, key)
				}
				return
			}
		}
		forEachSchemaChild(v, func(child any) {
			rewriteDefinitionRefsToEmptySchemas(child)
		})
	}
}

func collapseDeepSchemaObjectsFromRoot(value map[string]any) {
	collapseDeepSchemaObjects(value, 0)
}

func collapseDeepSchemaObjects(value any, depth int) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			collapseDeepSchemaObjects(item, depth)
		}
	case map[string]any:
		if depth >= maxCompactToolSchemaDepth && isComplexSchemaObject(v) {
			clear(v)
			return
		}
		forEachSchemaChild(v, func(child any) {
			collapseDeepSchemaObjects(child, depth+1)
		})
	}
}

func pruneSchemaCompositions(value map[string]any) {
	pruneSchemaCompositionsValue(value)
}

func pruneSchemaCompositionsValue(value any) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			pruneSchemaCompositionsValue(item)
		}
	case map[string]any:
		if hasCompositionKeyword(v) {
			clear(v)
			return
		}
		forEachSchemaChild(v, func(child any) {
			pruneSchemaCompositionsValue(child)
		})
	}
}

func isComplexSchemaObject(m map[string]any) bool {
	for _, key := range schemaChildKeys {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return hasAny(m, "properties", "additionalProperties", "$ref")
}

func hasCompositionKeyword(m map[string]any) bool {
	for _, key := range compositionSchemaKeys {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}

// forEachSchemaChildIncludingDefs is the DefinitionTraversal::Include variant
// used by strip_schema_descriptions.
func forEachSchemaChildIncludingDefs(m map[string]any, visitor func(any)) {
	forEachSchemaChild(m, visitor)
	for _, table := range definitionTableKeys {
		if definitions, ok := m[table].(map[string]any); ok {
			for _, definition := range definitions {
				visitor(definition)
			}
		}
	}
}

// cloneJSON deep-copies a JSON value without mutating the input.
func cloneJSON(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = cloneJSON(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = cloneJSON(item)
		}
		return out
	default:
		return value
	}
}
