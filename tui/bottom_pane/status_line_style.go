package bottompane

// Rust parity: codex-rs/tui/src/bottom_pane/status_line_style.rs.

import (
	"strings"

	"codex_go/tui"
)

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
	StatusLineThreadCredits
	StatusLineEstimatedThreadCost
	StatusLineFiveHourLimit
	StatusLineWeeklyLimit
	StatusLineCodexVersion
	StatusLineSessionID
	StatusLineFastMode
	StatusLineDaybreak
	StatusLineRawOutput
	StatusLinePermissions
	StatusLineApprovalMode
	StatusLineThreadName
	StatusLineThreadTitle
	StatusLineWorkspaceHeadline
	StatusLineTaskProgress
	// StatusLineProfile is the named config profile selected for this
	// invocation (Rust #57516). It is omitted when no profile is selected.
	StatusLineProfile
	// StatusLineProvider is the model provider id of the active thread.
	StatusLineProvider
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
			span.ThreadIdentity = segment.Item == StatusLineThreadName || segment.Item == StatusLineThreadTitle
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

// StatusLineRenderer selects how the configured status line is materialized
// (Rust #57970 StatusLineRenderer).
type StatusLineRenderer string

const (
	// StatusLineRendererNative renders the span-based status line.
	StatusLineRendererNative StatusLineRenderer = "native"
	// StatusLineRendererExtension renders through the shared UI tree.
	StatusLineRendererExtension StatusLineRenderer = "extension"
)

// StatusLineRendererFromFeature maps the tui_extensions_statusline feature to
// its renderer.
func StatusLineRendererFromFeature(extensionEnabled bool) StatusLineRenderer {
	if extensionEnabled {
		return StatusLineRendererExtension
	}
	return StatusLineRendererNative
}

// FitToWidth renders the plain status line truncated to width, keeping the
// truncation ellipsis outside any hyperlink and clipping the link range to the
// visible prefix (Rust #57883 footer hyperlink fitting).
func (l StatusLine) FitToWidth(width int, pullRequestURL string) (string, []tui.TerminalHyperlink) {
	text := l.PlainText()
	links := l.PullRequestHyperlinks(pullRequestURL)
	if width <= 0 {
		return "", nil
	}
	if tui.DisplayWidth(text) <= width {
		return text, links
	}
	keep := width - 1
	fitted := tui.TruncateToWidth(text, keep) + "…"
	return fitted, tui.ClipHyperlinksToWidth(links, keep)
}

// PullRequestHyperlinks returns a hyperlink covering the pull-request-number
// span when its URL is a safe absolute HTTP(S) destination.
func (l StatusLine) PullRequestHyperlinks(pullRequestURL string) []tui.TerminalHyperlink {
	if strings.TrimSpace(pullRequestURL) == "" {
		return nil
	}
	if _, err := tui.NewLinkHref(pullRequestURL); err != nil {
		return nil
	}
	column := 0
	for _, span := range l.Spans {
		width := tui.DisplayWidth(span.Text)
		if span.Underline && !span.Separator {
			return []tui.TerminalHyperlink{{Start: column, End: column + width, Destination: pullRequestURL}}
		}
		column += width
	}
	return nil
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
	case StatusLineModelName, StatusLineModelWithReasoning, StatusLineReasoning, StatusLineProvider:
		return StatusLineAccentModel
	case StatusLineCurrentDir, StatusLineProjectRoot:
		return StatusLineAccentPath
	case StatusLineGitBranch, StatusLinePullRequestNumber, StatusLineBranchChanges:
		return StatusLineAccentBranch
	case StatusLineStatus:
		return StatusLineAccentState
	case StatusLineContextRemaining, StatusLineContextUsed, StatusLineContextWindowSize, StatusLineUsedTokens, StatusLineTotalInputTokens, StatusLineTotalOutputTokens:
		return StatusLineAccentUsage
	case StatusLineThreadCredits, StatusLineEstimatedThreadCost:
		return StatusLineAccentUsage
	case StatusLineFiveHourLimit, StatusLineWeeklyLimit:
		return StatusLineAccentLimit
	case StatusLineCodexVersion, StatusLineHostname, StatusLineSessionID, StatusLineProfile:
		return StatusLineAccentMetadata
	case StatusLineFastMode, StatusLineDaybreak, StatusLineRawOutput, StatusLinePermissions, StatusLineApprovalMode:
		return StatusLineAccentMode
	case StatusLineThreadName, StatusLineThreadTitle, StatusLineWorkspaceHeadline:
		return StatusLineAccentThread
	case StatusLineTaskProgress:
		return StatusLineAccentProgress
	default:
		return StatusLineAccentNone
	}
}
