package tea

import (
	"math/rand"
	"strconv"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/markdown"
)

// composerTipState keeps fullscreen composer-gap tip selection stable across
// redraws while varying it by session seed and user-turn count (Rust #46931).
type composerTipState struct {
	seed                  uint64
	announcement          string
	announcementDismissed bool
	bootTurnCount         int
	bootTurnSet           bool
	cacheKey              string
	line                  string
	found                 bool
	announcementCacheKey  string
	announcementLine      string
	announcementFound     bool
}

func newComposerTipState(seed uint64, announcement *string) composerTipState {
	state := composerTipState{seed: seed}
	if announcement != nil {
		state.announcement = strings.TrimSpace(*announcement)
	}
	return state
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
		if line, ok := renderComposerTip(tip, width, themeID, cwd); ok {
			c.line = line
			c.found = true
			return c.line, true
		}
	}
	return "", false
}

func (c *composerTipState) selectAnnouncement(width int, themeID string, cwd string) (string, bool) {
	if c == nil || c.announcement == "" || c.announcementDismissed || width <= 0 {
		return "", false
	}
	key := strings.Join([]string{strconv.Itoa(width), themeID, cwd, c.announcement}, "\x00")
	if key == c.announcementCacheKey {
		return c.announcementLine, c.announcementFound
	}
	c.announcementCacheKey = key
	c.announcementLine, c.announcementFound = renderComposerTip(c.announcement, width, themeID, cwd)
	return c.announcementLine, c.announcementFound
}

func renderComposerTip(tip string, width int, themeID string, cwd string) (string, bool) {
	rendered, err := markdown.RenderWithThemeCwd(tip, width, themeID, cwd)
	if err != nil {
		return "", false
	}
	lines := trimBlankDisplayEdges(rawLinesTrimmed(rendered))
	if len(lines) != 1 {
		return "", false
	}
	line := strings.TrimRight(lines[0], " ")
	if codextui.DisplayWidth(line) > width {
		return "", false
	}
	return line, true
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
	if !m.composerTips.bootTurnSet {
		m.composerTips.bootTurnCount = turnCount
		m.composerTips.bootTurnSet = true
	}
	if turnCount != m.composerTips.bootTurnCount || m.isUserTurnPendingOrRunning() {
		// Rust #46931: leaving startup permanently dismisses the announcement,
		// even if /clear or backtrack later removes the user turn.
		m.composerTips.announcementDismissed = true
	}
	width := max(m.width-2, 1)
	if line, ok := m.composerTips.selectAnnouncement(width, m.activeTUITheme(), m.sessionCWD); ok {
		return line, true
	}
	return m.composerTips.selectTip(turnCount, width, m.keymapConfig, m.activeTUITheme(), m.sessionCWD)
}

// OpenURLResultMsg reports an app-owned hyperlink open initiated by Ctrl-click.
type OpenURLResultMsg struct {
	URL string
	Err error
}

// StartupAnnouncementMsg carries the asynchronously fetched announcement tip.
type StartupAnnouncementMsg struct {
	Text string
	OK   bool
}

func (m *Model) startupAnnouncementCmd() bubbletea.Cmd {
	if m == nil || m.onFetchStartupAnnouncement == nil || m.composerTips.announcement != "" {
		return nil
	}
	fetch := m.onFetchStartupAnnouncement
	return func() bubbletea.Msg {
		text, ok := fetch()
		return StartupAnnouncementMsg{Text: text, OK: ok}
	}
}

func (m *Model) applyStartupAnnouncement(msg StartupAnnouncementMsg) {
	if m == nil || !msg.OK || m.composerTips.announcementDismissed {
		return
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" || text == m.composerTips.announcement {
		return
	}
	m.composerTips.announcement = text
	m.composerTips.announcementCacheKey = ""
}

func (m *Model) applyOpenURLResult(msg OpenURLResultMsg) {
	if m == nil || msg.Err == nil {
		return
	}
	m.notice = "Failed to open link: " + msg.Err.Error()
	m.refreshTranscript()
}

// handleComposerGapLinkClick maps a Ctrl-click on the composer-gap footer row
// to an OSC 8 destination. Bubble Tea v1 has no Super modifier, so Ctrl-click
// is the available carrier for Rust's Ctrl/Super-click behavior.
func (m *Model) handleComposerGapLinkClick(msg bubbletea.MouseMsg) (bool, bubbletea.Cmd) {
	if m == nil || m.onOpenURL == nil || !msg.Ctrl ||
		msg.Action != bubbletea.MouseActionPress || msg.Button != bubbletea.MouseButtonLeft {
		return false, nil
	}
	region := m.renderedSelection.regions[renderedRegionFooter]
	if region.h <= 0 || msg.Y < region.y || msg.Y >= region.y+region.h {
		return false, nil
	}
	row := msg.Y - region.y
	if row < 0 || row >= len(region.text) {
		return false, nil
	}
	column := msg.X - region.x
	for _, link := range codextui.OSC8LinksInLine(region.text[row]) {
		if column < link.Start || column >= link.End {
			continue
		}
		destination := link.Destination
		opener := m.onOpenURL
		return true, func() bubbletea.Msg {
			return OpenURLResultMsg{URL: destination, Err: opener(destination)}
		}
	}
	return false, nil
}
