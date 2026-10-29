package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

// composerTips carries the startup announcement tip Rust prewarms before the
// first frame. Rust #47954 moved startup tips out of the fullscreen composer gap
// and into the transcript session header, so this state now only feeds the
// session header tip.
type composerTips struct {
	announcement string
}

func newComposerTips(announcement *string) composerTips {
	if announcement == nil {
		return composerTips{}
	}
	return composerTips{announcement: strings.TrimSpace(*announcement)}
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
	if m == nil || !msg.OK {
		return
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" || text == m.composerTips.announcement {
		return
	}
	m.composerTips.announcement = text
	m.replaceStartupTooltip(text)
}

// replaceStartupTooltip swaps the retained startup tooltip row in place. The
// announcement arrives asynchronously, but Rust renders the tip in the transcript
// session header, so a late announcement may only replace the row while the
// session is still at its first turn.
func (m *Model) replaceStartupTooltip(tip string) {
	if m == nil || m.State == nil {
		return
	}
	tipIndex := -1
	for i, message := range m.State.Messages {
		if message.StartupTip {
			tipIndex = i
			continue
		}
		if message.Role == codextui.RoleUser {
			return
		}
	}
	if tipIndex < 0 {
		return
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	lines := renderStartupTooltipLines(tip, width, m.keymapConfig, m.activeTUITheme(), m.sessionCWD)
	if len(lines) == 0 {
		return
	}
	replacement := historyMessageFromCell(historycell.NewPlainHistoryCell(lines), width)
	replacement.StartupTip = true
	m.State.Messages[tipIndex] = replacement
	m.refreshTranscript()
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
