package tui

// Rust parity subset: codex-rs/tui/src/tui/job_control.rs.

const SuspendKey = "ctrl+z"

type JobControlState struct {
	Suspended bool
}

type ResumeAction string

const (
	ResumeActionRealignInline ResumeAction = "realign_inline"
	ResumeActionRestoreAlt    ResumeAction = "restore_alt"
)

type PreparedResumeActionKind string

const (
	PreparedResumeRealignViewport PreparedResumeActionKind = "realign_viewport"
	PreparedResumeRestoreAlt      PreparedResumeActionKind = "restore_alt_screen"
)

type PreparedResumeAction struct {
	Kind     PreparedResumeActionKind
	Viewport Rect
}

type Rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

type SuspendContext struct {
	ResumePending *ResumeAction
	CursorY       int
}

func NewSuspendContext() *SuspendContext {
	return &SuspendContext{}
}

func (c *SuspendContext) CaptureSuspend(altScreenActive bool) {
	if c == nil {
		return
	}
	action := ResumeActionRealignInline
	if altScreenActive {
		action = ResumeActionRestoreAlt
	}
	c.ResumePending = &action
}

func (c *SuspendContext) PrepareResumeAction(altSavedViewport *Rect) (PreparedResumeAction, bool) {
	if c == nil || c.ResumePending == nil {
		return PreparedResumeAction{}, false
	}
	action := *c.ResumePending
	c.ResumePending = nil
	switch action {
	case ResumeActionRestoreAlt:
		if altSavedViewport != nil {
			altSavedViewport.Y = c.CursorY
		}
		return PreparedResumeAction{Kind: PreparedResumeRestoreAlt}, true
	default:
		return PreparedResumeAction{
			Kind:     PreparedResumeRealignViewport,
			Viewport: Rect{Y: c.CursorY},
		}, true
	}
}

func (c *SuspendContext) SetCursorY(value int) {
	if c != nil {
		c.CursorY = value
	}
}
