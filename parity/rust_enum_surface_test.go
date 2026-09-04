package parity

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"

	"codex_go/execserver"
	"codex_go/recordreplay"
)

// TestRustProcessSandboxTypeSurfaceAgainstGo is the L0 enum-inventory check
// for sandbox types: the exec-server protocol ProcessSandboxType serializes
// camelCase (macosSeatbelt / linuxSeccomp / windowsRestrictedToken), and Go's
// wire values must match exactly.
func TestRustProcessSandboxTypeSurfaceAgainstGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	source := string(mustReadParityFile(t, filepath.Join(root, "exec-server-protocol", "src", "protocol.rs")))
	rustWire := parseCamelCaseEnum(t, source, "ProcessSandboxType")
	goWire := []string{
		string(execserver.ProcessSandboxNone),
		string(execserver.ProcessSandboxMacosSeatbelt),
		string(execserver.ProcessSandboxLinuxSeccomp),
		string(execserver.ProcessSandboxWindowsRestrictedToken),
	}
	sort.Strings(rustWire)
	sort.Strings(goWire)
	if !reflect.DeepEqual(rustWire, goWire) {
		t.Fatalf("ProcessSandboxType wire drift\nRust: %v\nGo:   %v", rustWire, goWire)
	}
}

// historicalEventRenames documents recording event names that were renamed in
// the app-server protocol EventMsg enum after the recording was captured
// (oss-story.jsonl, 2025-08-10). The verifier accepts these names only while
// they map to a current wire name, so any further rename breaks the check.
var historicalEventRenames = map[string]string{
	"agent_message_delta":               "agent_message_content_delta",
	"agent_reasoning_raw_content_delta": "reasoning_raw_content_delta",
}

// TestRustEventMsgWireNamesCoverRecordedSurface is the L0 enum-inventory check
// for event names: every codex_event msg.type in the Rust-recorded TUI trace
// must be a valid wire name of the app-server protocol EventMsg enum, or a
// documented historical rename that still maps to a current wire name, so the
// recording's event surface stays pinned to Rust's canonical event names.
func TestRustEventMsgWireNamesCoverRecordedSurface(t *testing.T) {
	root := rustSnapshotRoot(t)
	source := string(mustReadParityFile(t, filepath.Join(root, "protocol", "src", "protocol.rs")))
	rustWire := parseSnakeCaseEnumWithRenames(t, source, "EventMsg")
	wireSet := map[string]bool{}
	for _, name := range rustWire {
		wireSet[name] = true
	}
	for _, current := range historicalEventRenames {
		if !wireSet[current] {
			t.Fatalf("historical rename target %q is not a current EventMsg wire name", current)
		}
	}
	for _, alias := range []string{"turn_started", "turn_complete"} {
		wireSet[alias] = true // v2 interop aliases accepted by the wire format
	}

	recording, err := recordreplay.DefaultStoryRecordingPath()
	if err != nil {
		t.Fatalf("recording: %v", err)
	}
	events, err := recordreplay.Parse(recording)
	if err != nil {
		t.Fatalf("Parse(%s): %v", recording, err)
	}
	seen := map[string]bool{}
	for _, ev := range events {
		if ev.Kind != "codex_event" || ev.MsgType == "" || seen[ev.MsgType] {
			continue
		}
		seen[ev.MsgType] = true
		if !wireSet[ev.MsgType] {
			if current, ok := historicalEventRenames[ev.MsgType]; ok && wireSet[current] {
				continue
			}
			t.Errorf("recording msg.type %q is neither a Rust EventMsg wire name nor a documented historical rename", ev.MsgType)
		}
	}
	if len(seen) == 0 {
		t.Fatalf("no codex_event msg types found in the recording")
	}
}

// rustErrorCodeEmissionGaps documents Rust error-code wire values that Go does
// not emit yet, with the reason so the gap stays auditable and shrinks as Go
// wires the emitting paths. Currently empty: every CodexErrorInfo and
// ConfigWriteErrorCode wire value is present in Go production code.
var rustErrorCodeEmissionGaps = map[string]string{}

