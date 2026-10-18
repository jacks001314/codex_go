package tea

import (
	"math/rand"
	"strconv"
	"strings"

	codextui "codex_go/tui"
	"codex_go/tui/markdown"
)

// composerTipState keeps fullscreen composer-gap tip selection stable across
// redraws while varying it by session seed and user-turn count (Rust #46931).
type composerTipState struct {
	seed     uint64
	cacheKey string
	line     string
	found    bool
}

func newComposerTipState(seed uint64) composerTipState {
	return composerTipState{seed: seed}
}

func (c *composerTipState) selectTip(turnCount int, width int, keymap *codextui.KeymapConfig, themeID string, cwd string) (string, bool) {
	if c == nil || width <= 0 {
		return "", false
	}
	key := strings.Join([]string{
		strconv.Itoa(turnCount),
		strconv.Itoa(width),
		themeID,
		cwd,
	}, "\x00")
	if key == c.cacheKey {
		return c.line, c.found
	}
	c.cacheKey = key
	c.line = ""
	c.found = false

	tips := codextui.ResolvedTooltips(keymap)
	if len(tips) == 0 {
		return "", false
	}
	rng := rand.New(rand.NewSource(int64(c.seed + uint64(turnCount))))
	rng.Shuffle(len(tips), func(i, j int) { tips[i], tips[j] = tips[j], tips[i] })
	for _, tip := range tips {
		rendered, err := markdown.RenderWithThemeCwd(tip, width, themeID, cwd)
		if err != nil {
			continue
		}
		lines := trimBlankDisplayEdges(rawLinesTrimmed(rendered))
		if len(lines) != 1 {
			continue
		}
		line := strings.TrimRight(lines[0], " ")
		if codextui.DisplayWidth(line) > width {
			continue
		}
		c.line = line
		c.found = true
		return c.line, true
	}
	return "", false
}

// composerTipLine mirrors Rust's fullscreen composer_hint fallback: a
// catalog tip owns the gap only while the transcript is following, the input is
// idle and empty, and no interaction surface has priority.
func (m *Model) composerTipLine() (string, bool) {
	if m == nil || m.State == nil || !m.fullscreenTranscript || m.rawOutput ||
		!m.showTooltips || !m.composerIsEmpty() || m.isUserTurnPendingOrRunning() ||
		m.backendBannerApplicable() {
		return "", false
	}
	if m.modal != nil || m.slashPopup.Active || m.skillPopup.Active || m.mentionPopup != nil {
		return "", false
	}
	if m.vimSearchMode || m.mainFind.active() || m.activityFocus.active || m.escBacktrackHint {
		return "", false
	}
	if m.renderedSelection.active || m.backtrack.Primed || m.backtrack.OverlayPreviewActive {
		return "", false
	}
	if !m.activityFollow && !m.transcript.AtBottom() {
		return "", false
	}
	turnCount := 0
	for _, message := range m.State.Messages {
		if message.Role == codextui.RoleUser && !message.StartupTip {
			turnCount++
		}
	}
	return m.composerTips.selectTip(turnCount, max(m.width-2, 1), m.keymapConfig, m.activeTUITheme(), m.sessionCWD)
}
