package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	DefaultAgentsMDFilename = "AGENTS.md"
	LocalAgentsMDFilename   = "AGENTS.override.md"
)

type Instructions struct {
	Text   string `json:"text"`
	Source string `json:"source"`
}

type LoadedUserInstructions struct {
	Instructions *Instructions `json:"instructions,omitempty"`
	Warnings     []string      `json:"warnings,omitempty"`
}

type UserInstructionsProvider struct {
	CodexHome string
}

func NewUserInstructionsProvider(codexHome string) *UserInstructionsProvider {
	return &UserInstructionsProvider{CodexHome: codexHome}
}

func (p *UserInstructionsProvider) Load() *LoadedUserInstructions {
	loaded := &LoadedUserInstructions{}
	if p == nil || p.CodexHome == "" {
		return loaded
	}
	for _, candidate := range []string{LocalAgentsMDFilename, DefaultAgentsMDFilename} {
		path := filepath.Join(p.CodexHome, candidate)
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			continue
		}
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			loaded.Warnings = append(loaded.Warnings, readWarning(path, err))
			continue
		}
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			loaded.Warnings = append(loaded.Warnings, readWarning(path, err))
			continue
		}
		validUTF8 := utf8.Valid(data)
		if !validUTF8 {
			loaded.Warnings = append(loaded.Warnings, fmt.Sprintf("Global AGENTS.md instructions from `%s` contain invalid UTF-8; invalid bytes were replaced.", path))
		}
		text := string(data)
		if !validUTF8 {
			text = strings.ToValidUTF8(text, "\ufffd")
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		loaded.Instructions = &Instructions{Text: text, Source: path}
		return loaded
	}
	return loaded
}

func readWarning(path string, err error) string {
	return fmt.Sprintf("Failed to read global AGENTS.md instructions from `%s`: %v", path, err)
}
