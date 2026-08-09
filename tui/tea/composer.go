package tea

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"

	bottompane "codex_go/tui/bottom_pane"
)

// ComposerComponent manages the user input area: the textarea widget,
// slash/skill popups, input history, submission queue, paste handling,
// and file/image attachments.
type ComposerComponent struct {
	textarea        textarea.Model
	attachments     []bottompane.ComposerAttachment
	mentionBindings []string

	// Inline popups
	slashPopup slashCommandPopup
	skillPopup skillPopupState

	// Input history
	inputHistory       []string
	inputHistoryIndex  int
	inputHistoryDraft  string
	inputHistoryActive bool

	// Submission queue
	submitted      []string
	submitRequests []SubmitRequest
	queued         []queuedSubmission
	editorActive   bool

	// Paste burst handling
	pasteEnterUntil *time.Time

	// Side conversation placeholder
	sidePlaceholder string
}

// newComposerComponent initializes the composer sub-component.
func newComposerComponent(placeholder string) ComposerComponent {
	composer := textarea.New()
	composer.Prompt = "> "
	if placeholder != "" {
		composer.Placeholder = placeholder
	} else {
		composer.Placeholder = "Ask gcode"
	}
	composer.ShowLineNumbers = false
	composer.CharLimit = 0
	composer.SetHeight(defaultComposerHeight)
	composer.SetWidth(defaultWidth)
	composer.Focus()
	return ComposerComponent{
		textarea:        composer,
		sidePlaceholder: "Side conversation - type /exit-side to return",
	}
}

// Textarea returns the underlying textarea for Bubble Tea Update delegation.
func (c *ComposerComponent) Textarea() *textarea.Model {
	if c == nil {
		return nil
	}
	return &c.textarea
}

// Value returns the current composer text.
func (c *ComposerComponent) Value() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.textarea.Value())
}

// SubmittedPrompts returns all submitted prompts in order.
func (c *ComposerComponent) SubmittedPrompts() []string {
	if c == nil {
		return nil
	}
	out := make([]string, len(c.submitted))
	copy(out, c.submitted)
	return out
}

// SubmittedRequests returns all submitted request objects in order.
func (c *ComposerComponent) SubmittedRequests() []SubmitRequest {
	if c == nil {
		return nil
	}
	out := make([]SubmitRequest, len(c.submitRequests))
	copy(out, c.submitRequests)
	return out
}

// QueuedRequests returns the current submission queue.
func (c *ComposerComponent) QueuedRequests() []SubmitRequest {
	if c == nil || len(c.queued) == 0 {
		return nil
	}
	out := make([]SubmitRequest, len(c.queued))
	for i, q := range c.queued {
		out[i] = q.Request
	}
	return out
}

// Attachments returns the current file/image attachments.
func (c *ComposerComponent) Attachments() []bottompane.ComposerAttachment {
	if c == nil {
		return nil
	}
	return c.attachments
}

// ClearAttachments removes all pending attachments.
func (c *ComposerComponent) ClearAttachments() {
	if c != nil {
		c.attachments = nil
		c.mentionBindings = nil
	}
}

// IsEditorActive returns whether the external editor is open.
func (c *ComposerComponent) IsEditorActive() bool {
	if c == nil {
		return false
	}
	return c.editorActive
}

// SetEditorActive sets the external editor activity state.
func (c *ComposerComponent) SetEditorActive(active bool) {
	if c != nil {
		c.editorActive = active
	}
}

// SlashPopup returns the current slash command popup state.
func (c *ComposerComponent) SlashPopup() *slashCommandPopup {
	if c == nil {
		return nil
	}
	return &c.slashPopup
}

// SkillPopup returns the current skill popup state.
func (c *ComposerComponent) SkillPopup() *skillPopupState {
	if c == nil {
		return nil
	}
	return &c.skillPopup
}

// Reset resets the composer textarea value.
func (c *ComposerComponent) Reset() {
	if c != nil {
		c.textarea.Reset()
		c.textarea.Focus()
	}
}

// SetValue sets the composer text.
func (c *ComposerComponent) SetValue(value string) {
	if c != nil {
		c.textarea.SetValue(value)
	}
}
