package bottompane

import (
	"path/filepath"
	"strings"

	"codex_go/internal/tui"
)

// Rust parity: codex-rs/tui/src/bottom_pane/chat_composer*.rs.

type AttachmentKind string

const (
	AttachmentFile        AttachmentKind = "file"
	AttachmentImage       AttachmentKind = "image"
	AttachmentRemoteImage AttachmentKind = "remote_image"
)

type ComposerAttachment struct {
	Kind        AttachmentKind
	Path        string
	URL         string
	DisplayName string
}

func (a ComposerAttachment) Label() string {
	if a.DisplayName != "" {
		return a.DisplayName
	}
	if a.Path != "" {
		return filepath.Base(a.Path)
	}
	if a.URL != "" {
		return a.URL
	}
	return string(a.Kind)
}

type ComposerDraftState struct {
	Text        string
	Cursor      int
	Attachments []ComposerAttachment
}

func NewComposerDraftState() *ComposerDraftState {
	return &ComposerDraftState{}
}

func (d *ComposerDraftState) Insert(text string) {
	if d == nil || text == "" {
		return
	}
	d.clampCursor()
	d.Text = d.Text[:d.Cursor] + text + d.Text[d.Cursor:]
	d.Cursor += len(text)
}

func (d *ComposerDraftState) Backspace() bool {
	if d == nil || d.Cursor <= 0 || d.Text == "" {
		return false
	}
	d.clampCursor()
	start := previousRuneStart(d.Text, d.Cursor)
	d.Text = d.Text[:start] + d.Text[d.Cursor:]
	d.Cursor = start
	return true
}

func (d *ComposerDraftState) MoveCursor(delta int) {
	if d == nil {
		return
	}
	d.Cursor += delta
	d.clampCursor()
}

func (d *ComposerDraftState) AddAttachment(attachment ComposerAttachment) {
	if d == nil {
		return
	}
	if attachment.Kind == "" {
		attachment.Kind = AttachmentFile
	}
	d.Attachments = append(d.Attachments, attachment)
}

func (d *ComposerDraftState) RemoveAttachment(index int) bool {
	if d == nil || index < 0 || index >= len(d.Attachments) {
		return false
	}
	d.Attachments = append(d.Attachments[:index], d.Attachments[index+1:]...)
	return true
}

func (d *ComposerDraftState) IsEmpty() bool {
	return d == nil || (strings.TrimSpace(d.Text) == "" && len(d.Attachments) == 0)
}

func (d *ComposerDraftState) SnapshotAndClear() ComposerSubmission {
	if d == nil {
		return ComposerSubmission{}
	}
	submission := ComposerSubmission{
		Text:        d.Text,
		Attachments: append([]ComposerAttachment(nil), d.Attachments...),
	}
	d.Text = ""
	d.Cursor = 0
	d.Attachments = nil
	return submission
}

func (d *ComposerDraftState) clampCursor() {
	if d.Cursor < 0 {
		d.Cursor = 0
	}
	if d.Cursor > len(d.Text) {
		d.Cursor = len(d.Text)
	}
	for d.Cursor > 0 && d.Cursor < len(d.Text) && (d.Text[d.Cursor]&0xC0) == 0x80 {
		d.Cursor--
	}
}

type ComposerSubmission struct {
	Text        string
	Attachments []ComposerAttachment
}

type ComposerHistory struct {
	entries []string
	index   int
}

func NewComposerHistory() *ComposerHistory {
	return &ComposerHistory{index: -1}
}

func (h *ComposerHistory) Add(text string) {
	if h == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if len(h.entries) == 0 || h.entries[len(h.entries)-1] != text {
		h.entries = append(h.entries, text)
	}
	h.index = len(h.entries)
}

func (h *ComposerHistory) Previous() (string, bool) {
	if h == nil || len(h.entries) == 0 {
		return "", false
	}
	if h.index < 0 || h.index > len(h.entries) {
		h.index = len(h.entries)
	}
	if h.index > 0 {
		h.index--
	}
	return h.entries[h.index], true
}

