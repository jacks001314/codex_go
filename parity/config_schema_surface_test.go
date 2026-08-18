package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"codex_go/config"
)

// configSchemaRustOnlyAllowlist lists Rust core/config.schema.json top-level
// properties that Go intentionally does not recognize. Each entry carries the
// reason so the list is auditable and can shrink as Go implements more keys.
var configSchemaRustOnlyAllowlist = map[string]string{
	"experimental_compact_prompt_file":    "Rust experimental compaction feature; not implemented in Go",
	"experimental_thread_store":           "Rust experimental thread store; Go uses its own session store",
}

// configSchemaGoOnlyAllowlist lists Go-recognized keys absent from Rust's
// config.schema.json: legacy or Go-specific extensions.
var configSchemaGoOnlyAllowlist = map[string]string{
	"js_repl_node_module_dirs":     "Rust deprecated ignored field (schemars skip); Go recognizes it so strict config does not reject legacy config",
	"js_repl_node_path":            "Rust deprecated ignored field (schemars skip); Go recognizes it so strict config does not reject legacy config",
	"notices":                      "Go extension for notice suppression (Rust uses `notice`); kept for Go compatibility",
	"requirements":                 "Go extension matching legacy config requirements sections",
	"responsesapi_client_metadata": "Go legacy alias of Rust responses_api_metadata; kept for backward compatibility",
	"resume_cwd":                   "Go extension for resume working-directory policy",
	"trusted_projects":             "Go extension for trusted project roots",
	"windows_sandbox":              "Go extension for Windows sandbox configuration",
}

// TestRustConfigSchemaSurfaceAgainstGo diffs Go's recognized top-level config
// keys against Rust's core/config.schema.json properties. Every Rust property
// must be recognized or explicitly allowlisted with a reason, so schema drift
// is reported instead of silently ignored.
func TestRustConfigSchemaSurfaceAgainstGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	schemaPath := filepath.Join(root, "core", "config.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", schemaPath, err)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("Unmarshal(config.schema.json): %v", err)
	}
	rustKeys := make([]string, 0, len(schema.Properties))
	for key := range schema.Properties {
		rustKeys = append(rustKeys, key)
	}
	sort.Strings(rustKeys)

	goKeys := config.KnownTopLevelConfigFields()
	sort.Strings(goKeys)

	if len(rustKeys) != 93 {
		t.Fatalf("Rust config.schema.json top-level property count = %d, want 93 (pinned baseline)", len(rustKeys))
	}
	if len(goKeys) != 99 {
		t.Fatalf("Go recognized top-level config key count = %d, want 99 (pinned baseline)", len(goKeys))
	}

	rustSet := stringSet(rustKeys)
	goSet := stringSet(goKeys)
	missing := sortedDiff(rustKeys, rustSet, goSet) // Rust-only, not in Go
	for _, key := range missing {
		if reason, ok := configSchemaRustOnlyAllowlist[key]; ok {
			t.Logf("allowlisted Rust-only key %q: %s", key, reason)
			continue
		}
		t.Errorf("Rust config.schema.json property %q has no Go recognized key and is not allowlisted", key)
	}
	unexpected := sortedDiff(goKeys, goSet, rustSet) // Go-only, not in Rust schema
	for _, key := range unexpected {
		if reason, ok := configSchemaGoOnlyAllowlist[key]; ok {
			t.Logf("allowlisted Go-only key %q: %s", key, reason)
			continue
		}
		t.Errorf("Go recognized key %q is absent from Rust config.schema.json and is not allowlisted", key)
	}

	for key := range configSchemaRustOnlyAllowlist {
		if !rustSet[key] {
			t.Errorf("allowlisted Rust-only key %q is no longer in config.schema.json; remove the allowlist entry", key)
		}
		if goSet[key] {
			t.Errorf("allowlisted Rust-only key %q is now recognized by Go; remove the allowlist entry", key)
		}
	}
	for key := range configSchemaGoOnlyAllowlist {
		if !goSet[key] {
			t.Errorf("allowlisted Go-only key %q is no longer recognized by Go; remove the allowlist entry", key)
		}
		if rustSet[key] {
			t.Errorf("allowlisted Go-only key %q now appears in Rust config.schema.json; remove the allowlist entry", key)
		}
	}
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func sortedDiff(sorted []string, self, other map[string]bool) []string {
	var out []string
	for _, key := range sorted {
		if self[key] && !other[key] {
			out = append(out, key)
		}
	}
	return out
}

func TestConfigKnownTopLevelFieldsAreUnique(t *testing.T) {
	keys := config.KnownTopLevelConfigFields()
	if len(keys) != len(stringSet(keys)) {
		t.Fatalf("KnownTopLevelConfigFields contains duplicates: %v", keys)
	}
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			t.Fatalf("KnownTopLevelConfigFields contains an empty key")
		}
	}
}