// TestRustErrorCodeSurfaceAgainstGo is the L0 enum-inventory check for error
// codes: every wire value of the app-server v2 CodexErrorInfo and
// ConfigWriteErrorCode enums must be present in the Go tree (as an emitted
// string or a constant) or be a documented emission gap. ConfigWriteErrorCode
// values are covered by config/api.go constants; CodexErrorInfo values by the
// appserver's emitted strings.
func TestRustErrorCodeSurfaceAgainstGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	protocolDir := filepath.Join(root, "app-server-protocol", "src", "protocol", "v2")
	shared := string(mustReadParityFile(t, filepath.Join(protocolDir, "shared.rs")))
	config := string(mustReadParityFile(t, filepath.Join(protocolDir, "config.rs")))
	rustWire := append(
		parseCamelCaseEnum(t, shared, "CodexErrorInfo"),
		parseCamelCaseEnum(t, config, "ConfigWriteErrorCode")...,
	)
	wireSet := map[string]bool{}
	for _, name := range rustWire {
		wireSet[name] = true
	}

	present := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(".."), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for code := range wireSet {
			if strings.Contains(string(data), `"`+code+`"`) {
				present[code] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}

	for code := range wireSet {
		if present[code] {
			continue
		}
		if reason, ok := rustErrorCodeEmissionGaps[code]; ok {
			t.Logf("documented emission gap %q: %s", code, reason)
			continue
		}
		t.Errorf("Rust error code %q is neither present in the Go tree nor a documented emission gap", code)
	}
	for code := range rustErrorCodeEmissionGaps {
		if !wireSet[code] {
			t.Errorf("documented emission gap %q is no longer a Rust error code; remove the entry", code)
		}
		if present[code] {
			t.Errorf("documented emission gap %q is now present in the Go tree; remove the entry", code)
		}
	}
}

// modelInfoRustOnlyAllowlist documents Rust ModelInfo wire fields that Go's
// ModelInfo does not carry, with the reason so each entry is auditable and
// removable as Go adopts the field.
// modelInfoRustOnlyAllowlist documents Rust ModelInfo wire fields that Go's
// ModelInfo does not carry. Currently empty: all wire fields are carried.
var modelInfoRustOnlyAllowlist = map[string]string{}

// modelInfoGoOnlyAllowlist documents Go ModelInfo JSON fields absent from
// Rust's ModelInfo: extensions or naming variants.
var modelInfoGoOnlyAllowlist = map[string]string{
	"base_instructions":            "Go extension carrying the model base instructions template",
	"supports_parallel_tool_calls": "Go extension for parallel tool call support",
}

// TestRustModelInfoFieldSurfaceAgainstGo is the L0 enum-inventory check for
// model metadata: the wire fields of Rust's openai_models::ModelInfo (skipping
// internal-only fields) must be present in Go's ModelInfo JSON surface or be a
// documented naming variant / gap, so metadata drift is reported instead of
// silently diverging.
func TestRustModelInfoFieldSurfaceAgainstGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	source := string(mustReadParityFile(t, filepath.Join(root, "protocol", "src", "openai_models.rs")))
	rustFields := parseStructFields(t, source, "ModelInfo")
	rustSet := map[string]bool{}
	for _, field := range rustFields {
		if field == "used_fallback_model_metadata" {
			continue // #[serde(skip_serializing, skip_deserializing)] internal marker
		}
		rustSet[field] = true
	}

	goSource := string(mustReadParityFile(t, filepath.Join("..", "model", "catalog.go")))
	goFields := parseJSONStructFields(t, goSource, "ModelInfo")
	goSet := map[string]bool{}
	for _, field := range goFields {
		if field == "-" {
			continue
		}
		goSet[field] = true
	}

	if len(rustSet) != 43 {
		t.Fatalf("Rust ModelInfo wire field count = %d, want 43 (pinned baseline)", len(rustSet))
	}
	if len(goSet) != 45 {
		t.Fatalf("Go ModelInfo JSON field count = %d, want 45 (pinned baseline)", len(goSet))
	}

	for field := range rustSet {
		if goSet[field] {
			continue
		}
		if reason, ok := modelInfoRustOnlyAllowlist[field]; ok {
			t.Logf("allowlisted Rust-only ModelInfo field %q: %s", field, reason)
			continue
		}
		t.Errorf("Rust ModelInfo field %q is not in Go's ModelInfo and not allowlisted", field)
	}
	for field := range goSet {
		if rustSet[field] {
			continue
		}
		if reason, ok := modelInfoGoOnlyAllowlist[field]; ok {
			t.Logf("allowlisted Go-only ModelInfo field %q: %s", field, reason)
			continue
		}
		t.Errorf("Go ModelInfo field %q is absent from Rust's ModelInfo and not allowlisted", field)
	}

	for field := range modelInfoRustOnlyAllowlist {
		if !rustSet[field] {
			t.Errorf("allowlisted Rust-only ModelInfo field %q is no longer in Rust's ModelInfo; remove the entry", field)
		}
		if goSet[field] {
			t.Errorf("allowlisted Rust-only ModelInfo field %q is now in Go's ModelInfo; remove the entry", field)
		}
	}
	for field := range modelInfoGoOnlyAllowlist {
		if !goSet[field] {
			t.Errorf("allowlisted Go-only ModelInfo field %q is no longer in Go's ModelInfo; remove the entry", field)
		}
		if rustSet[field] {
			t.Errorf("allowlisted Go-only ModelInfo field %q now appears in Rust's ModelInfo; remove the entry", field)
		}
	}
}

