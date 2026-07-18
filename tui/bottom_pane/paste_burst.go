package bottompane

import "time"

// Rust parity: codex-rs/tui/src/bottom_pane/paste_burst.rs.

const (
	PasteBurstMinChars          = 3
	PasteEnterSuppressWindow    = 120 * time.Millisecond
	PasteBurstCharInterval      = 8 * time.Millisecond
	PasteBurstActiveIdleTimeout = 60 * time.Millisecond
)

type PasteBurst struct {
	lastPlainCharTime     *time.Time
	consecutivePlainChars int
	burstWindowUntil      *time.Time
	buffer                string
	active                bool
	pendingFirstChar      rune
	pendingFirstCharTime  *time.Time
	hasPendingFirstChar   bool
}

type CharDecision int

const (
	CharDecisionBeginBuffer CharDecision = iota
	CharDecisionBufferAppend
	CharDecisionRetainFirstChar
	CharDecisionBeginBufferFromPending
)

type CharDecisionResult struct {
	Decision   CharDecision
	RetroChars int
}

type RetroGrab struct {
	StartByte int
	Grabbed   string
}

type FlushKind int

const (
	FlushNone FlushKind = iota
	FlushPaste
	FlushTyped
)

type FlushResult struct {
	Kind  FlushKind
	Text  string
	Typed rune
}

func PasteBurstRecommendedFlushDelay() time.Duration {
	return PasteBurstCharInterval + time.Millisecond
}

func PasteBurstRecommendedActiveFlushDelay() time.Duration {
	return PasteBurstActiveIdleTimeout + time.Millisecond
}

func (p *PasteBurst) OnPlainChar(ch rune, now time.Time) CharDecisionResult {
	p.notePlainChar(now)
	if p.active {
		p.extendWindow(now)
		return CharDecisionResult{Decision: CharDecisionBufferAppend}
	}
	if p.hasPendingFirstChar && now.Sub(*p.pendingFirstCharTime) <= PasteBurstCharInterval {
		held := p.pendingFirstChar
		p.clearPending()
		p.active = true
		p.buffer += string(held)
		p.extendWindow(now)
		return CharDecisionResult{Decision: CharDecisionBeginBufferFromPending}
	}
	if p.consecutivePlainChars >= PasteBurstMinChars {
		return CharDecisionResult{
			Decision:   CharDecisionBeginBuffer,
			RetroChars: max(p.consecutivePlainChars-1, 0),
		}
	}
	p.pendingFirstChar = ch
	p.pendingFirstCharTime = &now
	p.hasPendingFirstChar = true
	return CharDecisionResult{Decision: CharDecisionRetainFirstChar}
}

func (p *PasteBurst) OnPlainCharNoHold(now time.Time) (CharDecisionResult, bool) {
	p.notePlainChar(now)
	if p.active {
		p.extendWindow(now)
		return CharDecisionResult{Decision: CharDecisionBufferAppend}, true
	}
	if p.consecutivePlainChars >= PasteBurstMinChars {
		return CharDecisionResult{
			Decision:   CharDecisionBeginBuffer,
			RetroChars: max(p.consecutivePlainChars-1, 0),
		}, true
	}
	return CharDecisionResult{}, false
}

func (p *PasteBurst) FlushIfDue(now time.Time) FlushResult {
	timeout := PasteBurstCharInterval
	if p.isActiveInternal() {
		timeout = PasteBurstActiveIdleTimeout
	}
	timedOut := p.lastPlainCharTime != nil && now.Sub(*p.lastPlainCharTime) > timeout
	if timedOut && p.isActiveInternal() {
		p.active = false
		out := p.buffer
		p.buffer = ""
		return FlushResult{Kind: FlushPaste, Text: out}
	}
	if timedOut && p.hasPendingFirstChar {
		ch := p.pendingFirstChar
		p.clearPending()
		return FlushResult{Kind: FlushTyped, Typed: ch}
	}
	return FlushResult{Kind: FlushNone}
}

