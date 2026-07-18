package bottompane

// Rust parity: codex-rs/tui/src/bottom_pane/status_line_style.rs.

const StatusLineSeparator = " \u00b7 "

type StatusLineItem int

const (
	StatusLineModelName StatusLineItem = iota
	StatusLineModelWithReasoning
	StatusLineReasoning
	StatusLineCurrentDir
	StatusLineProjectRoot
	StatusLineGitBranch
	StatusLinePullRequestNumber
	StatusLineBranchChanges
	StatusLineStatus
	StatusLineContextRemaining
	StatusLineContextUsed
	StatusLineContextWindowSize
	StatusLineUsedTokens
	StatusLineTotalInputTokens
	StatusLineTotalOutputTokens
	StatusLineFiveHourLimit
	StatusLineWeeklyLimit
	StatusLineCodexVersion
	StatusLineSessionID
	StatusLineFastMode
	StatusLineRawOutput
	StatusLinePermissions
	StatusLineApprovalMode
	StatusLineThreadTitle
	StatusLineWorkspaceHeadline
	StatusLineTaskProgress
)

type StatusLineAccent string

const (
	StatusLineAccentModel    StatusLineAccent = "model"
	StatusLineAccentPath     StatusLineAccent = "path"
	StatusLineAccentBranch   StatusLineAccent = "branch"
	StatusLineAccentState    StatusLineAccent = "state"
	StatusLineAccentUsage    StatusLineAccent = "usage"
	StatusLineAccentLimit    StatusLineAccent = "limit"
	StatusLineAccentMetadata StatusLineAccent = "metadata"
	StatusLineAccentMode     StatusLineAccent = "mode"
	StatusLineAccentThread   StatusLineAccent = "thread"
	StatusLineAccentProgress StatusLineAccent = "progress"
	StatusLineAccentNone     StatusLineAccent = "none"
)

type StatusLineSegment struct {
	Item StatusLineItem
	Text string
}

type StatusLineSpan struct {
	Text      string
	Accent    StatusLineAccent
	Dim       bool
	Underline bool
	Separator bool
}

type StatusLine struct {
	Spans []StatusLineSpan
}

func StatusLineFromSegments(segments []StatusLineSegment, useThemeColors bool) (StatusLine, bool) {
	if len(segments) == 0 {
		return StatusLine{}, false
	}
	spans := make([]StatusLineSpan, 0, len(segments)*2-1)
	for _, segment := range segments {
		if len(spans) > 0 {
			spans = append(spans, StatusLineSpan{Text: StatusLineSeparator, Dim: true, Separator: true})
		}
		span := StatusLineSpan{Text: segment.Text}
		if useThemeColors {
			span.Accent = StatusLineAccentForItem(segment.Item)
		} else {
			span.Accent = StatusLineAccentNone
			span.Dim = true
		}
		if segment.Item == StatusLinePullRequestNumber {
			span.Underline = true
		}
		spans = append(spans, span)
	}
	return StatusLine{Spans: spans}, true
}

func (l StatusLine) PlainText() string {
	out := ""
	for _, span := range l.Spans {
		out += span.Text
	}
	return out
}

func StatusLineAccentForItem(item StatusLineItem) StatusLineAccent {
	switch item {
	case StatusLineModelName, StatusLineModelWithReasoning, StatusLineReasoning:
		return StatusLineAccentModel
	case StatusLineCurrentDir, StatusLineProjectRoot:
		return StatusLineAccentPath
	case StatusLineGitBranch, StatusLinePullRequestNumber, StatusLineBranchChanges:
		return StatusLineAccentBranch
	case StatusLineStatus:
		return StatusLineAccentState
	case StatusLineContextRemaining, StatusLineContextUsed, StatusLineContextWindowSize, StatusLineUsedTokens, StatusLineTotalInputTokens, StatusLineTotalOutputTokens:
		return StatusLineAccentUsage
	case StatusLineFiveHourLimit, StatusLineWeeklyLimit:
		return StatusLineAccentLimit
	case StatusLineCodexVersion, StatusLineSessionID:
		return StatusLineAccentMetadata
	case StatusLineFastMode, StatusLineRawOutput, StatusLinePermissions, StatusLineApprovalMode:
		return StatusLineAccentMode
	case StatusLineThreadTitle, StatusLineWorkspaceHeadline:
		return StatusLineAccentThread
	case StatusLineTaskProgress:
		return StatusLineAccentProgress
	default:
		return StatusLineAccentNone
	}
}
