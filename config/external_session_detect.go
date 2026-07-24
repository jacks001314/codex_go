package config

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *ConfigService) detectExternalSessionMigration() (ExternalAgentConfigMigrationItem, bool) {
	sessions := discoverExternalSessions(s.externalAgentHome)
	if len(sessions) == 0 {
		return ExternalAgentConfigMigrationItem{}, false
	}
	return ExternalAgentConfigMigrationItem{
		ItemType:    MigrationSessions,
		Description: "Import external-agent chat sessions",
		Details:     &MigrationDetails{Sessions: sessions},
	}, true
}

func discoverExternalSessions(home string) []SessionMigration {
	root := filepath.Join(strings.TrimSpace(home), "projects")
	var sessions []SessionMigration
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() ||
			!strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
			return nil
		}
		cwd := externalSessionCWD(path)
		title := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		sessions = append(sessions, SessionMigration{
			Path: path,
			CWD:  cwd,
			Title: func() *string {
				if strings.EqualFold(title, "session") || strings.TrimSpace(title) == "" {
					return nil
				}
				return &title
			}(),
		})
		return nil
	})
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Path < sessions[j].Path })
	return sessions
}

func externalSessionCWD(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var row struct {
			CWD string `json:"cwd"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) == nil && strings.TrimSpace(row.CWD) != "" {
			return strings.TrimSpace(row.CWD)
		}
	}
	return ""
}