func (h *ComposerHistory) Next() (string, bool) {
	if h == nil || len(h.entries) == 0 {
		return "", false
	}
	if h.index < len(h.entries)-1 {
		h.index++
		return h.entries[h.index], true
	}
	h.index = len(h.entries)
	return "", false
}

func (h *ComposerHistory) Search(query string) []string {
	if h == nil {
		return nil
	}
	query = strings.ToLower(strings.TrimSpace(query))
	out := []string{}
	for i := len(h.entries) - 1; i >= 0; i-- {
		entry := h.entries[i]
		if query == "" || strings.Contains(strings.ToLower(entry), query) {
			out = append(out, entry)
		}
	}
	return out
}

type SlashInputState struct {
	Active bool
	Query  string
	Start  int
}

func DetectSlashInput(text string, cursor int) SlashInputState {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(text) {
		cursor = len(text)
	}
	prefix := text[:cursor]
	start := strings.LastIndex(prefix, "/")
	if start < 0 {
		return SlashInputState{}
	}
	if start > 0 && prefix[start-1] != ' ' && prefix[start-1] != '\n' && prefix[start-1] != '\t' {
		return SlashInputState{}
	}
	if newline := strings.LastIndex(prefix, "\n"); newline > start {
		return SlashInputState{}
	}
	return SlashInputState{Active: true, Query: text[start+1 : cursor], Start: start}
}

type ComposerFooterState struct {
	Running          bool
	QueuedCount      int
	ContextPercent   int
	ActiveAgentLabel string
	Mode             string
	EscPrimed        bool
}

func (s ComposerFooterState) Render(width int) string {
	parts := []string{}
	if s.Mode != "" {
		parts = append(parts, s.Mode)
	}
	if s.Running {
		parts = append(parts, "Ctrl+C interrupt")
	} else if s.EscPrimed {
		parts = append(parts, "Esc again quit")
	} else {
		parts = append(parts, "Enter send")
	}
	parts = append(parts, "Ctrl+J newline")
	parts = append(parts, "Ctrl+G editor")
	if s.QueuedCount > 0 {
		parts = append(parts, formatQueueCount(s.QueuedCount))
	}
	if s.ContextPercent > 0 {
		parts = append(parts, "context "+formatPercent(s.ContextPercent))
	}
	if s.ActiveAgentLabel != "" {
		parts = append(parts, s.ActiveAgentLabel)
	}
	line := strings.Join(parts, " | ")
	if width > 0 {
		return tui.TruncateWithEllipsis(line, width)
	}
	return line
}

type ChatComposerState struct {
	Draft   *ComposerDraftState
	History *ComposerHistory
	Queue   []ComposerSubmission
}

func NewChatComposerState() *ChatComposerState {
	return &ChatComposerState{
		Draft:   NewComposerDraftState(),
		History: NewComposerHistory(),
	}
}

func (c *ChatComposerState) Submit(running bool) (ComposerSubmission, bool) {
	if c == nil || c.Draft == nil || c.Draft.IsEmpty() {
		return ComposerSubmission{}, false
	}
	submission := c.Draft.SnapshotAndClear()
	if c.History != nil {
		c.History.Add(submission.Text)
	}
	if running {
		c.Queue = append(c.Queue, submission)
		return ComposerSubmission{}, false
	}
	return submission, true
}

func (c *ChatComposerState) Dequeue() (ComposerSubmission, bool) {
	if c == nil || len(c.Queue) == 0 {
		return ComposerSubmission{}, false
	}
	submission := c.Queue[0]
	c.Queue = append([]ComposerSubmission(nil), c.Queue[1:]...)
	return submission, true
}

func previousRuneStart(text string, cursor int) int {
	if cursor > len(text) {
		cursor = len(text)
	}
	if cursor <= 0 {
		return 0
	}
	cursor--
	for cursor > 0 && (text[cursor]&0xC0) == 0x80 {
		cursor--
	}
	return cursor
}

func formatQueueCount(count int) string {
	if count == 1 {
		return "1 queued"
	}
	return tui.FormatInt(int64(count)) + " queued"
}

func formatPercent(percent int) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return tui.FormatInt(int64(percent)) + "%"
}
