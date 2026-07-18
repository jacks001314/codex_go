package mentionsv2

// Rust parity subset: codex-rs/tui/src/bottom_pane/mentions_v2/search_mode.rs.

type SearchMode string

const (
	SearchModeResults        SearchMode = "results"
	SearchModeFilesystemOnly SearchMode = "filesystem_only"
	SearchModeTools          SearchMode = "tools"

	SearchModeFiles  = SearchModeFilesystemOnly
	SearchModeSkills = SearchModeTools
)

func (m SearchMode) Previous() SearchMode {
	switch m {
	case SearchModeResults:
		return SearchModeTools
	case SearchModeFilesystemOnly:
		return SearchModeResults
	case SearchModeTools:
		return SearchModeFilesystemOnly
	default:
		return SearchModeResults
	}
}

func (m SearchMode) Next() SearchMode {
	switch m {
	case SearchModeResults:
		return SearchModeFilesystemOnly
	case SearchModeFilesystemOnly:
		return SearchModeTools
	case SearchModeTools:
		return SearchModeResults
	default:
		return SearchModeResults
	}
}

func (m SearchMode) Accepts(mentionType MentionType) bool {
	switch m {
	case SearchModeResults, "":
		return true
	case SearchModeFilesystemOnly:
		return mentionType == MentionTypeFile || mentionType == MentionTypeDirectory
	case SearchModeTools:
		return mentionType == MentionTypePlugin || mentionType == MentionTypeSkill
	default:
		return true
	}
}

func (m SearchMode) Label() string {
	switch m {
	case SearchModeResults, "":
		return "All Results"
	case SearchModeFilesystemOnly:
		return "Filesystem Only"
	case SearchModeTools:
		return "Plugins"
	default:
		return string(m)
	}
}
