package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"codex_go/jsonschema"
)

// fixturePaths mirrors Rust json_schema_policy_fixtures.rs FIXTURE_PATHS.
var schemaPolicyFixturePaths = []string{
	"tools/tests/fixtures/json_schema_policy/slack.json",
	"tools/tests/fixtures/json_schema_policy/google_calendar.json",
	"tools/tests/fixtures/json_schema_policy/google_drive.json",
	"tools/tests/fixtures/json_schema_policy/notion.json",
	"tools/tests/fixtures/json_schema_policy/microsoft_outlook_email.json",
}

const oversizedNotionSchemaPath = "tools/tests/fixtures/json_schema_policy/oversized_notion_create_page_input_schema.json"

// schemaPolicyFixture mirrors the FixtureFile/FixtureTool Rust shapes.
type schemaPolicyFixture struct {
	Source string             `json:"source"`
	Tools  []schemaPolicyTool `json:"tools"`
}

type schemaPolicyTool struct {
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	InputSchema       map[string]any  `json:"input_schema"`
	ExpectedPreserved []expectedValue `json:"expected_preserved"`
	ExpectedPruned    []string        `json:"expected_pruned"`
	ExpectedDropped   []string        `json:"expected_dropped_fields"`
}

type expectedValue struct {
	Pointer string `json:"pointer"`
	Value   any    `json:"value"`
}

// TestToolJSONSchemaPolicyFixturesMatchRust feeds the committed Rust tool
// input-schema fixtures through Go's jsonschema normalization and asserts the
// exact preserved / pruned / dropped contract pinned by
// codex-rs/tools/tests/json_schema_policy_fixtures.rs.
func TestToolJSONSchemaPolicyFixturesMatchRust(t *testing.T) {
	root := rustSnapshotRoot(t)
	for _, path := range schemaPolicyFixturePaths {
		fixture := loadSchemaPolicyFixture(t, filepath.Join(root, filepath.FromSlash(path)))
		for _, fixtureTool := range fixture.Tools {
			t.Run(fixtureTool.Name, func(t *testing.T) {
				parameters := jsonschema.Normalize(fixtureTool.InputSchema)

				// Responses-tool envelope contract.
				if got := parameters["type"]; got != "object" {
					t.Fatalf("parameters.type = %v, want object", got)
				}
				if _, ok := parameters["properties"].(map[string]any); !ok {
					t.Fatalf("parameters.properties missing or not an object: %#v", parameters["properties"])
				}

				for _, expected := range fixtureTool.ExpectedPreserved {
					got := pointerGet(parameters, expected.Pointer)
					if !reflect.DeepEqual(got, expected.Value) {
						t.Fatalf("pointer %s = %#v, want %#v", expected.Pointer, got, expected.Value)
					}
				}
				for _, pointer := range fixtureTool.ExpectedPruned {
					if got := pointerGet(parameters, pointer); got != nil {
						t.Fatalf("pruned pointer %s still present: %#v", pointer, got)
					}
				}
				for _, pointer := range fixtureTool.ExpectedDropped {
					if got := pointerGet(fixtureTool.InputSchema, pointer); got == nil {
						t.Fatalf("fixture input should contain dropped field %s", pointer)
					}
					if got := pointerGet(parameters, pointer); got != nil {
						t.Fatalf("dropped field %s still present after normalization: %#v", pointer, got)
					}
				}
			})
		}
	}
}

// TestToolJSONSchemaPolicyOversizedCompaction pins Rust's oversized-schema
// compaction behavior (json_schema_policy_oversized_golden_schema_triggers_compaction).
func TestToolJSONSchemaPolicyOversizedCompaction(t *testing.T) {
	root := rustSnapshotRoot(t)
	fixture := loadSchemaPolicyFixture(t, filepath.Join(root, filepath.FromSlash(oversizedNotionSchemaPath)))
	if len(fixture.Tools) != 1 {
		t.Fatalf("oversized fixture tools = %d, want 1", len(fixture.Tools))
	}
	fixtureTool := fixture.Tools[0]
	inputBytes := compactJSONLen(fixtureTool.InputSchema)
	parameters := jsonschema.Normalize(fixtureTool.InputSchema)
	outputBytes := compactJSONLen(parameters)
	if outputBytes >= inputBytes {
		t.Fatalf("compaction did not shrink schema: input %d bytes, output %d bytes", inputBytes, outputBytes)
	}
	absent := []string{"/description", "/properties/parent/description", "/$defs"}
	for _, pointer := range absent {
		if got := pointerGet(parameters, pointer); got != nil {
			t.Fatalf("oversized schema should drop %s, got %#v", pointer, got)
		}
	}
	expected := []struct {
		pointer string
		value   any
	}{
		{"/properties/parent", map[string]any{}},
		{"/properties/children/items", map[string]any{}},
		{"/properties/markdown/type", "string"},
		{"/properties/properties/type", "object"},
	}
	for _, item := range expected {
		if got := pointerGet(parameters, item.pointer); !reflect.DeepEqual(got, item.value) {
			t.Fatalf("pointer %s = %#v, want %#v", item.pointer, got, item.value)
		}
	}
}

func loadSchemaPolicyFixture(t *testing.T, path string) schemaPolicyFixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	var fixture schemaPolicyFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("Unmarshal(%s): %v", path, err)
	}
	return fixture
}

// pointerGet navigates a JSON pointer (RFC 6901) over a decoded JSON value.
func pointerGet(value any, pointer string) any {
	if pointer == "" || pointer == "/" {
		return value
	}
	pointer = strings.TrimPrefix(pointer, "/")
	if pointer == "" {
		return value
	}
	tokens := strings.Split(pointer, "/")
	current := value
	for _, raw := range tokens {
		token := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[token]
			if !ok {
				return nil
			}
			current = next
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(node) {
				return nil
			}
			current = node[index]
		default:
			return nil
		}
	}
	return current
}

// compactJSONLen returns the compact JSON byte length of a value.
func compactJSONLen(value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return len(data)
}
