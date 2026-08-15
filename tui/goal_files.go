package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const (
	// GoalFilePrefix/GoalFileSuffix/GoalFileName mirror Rust
	// tui/src/goal_files.rs constants for materialized goal objective files.
	GoalFilePrefix    = "Read the Codex goal objective file at "
	GoalFileSuffix    = " before continuing."
	GoalFileName      = "goal-objective.md"
	GoalAttachmentDir = "attachments"
	// MaxGoalObjectiveRune matches protocol MAX_THREAD_GOAL_OBJECTIVE_CHARS.
	MaxGoalObjectiveRune = 4000
)

type GoalFileReference struct {
	Path string
	Line int
}

// GoalLocalImage mirrors chat_composer.LocalImageAttachment without importing
// that package (which depends back on codex_go/tui).
type GoalLocalImage struct {
	Placeholder string
	Path        string
}

// GoalDraft mirrors Rust tui/src/goal_files.rs GoalDraft: the objective text
// plus the composer attachments (pasted text, local images, remote image URLs)
// that are materialized into app-server files before the goal is persisted.
type GoalDraft struct {
	Objective       string
	PendingPastes   [][2]string
	LocalImages     []GoalLocalImage
	RemoteImageURLs []string
}

// GoalObjectiveFileReference builds the objective text that references a
// materialized goal file, mirroring Rust goal_files::objective_file_reference.
func GoalObjectiveFileReference(path string) (string, error) {
	reference := GoalFilePrefix + path + GoalFileSuffix
	if len([]rune(reference)) > MaxGoalObjectiveRune {
		return "", fmt.Errorf(
			"Goal objective file reference is too long: %d characters. Limit: %d characters.",
			len([]rune(reference)),
			MaxGoalObjectiveRune,
		)
	}
	return reference, nil
}

// GoalObjectiveFilePath parses a goal objective file reference and returns the
// resolved path only when it points into <codexHome>/attachments/<uuid>/
// goal-objective.md with a valid UUID, mirroring Rust
// goal_files::objective_file_path.
func GoalObjectiveFilePath(objective, codexHome string) (string, bool) {
	candidate, ok := goalObjectiveFileCandidate(objective)
	if !ok {
		return "", false
	}
	if !filepath.IsAbs(candidate) {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(candidate))
	attachmentID := filepath.Base(filepath.Dir(clean))
	if _, err := uuid.Parse(attachmentID); err != nil {
		return "", false
	}
	expected := filepath.Join(
		filepath.Clean(codexHome),
		GoalAttachmentDir,
		attachmentID,
		GoalFileName,
	)
	if !goalPathEqualFold(clean, expected) {
		return "", false
	}
	return clean, true
}

func goalObjectiveFileCandidate(objective string) (string, bool) {
	if !strings.HasPrefix(objective, GoalFilePrefix) {
		return "", false
	}
	rest := strings.TrimPrefix(objective, GoalFilePrefix)
	if !strings.HasSuffix(rest, GoalFileSuffix) {
		return "", false
	}
	path := strings.TrimSuffix(rest, GoalFileSuffix)
	if path == "" {
		return "", false
	}
	return path, true
}

func goalPathEqualFold(left, right string) bool {
	if filepath.Separator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}
