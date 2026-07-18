package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"codex_go/rollout"
)

const PersonalityMigrationFilename = ".personality_migration"

type PersonalityMigrationStatus string

const (
	PersonalityMigrationSkippedMarker              PersonalityMigrationStatus = "skipped_marker"
	PersonalityMigrationSkippedExplicitPersonality PersonalityMigrationStatus = "skipped_explicit_personality"
	PersonalityMigrationSkippedNoSessions          PersonalityMigrationStatus = "skipped_no_sessions"
	PersonalityMigrationApplied                    PersonalityMigrationStatus = "applied"
)

var errRecordedSessionFound = errors.New("recorded session found")

func (s *ConfigService) MaybeMigratePersonality() (PersonalityMigrationStatus, error) {
	if s == nil {
		return PersonalityMigrationSkippedNoSessions, nil
	}
	codexHome := strings.TrimSpace(s.CodexHome())
	if codexHome == "" {
		return PersonalityMigrationSkippedNoSessions, nil
	}
	return MaybeMigratePersonality(codexHome)
}

func MaybeMigratePersonality(codexHome string) (PersonalityMigrationStatus, error) {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return PersonalityMigrationSkippedNoSessions, nil
	}
	markerPath := filepath.Join(codexHome, PersonalityMigrationFilename)
	if _, err := os.Stat(markerPath); err == nil {
		return PersonalityMigrationSkippedMarker, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	configPath := ConfigPath(codexHome)
	values, err := loadConfigFile(configPath)
	if err != nil {
		return "", err
	}
	if value, ok := values["personality"].(string); ok && strings.TrimSpace(value) != "" {
		if err := createPersonalityMigrationMarker(markerPath); err != nil {
			return "", err
		}
		return PersonalityMigrationSkippedExplicitPersonality, nil
	}

	hasSessions, err := hasRecordedPersonalityMigrationSessions(codexHome)
	if err != nil {
		return "", err
	}
	if !hasSessions {
		if err := createPersonalityMigrationMarker(markerPath); err != nil {
			return "", err
		}
		return PersonalityMigrationSkippedNoSessions, nil
	}

	values = cloneMap(values)
	values["personality"] = "pragmatic"
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(configPath, []byte(renderTOML(values)), 0o600); err != nil {
		return "", err
	}
	if err := createPersonalityMigrationMarker(markerPath); err != nil {
		return "", err
	}
	return PersonalityMigrationApplied, nil
}

func createPersonalityMigrationMarker(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString("v1\n")
	return err
}

func hasRecordedPersonalityMigrationSessions(codexHome string) (bool, error) {
	for _, entry := range []struct {
		subdir   string
		archived bool
	}{
		{subdir: rollout.SessionsSubdir},
		{subdir: rollout.ArchivedSessionsSubdir, archived: true},
	} {
		root := filepath.Join(codexHome, entry.subdir)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return filepath.SkipDir
			}
			if err != nil {
				return err
			}
			if d == nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
				return nil
			}
			record, err := rollout.RecordFromPath(path, entry.archived)
			if err != nil || record == nil {
				return nil
			}
			for i := range record.Items {
				item := &record.Items[i]
				if item.Type == "user_message" || (item.Type == "message" && item.Role == "user") {
					return errRecordedSessionFound
				}
			}
			return nil
		})
		if errors.Is(err, errRecordedSessionFound) {
			return true, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
	}
	return false, nil
}
