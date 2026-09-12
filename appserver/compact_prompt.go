package appserver

import (
	"fmt"
	"os"
	"strings"

	"codex_go/compact"
	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// This file ports Rust's compaction prompt resolution
// (core/src/config/mod.rs::load_config and core/src/compact.rs): the
// `compact_prompt` config value comes first, then the
// `experimental_compact_prompt_file` contents; both are trimmed and must be
// non-empty, otherwise the built-in summarization prompt applies.
//
// The file is read on demand rather than during config loading, matching how
// Go already handles the other file-backed config value
// (`model_instructions_file` in appBaseInstructionsForConfig); Rust reads it
// while building the Config.

// compactPromptForConfig resolves the configured compaction prompt.
func compactPromptForConfig(cfg *config.Config) (string, error) {
	if cfg != nil {
		if prompt := strings.TrimSpace(stringConfigValue(cfg, "compact_prompt")); prompt != "" {
			return prompt, nil
		}
	}
	path := ""
	if cfg != nil {
		path = strings.TrimSpace(stringConfigValue(cfg, "experimental_compact_prompt_file"))
	}
	if path == "" {
		return compact.SummarizationPrompt, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read experimental compact prompt file %s: %w", path, err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", fmt.Errorf("experimental compact prompt file is empty: %s", path)
	}
	return text, nil
}

// compactPromptForRecord resolves the prompt for a thread's compaction using the
// same effective-config inputs the compaction model resolution uses.
func (r *RuntimeRouter) compactPromptForRecord(record *session.Record) (string, error) {
	params := &turn.TurnStartParams{}
	if record != nil {
		params.ThreadID = string(record.ID)
		params.CWD = strings.TrimSpace(record.Metadata.CWD)
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return "", err
	}
	return compactPromptForConfig(cfg)
}