// parseCamelCaseEnum extracts a #[serde(rename_all = "camelCase")] enum's
// serialized variant names from Rust source.
func parseCamelCaseEnum(t *testing.T, source, enumName string) []string {
	t.Helper()
	body := extractEnumBody(t, source, enumName)
	var out []string
	for _, variant := range extractEnumVariants(body) {
		runes := []rune(variant)
		runes[0] = unicode.ToLower(runes[0])
		out = append(out, string(runes))
	}
	return out
}

// parseSnakeCaseEnumWithRenames extracts a #[serde(rename_all = "snake_case")]
// enum's wire names, honoring explicit per-variant #[serde(rename = ...)].
func parseSnakeCaseEnumWithRenames(t *testing.T, source, enumName string) []string {
	t.Helper()
	body := extractEnumBody(t, source, enumName)
	renamePattern := regexp.MustCompile(`#\[serde\(rename = "([^"]+)"`)
	renames := map[string]string{}
	segments := regexp.MustCompile(`(#\[serde\([^\]]*\)\]\s*\n\s*[A-Z][A-Za-z0-9]*|\b[A-Z][A-Za-z0-9]*)`)
	for _, seg := range segments.FindAllString(body, -1) {
		parts := strings.Split(seg, "\n")
		variant := strings.TrimSpace(parts[len(parts)-1])
		if renameMatch := renamePattern.FindStringSubmatch(seg); len(renameMatch) == 2 && variant != "" {
			renames[variant] = renameMatch[1]
		}
	}
	var out []string
	for _, variant := range extractEnumVariants(body) {
		if wire, ok := renames[variant]; ok {
			out = append(out, wire)
			continue
		}
		out = append(out, snakeCase(variant))
	}
	return out
}

func extractEnumBody(t *testing.T, source, enumName string) string {
	t.Helper()
	pattern := regexp.MustCompile(`(?s)pub enum ` + regexp.QuoteMeta(enumName) + ` \{(.*?)\n\}`)
	match := pattern.FindStringSubmatch(source)
	if len(match) != 2 {
		t.Fatalf("could not find enum %s in source", enumName)
	}
	return match[1]
}

func extractEnumVariants(body string) []string {
	variantPattern := regexp.MustCompile(`(?m)^\s*([A-Z][A-Za-z0-9]*)(?:\([^)]*\))?\s*,`)
	var out []string
	for _, match := range variantPattern.FindAllStringSubmatch(body, -1) {
		out = append(out, match[1])
	}
	return out
}

// parseStructFields extracts the `pub <field>:` names of a Rust struct.
func parseStructFields(t *testing.T, source, structName string) []string {
	t.Helper()
	pattern := regexp.MustCompile(`(?s)pub struct ` + regexp.QuoteMeta(structName) + ` \{(.*?)\n\}`)
	match := pattern.FindStringSubmatch(source)
	if len(match) != 2 {
		t.Fatalf("could not find struct %s in source", structName)
	}
	body := match[1]
	var out []string
	for _, match := range regexp.MustCompile(`(?m)^\s+pub ([a-z_0-9]+):`).FindAllStringSubmatch(body, -1) {
		out = append(out, match[1])
	}
	return out
}

// parseJSONStructFields extracts the json tag names of a Go struct.
func parseJSONStructFields(t *testing.T, source, structName string) []string {
	t.Helper()
	pattern := regexp.MustCompile(`(?s)type ` + regexp.QuoteMeta(structName) + ` struct \{(.*?)\n\}`)
	match := pattern.FindStringSubmatch(source)
	if len(match) != 2 {
		t.Fatalf("could not find struct %s in source", structName)
	}
	var out []string
	for _, m := range regexp.MustCompile(`json:"([^"]+)"`).FindAllStringSubmatch(match[1], -1) {
		name := strings.Split(m[1], ",")[0]
		out = append(out, name)
	}
	return out
}

func snakeCase(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
