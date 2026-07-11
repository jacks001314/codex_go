package mentionsv2

// Rust parity subset: codex-rs/tui/src/bottom_pane/mentions_v2/candidate.rs.

const mentionTagWidth = len("Plugin")

type SelectionKind string

const (
	SelectionFile SelectionKind = "file"
	SelectionTool SelectionKind = "tool"
)

type Selection struct {
	Kind       SelectionKind
	Path       string
	InsertText string
}

func FileSelection(path string) Selection {
	return Selection{Kind: SelectionFile, Path: path}
}

func ToolSelection(insertText string, path string) Selection {
	return Selection{Kind: SelectionTool, InsertText: insertText, Path: path}
}

type MentionType string

const (
	MentionTypePlugin    MentionType = "plugin"
	MentionTypeSkill     MentionType = "skill"
	MentionTypeFile      MentionType = "file"
	MentionTypeDirectory MentionType = "directory"
)

func (t MentionType) IsFilesystem() bool {
	return t == MentionTypeFile || t == MentionTypeDirectory
}

func (t MentionType) Label() string {
	switch t {
	case MentionTypePlugin:
		return "Plugin"
	case MentionTypeSkill:
		return "Skill"
	case MentionTypeFile:
		return "File"
	case MentionTypeDirectory:
		return "Dir"
	default:
		return ""
	}
}

func (t MentionType) Tag() string {
	label := t.Label()
	for len(label) < mentionTagWidth {
		label += " "
	}
	return label
}

type Candidate struct {
	ID          string
	Label       string
	DisplayName string
	Description string
	SearchTerms []string
	MentionType MentionType
	Selection   Selection
}

type SearchResult struct {
	DisplayName  string
	Description  string
	MentionType  MentionType
	Selection    Selection
	MatchIndices []int
	Score        int
}

func (c Candidate) ToResult(matchIndices []int, score int) SearchResult {
	return SearchResult{
		DisplayName:  c.DisplayName,
		Description:  c.Description,
		MentionType:  c.MentionType,
		Selection:    c.Selection,
		MatchIndices: append([]int(nil), matchIndices...),
		Score:        score,
	}
}