func (p *PasteBurst) AppendNewlineIfActive(now time.Time) bool {
	if !p.IsActive() {
		return false
	}
	p.buffer += "\n"
	p.extendWindow(now)
	return true
}

func (p *PasteBurst) NewlineShouldInsertInsteadOfSubmit(now time.Time) bool {
	inWindow := p.burstWindowUntil != nil && !now.After(*p.burstWindowUntil)
	return p.IsActive() || inWindow
}

func (p *PasteBurst) DirectInsertNewlineShouldInsert(now time.Time) bool {
	recentPlainChar := p.lastPlainCharTime != nil && now.Sub(*p.lastPlainCharTime) <= PasteBurstCharInterval
	return p.NewlineShouldInsertInsteadOfSubmit(now) || recentPlainChar
}

func (p *PasteBurst) ExtendWindow(now time.Time) {
	p.extendWindow(now)
}

func (p *PasteBurst) BeginWithRetroGrabbed(grabbed string, now time.Time) {
	p.buffer += grabbed
	p.active = true
	p.extendWindow(now)
}

func (p *PasteBurst) AppendCharToBuffer(ch rune, now time.Time) {
	p.buffer += string(ch)
	p.extendWindow(now)
}

func (p *PasteBurst) TryAppendCharIfActive(ch rune, now time.Time) bool {
	if !p.active && p.buffer == "" {
		return false
	}
	p.AppendCharToBuffer(ch, now)
	return true
}

func (p *PasteBurst) DecideBeginBuffer(now time.Time, before string, retroChars int) (RetroGrab, bool) {
	start := RetroStartIndex(before, retroChars)
	grabbed := before[start:]
	looksPastey := false
	if len([]rune(grabbed)) >= 16 {
		looksPastey = true
	}
	for _, r := range grabbed {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			looksPastey = true
			break
		}
	}
	if !looksPastey {
		return RetroGrab{}, false
	}
	p.BeginWithRetroGrabbed(grabbed, now)
	return RetroGrab{StartByte: start, Grabbed: grabbed}, true
}

func (p *PasteBurst) FlushBeforeModifiedInput() (string, bool) {
	if !p.IsActive() {
		return "", false
	}
	p.active = false
	out := p.buffer
	p.buffer = ""
	if p.hasPendingFirstChar {
		out += string(p.pendingFirstChar)
		p.clearPending()
	}
	return out, true
}

func (p *PasteBurst) ClearWindowAfterNonChar() {
	p.consecutivePlainChars = 0
	p.lastPlainCharTime = nil
	p.burstWindowUntil = nil
	p.active = false
	p.clearPending()
}

func (p *PasteBurst) IsActive() bool {
	return p.isActiveInternal() || p.hasPendingFirstChar
}

func (p *PasteBurst) ClearAfterExplicitPaste() {
	p.lastPlainCharTime = nil
	p.consecutivePlainChars = 0
	p.burstWindowUntil = nil
	p.active = false
	p.buffer = ""
	p.clearPending()
}

func (p *PasteBurst) notePlainChar(now time.Time) {
	if p.lastPlainCharTime != nil && now.Sub(*p.lastPlainCharTime) <= PasteBurstCharInterval {
		p.consecutivePlainChars++
	} else {
		p.consecutivePlainChars = 1
	}
	p.lastPlainCharTime = &now
}

func (p *PasteBurst) isActiveInternal() bool {
	return p.active || p.buffer != ""
}

func (p *PasteBurst) extendWindow(now time.Time) {
	until := now.Add(PasteEnterSuppressWindow)
	p.burstWindowUntil = &until
}

func (p *PasteBurst) clearPending() {
	p.pendingFirstChar = 0
	p.pendingFirstCharTime = nil
	p.hasPendingFirstChar = false
}

func RetroStartIndex(before string, retroChars int) int {
	if retroChars <= 0 {
		return len(before)
	}
	runes := []rune(before)
	if retroChars >= len(runes) {
		return 0
	}
	prefix := string(runes[:len(runes)-retroChars])
	return len(prefix)
}
