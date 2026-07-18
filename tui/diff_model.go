package tui

// Rust parity: codex-rs/tui/src/diff_model.rs.

type FileChangeType string

const (
	FileChangeAdd    FileChangeType = "add"
	FileChangeDelete FileChangeType = "delete"
	FileChangeUpdate FileChangeType = "update"
)

type FileChange struct {
	Type        FileChangeType `json:"type"`
	Content     string         `json:"content,omitempty"`
	UnifiedDiff string         `json:"unified_diff,omitempty"`
	MovePath    string         `json:"move_path,omitempty"`
}

func NewAddFileChange(content string) FileChange {
	return FileChange{Type: FileChangeAdd, Content: content}
}

func NewDeleteFileChange(content string) FileChange {
	return FileChange{Type: FileChangeDelete, Content: content}
}

func NewUpdateFileChange(unifiedDiff string, movePath string) FileChange {
	return FileChange{Type: FileChangeUpdate, UnifiedDiff: unifiedDiff, MovePath: movePath}
}
