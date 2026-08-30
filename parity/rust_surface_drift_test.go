package parity

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"codex_go/features"
	"codex_go/plugin"
	"codex_go/turn"
)

func TestRustFeatureKeySurfaceAgainstGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustKeys := rustFeatureKeys(t, filepath.Join(root, "features", "src", "lib.rs"))
	goKeys := make([]string, 0, len(features.Sorted()))
	for _, spec := range features.Sorted() {
		goKeys = append(goKeys, spec.Key)
	}
	sort.Strings(goKeys)

	knownGoOnly := []string{}
	knownRustOnly := []string{}
	assertKnownSurfaceDiff(t, "feature key", rustKeys, goKeys, knownRustOnly, knownGoOnly)
}

func TestRustConfigTomlTopLevelSurfaceSnapshot(t *testing.T) {
	root := rustSnapshotRoot(t)
	fields := rustStructPublicFields(t, filepath.Join(root, "config", "src", "config_toml.rs"), "ConfigToml")
	if len(fields) != 99 {
		t.Fatalf("Rust ConfigToml top-level field count drift: got %d want 99", len(fields))
	}
	for _, required := range []string{
		"agents",
		"features",
		"model_auto_compact_token_limit",
		"model_auto_compact_token_limit_scope",
		"model_provider",
		"model_providers",
		"tools",
	} {
		if !surfaceContainsString(fields, required) {
			t.Fatalf("Rust ConfigToml is missing required tracked field %q", required)
		}
	}
}

func TestRustToolDiscoverySurfaceAgainstGoRegistry(t *testing.T) {
	root := rustSnapshotRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "tools", "src", "tool_discovery.rs"))
	if err != nil {
		t.Fatalf("ReadFile(tool_discovery.rs) error = %v", err)
	}
	re := regexp.MustCompile(`pub const [A-Z0-9_]+_TOOL_NAME: &str = "([^"]+)"`)
	rustNames := extractSortedMatches(string(source), re)

	options := turn.DefaultToolRegistryOptions("")
	options.PluginInstallCandidates = []plugin.DiscoverableInfo{{ID: "parity", Name: "Parity"}}
	registry, err := turn.BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	goNames := make([]string, 0, len(registry.Names()))
	for _, name := range registry.Names() {
		goNames = append(goNames, name.Key())
	}
	sort.Strings(goNames)
	for _, name := range rustNames {
		if !surfaceContainsString(goNames, name) {
			t.Fatalf("Go default tool registry is missing Rust discovery tool %q", name)
		}
	}
}

func rustFeatureKeys(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	start := strings.Index(string(data), "pub const FEATURES:")
	if start < 0 {
		t.Fatalf("could not locate Rust FEATURES table in %s", path)
	}
	re := regexp.MustCompile(`key:\s*"([^"]+)"`)
	return extractSortedMatches(string(data[start:]), re)
}

func rustStructPublicFields(t *testing.T, path string, structName string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	startMarker := "pub struct " + structName + " {"
	inStruct := false
	depth := 0
	fields := []string{}
	fieldRE := regexp.MustCompile(`^\s*pub\s+([A-Za-z0-9_]+)\s*:`)
	for _, line := range lines {
		if !inStruct {
			if strings.TrimSpace(line) != startMarker {
				continue
			}
			inStruct = true
		}
		depth += strings.Count(line, "{")
		depth -= strings.Count(line, "}")
		if depth == 1 {
			if match := fieldRE.FindStringSubmatch(line); len(match) == 2 {
				fields = append(fields, match[1])
			}
		}
		if inStruct && depth == 0 {
			break
		}
	}
	if len(fields) == 0 {
		t.Fatalf("no public fields found for Rust struct %s in %s", structName, path)
	}
	sort.Strings(fields)
	return fields
}

func assertKnownSurfaceDiff(t *testing.T, label string, rustValues []string, goValues []string, knownRustOnly []string, knownGoOnly []string) {
	t.Helper()
	rustOnly := missingStrings(goValues, rustValues)
	goOnly := missingStrings(rustValues, goValues)
	sort.Strings(knownRustOnly)
	sort.Strings(knownGoOnly)
	if strings.Join(rustOnly, "\x00") != strings.Join(knownRustOnly, "\x00") || strings.Join(goOnly, "\x00") != strings.Join(knownGoOnly, "\x00") {
		t.Fatalf("Rust/Go %s drift; rustOnly=%v want=%v goOnly=%v want=%v", label, rustOnly, knownRustOnly, goOnly, knownGoOnly)
	}
}

func extractSortedMatches(source string, re *regexp.Regexp) []string {
	values := []string{}
	for _, match := range re.FindAllStringSubmatch(source, -1) {
		if len(match) == 2 {
			values = append(values, match[1])
		}
	}
	sort.Strings(values)
	return values
}

func surfaceContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
