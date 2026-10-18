package tui

import (
	"fmt"
	"strings"
	"time"
)

// Rust parity: codex-rs/tui/src/status_indicator_widget.rs.

const StatusDetailsDefaultMaxLines = 3

type StatusDetailsCapitalization int

const (
	StatusDetailsCapitalizeFirst StatusDetailsCapitalization = iota
	StatusDetailsPreserve
)

type StatusIndicator struct {
	Header            string
	Details           string
	DetailsMaxLines   int
	InlineMessage     string
	ShowInterruptHint bool
	InterruptHint     string
	StartedAt         time.Time
	PausedElapsed     time.Duration
	Paused            bool
}

// StatusTimer is the pause-aware clock owned independently of the optional
// status row (Rust #41913). Streaming may hide the row, and modal approvals
// pause the clock without losing accumulated time.
type StatusTimer struct {
	ElapsedRunning time.Duration
	LastResumeAt   time.Time
	Paused         bool
}

func NewStatusTimer(now time.Time) StatusTimer {
	if now.IsZero() {
		now = time.Now()
	}
	return StatusTimer{LastResumeAt: now}
}

// ResetAt replaces the accumulated elapsed time without changing a modal's
// pause state, matching Rust's StatusTimer::reset.
func (t *StatusTimer) ResetAt(elapsed time.Duration, now time.Time) {
	if t == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	t.ElapsedRunning = elapsed
	t.LastResumeAt = now
}

func (t *StatusTimer) PauseAt(now time.Time) {
	if t == nil || t.Paused {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	if !t.LastResumeAt.IsZero() && now.After(t.LastResumeAt) {
		t.ElapsedRunning += now.Sub(t.LastResumeAt)
	}
	t.Paused = true
}

func (t *StatusTimer) ResumeAt(now time.Time) {
	if t == nil || !t.Paused {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	t.LastResumeAt = now
	t.Paused = false
}

func (t *StatusTimer) ElapsedAt(now time.Time) time.Duration {
	if t == nil {
		return 0
	}
	if t.Paused || t.LastResumeAt.IsZero() {
		return t.ElapsedRunning
	}
	if now.IsZero() {
		now = time.Now()
	}
	if now.Before(t.LastResumeAt) {
		return t.ElapsedRunning
	}
	return t.ElapsedRunning + now.Sub(t.LastResumeAt)
}

func NewStatusIndicator(now time.Time) *StatusIndicator {
	if now.IsZero() {
		now = time.Now()
	}
	return &StatusIndicator{
		Header:            "Working",
		DetailsMaxLines:   StatusDetailsDefaultMaxLines,
		ShowInterruptHint: true,
		InterruptHint:     "esc",
		StartedAt:         now,
	}
}

func FormatElapsedCompact(elapsedSeconds int64) string {
	if elapsedSeconds < 60 {
		return fmt.Sprintf("%ds", elapsedSeconds)
	}
	if elapsedSeconds < 3600 {
		return fmt.Sprintf("%dm %02ds", elapsedSeconds/60, elapsedSeconds%60)
	}
	return fmt.Sprintf("%dh %02dm %02ds", elapsedSeconds/3600, (elapsedSeconds%3600)/60, elapsedSeconds%60)
}

func (s *StatusIndicator) UpdateHeader(header string) {
	if s == nil {
		return
	}
	s.Header = header
}

func (s *StatusIndicator) UpdateDetails(details string, capitalization StatusDetailsCapitalization, maxLines int) {
	if s == nil {
		return
	}
	if maxLines <= 0 {
		maxLines = 1
	}
	details = strings.TrimLeft(details, " \t\r\n")
	if capitalization == StatusDetailsCapitalizeFirst {
		details = CapitalizeFirst(details)
	}
	s.Details = details
	s.DetailsMaxLines = maxLines
}

func (s *StatusIndicator) UpdateInlineMessage(message string) {
	if s == nil {
		return
	}
	s.InlineMessage = strings.TrimSpace(message)
}

func (s *StatusIndicator) SetInterruptHintVisible(visible bool) {
	if s == nil {
		return
	}
	s.ShowInterruptHint = visible
}

func (s *StatusIndicator) PauseAt(now time.Time) {
	if s == nil || s.Paused {
		return
	}
	s.PausedElapsed = s.Elapsed(now)
	s.Paused = true
}

func (s *StatusIndicator) ResumeAt(now time.Time) {
	if s == nil || !s.Paused {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.StartedAt = now
	s.Paused = false
}

func (s *StatusIndicator) Elapsed(now time.Time) time.Duration {
	if s == nil {
		return 0
	}
	if s.Paused {
		return s.PausedElapsed
	}
	if now.IsZero() {
		now = time.Now()
	}
	if s.StartedAt.IsZero() || now.Before(s.StartedAt) {
		return s.PausedElapsed
	}
	return s.PausedElapsed + now.Sub(s.StartedAt)
}

func (s *StatusIndicator) Render(width int, now time.Time) []string {
	if s == nil {
		return nil
	}
	header := strings.TrimSpace(s.Header)
	if header == "" {
		header = "Working"
	}
	elapsed := FormatElapsedCompact(int64(s.Elapsed(now).Seconds()))
	line := header + " (" + elapsed
	if s.ShowInterruptHint && strings.TrimSpace(s.InterruptHint) != "" {
		line += " \u2022 " + strings.TrimSpace(s.InterruptHint) + " to interrupt"
	}
	line += ")"
	if strings.TrimSpace(s.InlineMessage) != "" {
		line += " \u2022 " + strings.TrimSpace(s.InlineMessage)
	}
	if width > 0 {
		line = TruncateWithEllipsis(line, width)
	}
	lines := []string{line}
	if strings.TrimSpace(s.Details) == "" || width <= 0 {
		return lines
	}
	prefix := "  - "
	contentWidth, ok := UsableContentWidth(width, DisplayWidth(prefix))
	if !ok {
		return lines
	}
	wrapped := WrapLines(strings.Split(s.Details, "\n"), WrapOptions{
		Width:            contentWidth,
		SubsequentIndent: "",
		BreakWords:       true,
	})
	maxLines := s.DetailsMaxLines
	if maxLines <= 0 {
		maxLines = StatusDetailsDefaultMaxLines
	}
	if len(wrapped) > maxLines {
		wrapped = wrapped[:maxLines]
		wrapped[len(wrapped)-1] = appendEllipsisToWidth(wrapped[len(wrapped)-1], contentWidth)
	}
	for _, detail := range wrapped {
		lines = append(lines, prefix+detail)
	}
	return lines
}

func appendEllipsisToWidth(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if strings.HasSuffix(text, "…") {
		return TruncateWithEllipsis(text, width)
	}
	if DisplayWidth(text)+1 <= width {
		return text + "…"
	}
	return TruncateToWidth(text, width-1) + "…"
}
