package bottompane

// Rust parity: codex-rs/tui/src/bottom_pane/status_line_style.rs.

import "strings"

const StatusLineSeparator = " \u00b7 "

// ANSI sequences used by RenderStyled. Rust applies the same modifiers through
// ratatui styles (dim separators, underlined PR numbers, reset per span).
const (
	statusLineBoldSGR      = "\x1b[1m"
	statusLineDimSGR       = "\x1b[2m"
	statusLineUnderlineSGR = "\x1b[4m"
	statusLineResetSGR     = "\x1b[0m"
)

type StatusLineItem int

const (
	StatusLineModelName StatusLineItem = iota
	StatusLineModelWithReasoning
	StatusLineReasoning
	StatusLineCurrentDir
	StatusLineProjectRoot
	StatusLineHostname
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
	// ThreadIdentity marks ThreadName/ThreadTitle spans that use the
	// per-thread identity color instead of the accent palette (Rust #44857).
	ThreadIdentity bool
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
			span.ThreadIdentity = segment.Item == StatusLineThreadTitle
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

// StatusLineFallbackSGR returns the fallback foreground SGR for an accent when
// the active theme contributes no color for it (Rust
// StatusLineAccent::fallback_style: cyan/green/magenta groups).
func StatusLineFallbackSGR(accent StatusLineAccent) string {
	switch accent {
	case StatusLineAccentModel, StatusLineAccentState, StatusLineAccentMetadata, StatusLineAccentMode:
		return "\x1b[36m"
	case StatusLineAccentPath, StatusLineAccentUsage, StatusLineAccentProgress:
		return "\x1b[32m"
	case StatusLineAccentBranch, StatusLineAccentLimit, StatusLineAccentThread:
		return "\x1b[35m"
	default:
		return ""
	}
}

// RenderStyled renders the status line as ANSI text. Thread identity spans use
// threadColorSGR (empty leaves the terminal default); other themed spans use
// accentColorSGR, falling back to the accent palette when it returns "". Dim
// separators and the underlined PR number match Rust's ratatui styling, and a
// theme-disabled line renders as dim plain text. bold re-applies the footer's
// bold attribute on every span so the per-span resets cannot drop it.
func (l StatusLine) RenderStyled(threadColorSGR string, accentColorSGR func(StatusLineAccent) string, bold bool) string {
	var out strings.Builder
	for _, span := range l.Spans {
		color := ""
		switch {
		case span.ThreadIdentity && threadColorSGR != "":
			color = threadColorSGR
		case span.Accent != "" && span.Accent != StatusLineAccentNone:
			if accentColorSGR != nil {
				color = accentColorSGR(span.Accent)
			}
			if color == "" {
				color = StatusLineFallbackSGR(span.Accent)
			}
		}
		styled := span.Dim || span.Underline || color != ""
		if bold {
			out.WriteString(statusLineBoldSGR)
		}
		if span.Dim {
			out.WriteString(statusLineDimSGR)
		}
		if color != "" {
			out.WriteString(color)
		}
		if span.Underline {
			out.WriteString(statusLineUnderlineSGR)
		}
		out.WriteString(span.Text)
		if styled || bold {
			out.WriteString(statusLineResetSGR)
		}
	}
	return out.String()
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
	case StatusLineCodexVersion, StatusLineHostname, StatusLineSessionID:
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
