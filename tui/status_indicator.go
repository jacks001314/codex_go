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
