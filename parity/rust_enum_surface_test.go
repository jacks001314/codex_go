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
// wires the emitting paths. The verifier fails if Rust adds codes or Go starts
// emitting an allowlisted value without removing the entry.
var rustErrorCodeEmissionGaps = map[string]string{
	"sessionBudgetExceeded": "Go rollout-budget handling does not yet surface the sessionBudgetExceeded codexErrorInfo when the session budget is exhausted",
	"threadRollbackFailed":  "Go thread/rollback failure paths return generic RPC errors instead of the threadRollbackFailed codexErrorInfo",
}

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
