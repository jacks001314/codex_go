package chatwidget

import "strings"

type ChatWidgetRenderState struct {
	Width                    int
	Height                   int
	ModalActive              bool
	TranscriptRows           []string
	ActiveTranscriptRows     []string
	ActiveHookRows           []string
	PendingTokenActivityRows []string
	PendingRateLimitHintRows []string
	BottomPaneRows           []string
	AmbientPetReservedCols   int
	Status                   StatusIndicatorState
}

type ChatWidgetRenderSectionKind string

const (
	RenderSectionActiveTranscript     ChatWidgetRenderSectionKind = "active_transcript"
	RenderSectionActiveHook           ChatWidgetRenderSectionKind = "active_hook"
	RenderSectionPendingTokenActivity ChatWidgetRenderSectionKind = "pending_token_activity"
	RenderSectionPendingRateLimitHint ChatWidgetRenderSectionKind = "pending_rate_limit_hint"
	RenderSectionBottomPane           ChatWidgetRenderSectionKind = "bottom_pane"
)

type ChatWidgetRenderSection struct {
	Kind         ChatWidgetRenderSectionKind
	Flex         int
	TopInset     int
	RightReserve int
	Width        int
	Height       int
	Rows         []string
	ScrollOffset int
}

type ChatWidgetRenderPlan struct {
	Width             int
	Height            int
	LastRenderedWidth int
	Sections          []ChatWidgetRenderSection
}

func (s ChatWidgetRenderState) HistoryWrapWidth(reservedColumns int) int {
	width := s.Width - reservedColumns
	if width < 1 {
		return 1
	}
	return width
}

func (s ChatWidgetRenderState) HasVisibleModal() bool {
	return s.ModalActive
}

func (s ChatWidgetRenderState) ComposeRenderPlan() ChatWidgetRenderPlan {
	width := s.Width
	if width < 1 {
		width = 1
	}
	height := s.Height
	if height < 0 {
		height = 0
	}
	reserve := s.AmbientPetReservedCols
	if reserve < 0 {
		reserve = 0
	}
	plan := ChatWidgetRenderPlan{
		Width:             width,
		Height:            height,
		LastRenderedWidth: width,
	}
	activeRows := s.ActiveTranscriptRows
	if activeRows == nil {
		activeRows = s.TranscriptRows
	}
	plan.Sections = append(plan.Sections, s.transcriptSection(RenderSectionActiveTranscript, activeRows, 1, reserve))
	if len(s.ActiveHookRows) > 0 {
		plan.Sections = append(plan.Sections, s.transcriptSection(RenderSectionActiveHook, s.ActiveHookRows, 0, reserve))
	}
	if len(s.PendingTokenActivityRows) > 0 {
		plan.Sections = append(plan.Sections, s.transcriptSection(RenderSectionPendingTokenActivity, s.PendingTokenActivityRows, 1, reserve))
	}
	if len(s.PendingRateLimitHintRows) > 0 {
		plan.Sections = append(plan.Sections, s.transcriptSection(RenderSectionPendingRateLimitHint, s.PendingRateLimitHintRows, 1, reserve))
	}
	plan.Sections = append(plan.Sections, ChatWidgetRenderSection{
		Kind:         RenderSectionBottomPane,
		Flex:         0,
		TopInset:     1,
		RightReserve: reserve,
		Width:        maxRenderInt(width-reserve, 1),
		Rows:         append([]string(nil), s.BottomPaneRows...),
	})
	return plan
}

func (s ChatWidgetRenderState) transcriptSection(kind ChatWidgetRenderSectionKind, rows []string, flex int, rightReserve int) ChatWidgetRenderSection {
	childWidth := s.HistoryWrapWidth(rightReserve)
	childHeight := s.Height - 1
	if childHeight < 0 {
		childHeight = 0
	}
	return ChatWidgetRenderSection{
		Kind:         kind,
		Flex:         flex,
		TopInset:     1,
		RightReserve: rightReserve,
		Width:        childWidth,
		Height:       childHeight,
		Rows:         append([]string(nil), rows...),
		ScrollOffset: TranscriptAreaScrollOffset(len(rows), childHeight),
	}
}

func TranscriptAreaScrollOffset(lineCount int, height int) int {
	if height <= 0 || lineCount <= height {
		return 0
	}
	return lineCount - height
}

func TranscriptAreaVisibleRows(rows []string, height int) []string {
	if height <= 0 {
		return nil
	}
	offset := TranscriptAreaScrollOffset(len(rows), height)
	return append([]string(nil), rows[offset:]...)
}

func (s ChatWidgetRenderState) CompactTranscript(maxRows int) []string {
	if maxRows <= 0 || len(s.TranscriptRows) <= maxRows {
		return append([]string(nil), s.TranscriptRows...)
	}
	return append([]string{"..."}, s.TranscriptRows[len(s.TranscriptRows)-maxRows+1:]...)
}

func RenderStatusLineParts(parts ...string) string {
	out := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, " | ")
}

func maxRenderInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
