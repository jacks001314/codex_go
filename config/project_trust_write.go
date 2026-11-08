package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Trust levels Go shares with Rust's TrustLevel enum
// (codex-rs/protocol/src/config_types.rs).
const (
	ProjectTrustLevelTrusted   = "trusted"
	ProjectTrustLevelUntrusted = "untrusted"
)

// ProjectTrustDecision mirrors Rust's loader::ProjectTrustDecision
// (codex-rs/config/src/loader/mod.rs): the saved decision that governs a
// directory and the config key a grant would be written under. Level is empty
// when the effective configuration records no decision for the directory.
type ProjectTrustDecision struct {
	TrustKey string
	Level    string
}

// Saved reports whether the effective configuration already decides the
// project. Rust spells this as `trust_level.is_some()`.
func (d ProjectTrustDecision) Saved() bool {
	return d.Level != ""
}

// Trusted reports whether the saved decision is an explicit trust grant.
func (d ProjectTrustDecision) Trusted() bool {
	return trustLevelIsTrusted(d.Level)
}

// Untrusted reports whether the saved decision is an explicit refusal.
func (d ProjectTrustDecision) Untrusted() bool {
	return trustLevelIsUntrusted(d.Level)
}

// ProjectTrustDecisionForDir resolves the trust decision that governs cwd the
// way Rust's loader does for `codex exec --trust`: the directory's own saved
// decision wins, then the project root's, then the Git checkout's, and an
// undecided directory reports the key a grant would be written under
// (repository root, else project root).
func ProjectTrustDecisionForDir(values map[string]any, cwd string) ProjectTrustDecision {
	trimmed := strings.TrimSpace(cwd)
	if trimmed == "" {
		return ProjectTrustDecision{}
	}
	trustKey, level, saved := newProjectTrustContext(values, trimmed).decisionForDir(trimmed)
	if !saved {
		level = ""
	}
	return ProjectTrustDecision{TrustKey: trustKey, Level: level}
}

// SetProjectTrustLevel persists `[projects."<key>"] trust_level = "<level>"` in
// the config document at configPath, creating the file when it does not exist.
// An existing table for the same key is replaced in place, so repeated grants
// are idempotent and never duplicate the table. Bytes outside that table are
// preserved, matching Rust's ConfigEditsBuilder::set_project_trust_level, which
// edits the document instead of re-rendering it.
func SetProjectTrustLevel(configPath string, projectKey string, level string) error {
	key := strings.TrimSpace(projectKey)
	if key == "" {
		return errors.New("project trust key is required")
	}
	level = strings.TrimSpace(level)
	switch level {
	case ProjectTrustLevelTrusted, ProjectTrustLevelUntrusted:
	default:
		return fmt.Errorf("unsupported project trust level %q", level)
	}
	document := ""
	if data, err := os.ReadFile(configPath); err == nil {
		document = string(data)
	} else if !os.IsNotExist(err) {
		return err
	}
	updated := spliceProjectTrustTable(document, key, level)
	if updated == document {
		return nil
	}
	if dir := filepath.Dir(configPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(configPath, []byte(updated), 0o644)
}

// spliceProjectTrustTable replaces, or appends, the `[projects.<key>]` table.
func spliceProjectTrustTable(document string, key string, level string) string {
	block := fmt.Sprintf("[projects.%s]\ntrust_level = %s\n", tomlBasicString(key), tomlBasicString(level))
	items, _ := scanTOMLDocument(document)
	for _, item := range items {
		if !item.table || item.array || len(item.keyPath) != 2 {
			continue
		}
		if item.keyPath[0] != "projects" {
			continue
		}
		// scanTOMLDocument reports key segments with their TOML quoting removed
		// but their escapes intact, so unescape before comparing with the
		// native key (Windows trust keys carry backslashes).
		if unescapeTOMLBasicKey(item.keyPath[1]) != key {
			continue
		}
		return document[:item.start] + block + document[item.bodyEnd:]
	}
	trimmed := strings.TrimRight(document, "\n")
	if trimmed == "" {
		return block
	}
	return trimmed + "\n\n" + block
}

// tomlBasicString renders value as a TOML basic-string key segment.
func tomlBasicString(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	return `"` + escaped + `"`
}

// unescapeTOMLBasicKey reverses the escapes tomlBasicString applies so a key
// read back from a document compares equal to the native path it came from.
func unescapeTOMLBasicKey(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	var builder strings.Builder
	builder.Grow(len(value))
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i+1 >= len(value) {
			builder.WriteByte(value[i])
			continue
		}
		i++
		switch value[i] {
		case 'n':
			builder.WriteByte('\n')
		case 't':
			builder.WriteByte('\t')
		case 'r':
			builder.WriteByte('\r')
		default:
			builder.WriteByte(value[i])
		}
	}
	return builder.String()
}
