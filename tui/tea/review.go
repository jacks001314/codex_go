package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/review"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
)

func (m *Model) startReview(target chatwidget.ReviewTarget) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if m.onStartReview == nil {
		m.applyHistoryCell(historycell.NewPlainHistoryCell([]string{"/review", "", "Review start requires app-server review/start.", reviewTargetSummary(target)}))
		m.notice = "Review"
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		m.applyHistoryCell(historycell.NewErrorEvent("'/review' is unavailable before the session starts."))
		m.notice = "Review unavailable"
		return nil
	}
	delivery := "inline"
	params := review.StartParams{
		ThreadID: threadID,
		Target:   reviewAPITarget(target),
		Delivery: &delivery,
	}
	starter := m.onStartReview
	m.addBottomLine("Review starting: " + reviewTargetSummary(target))
	m.notice = "Review"
	return func() bubbletea.Msg {
		response, err := starter(params)
		return ReviewStartResultMsg{Target: target, Response: response, Err: err}
	}
}

func (m *Model) applyReviewBranchPickerCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	reader := m.onReadReviewBranches
	if reader == nil {
		reader = localReviewBranches
	}
	cwd := m.reviewCWD()
	m.openSelectionViewModal(ModalKindReview, reviewLoadingView("Loading branches..."))
	return func() bubbletea.Msg {
		currentBranch, branches, err := reader(cwd)
		return ReviewBranchesResultMsg{CurrentBranch: currentBranch, Branches: branches, Err: err}
	}
}

func (m *Model) applyReviewCommitPickerCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	reader := m.onReadReviewCommits
	if reader == nil {
		reader = localReviewCommits
	}
	cwd := m.reviewCWD()
	m.openSelectionViewModal(ModalKindReview, reviewLoadingView("Loading commits..."))
	return func() bubbletea.Msg {
		entries, err := reader(cwd, 100)
		return ReviewCommitsResultMsg{Entries: entries, Err: err}
	}
}

func (m *Model) applyReviewStartResult(message ReviewStartResultMsg) {
	if m == nil {
		return
	}
	if message.Err != nil {
		m.applyHistoryCell(historycell.NewErrorEvent("Review: " + strings.TrimSpace(message.Err.Error())))
		m.notice = "Review failed"
		return
	}
	lines := []string{"/review", "", "Review started.", reviewTargetSummary(message.Target)}
	if turnID := strings.TrimSpace(message.Response.Turn.ID); turnID != "" {
		lines = append(lines, "turn: "+turnID)
	}
	if reviewThreadID := strings.TrimSpace(message.Response.ReviewThreadID); reviewThreadID != "" {
		lines = append(lines, "review thread: "+reviewThreadID)
	}
	m.applyHistoryCell(historycell.NewPlainHistoryCell(lines))
	m.notice = "Review"
}

func (m *Model) applyReviewBranchesResult(message ReviewBranchesResultMsg) {
	if m == nil {
		return
	}
	if message.Err != nil {
		m.openSelectionViewModal(ModalKindReview, reviewErrorView("Review branches: "+strings.TrimSpace(message.Err.Error())))
		return
	}
	m.openSelectionViewModal(ModalKindReview, chatwidget.NewReviewBranchPickerView(message.CurrentBranch, message.Branches))
}

func (m *Model) applyReviewCommitsResult(message ReviewCommitsResultMsg) {
	if m == nil {
		return
	}
	if message.Err != nil {
		m.openSelectionViewModal(ModalKindReview, reviewErrorView("Review commits: "+strings.TrimSpace(message.Err.Error())))
		return
	}
	m.openSelectionViewModal(ModalKindReview, chatwidget.NewReviewCommitPickerView(message.Entries))
}

func reviewAPITarget(target chatwidget.ReviewTarget) review.APITarget {
	switch target.Kind {
	case chatwidget.ReviewTargetBaseBranch:
		return review.APITarget{Type: "baseBranch", Branch: target.Branch}
	case chatwidget.ReviewTargetCommit:
		return review.APITarget{Type: "commit", SHA: target.SHA, Title: stringPtrReviewExact(target.Title)}
	case chatwidget.ReviewTargetCustom:
		return review.APITarget{Type: "custom", Instructions: strings.TrimSpace(target.Instructions)}
	default:
		return review.APITarget{Type: "uncommittedChanges"}
	}
}

func (m *Model) reviewCWD() string {
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m.sessionCWD)
}

func localReviewBranches(cwd string) (string, []string, error) {
	return review.LocalBranches(cwd)
}

func localReviewCommits(cwd string, limit int) ([]chatwidget.ReviewCommitEntry, error) {
	entries, err := review.RecentCommits(cwd, limit)
	if err != nil {
		return nil, err
	}
	out := make([]chatwidget.ReviewCommitEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, chatwidget.ReviewCommitEntry{Subject: entry.Subject, SHA: entry.SHA})
	}
	return out, nil
}

func reviewLoadingView(subtitle string) chatwidget.SelectionView {
	return chatwidget.SelectionView{
		ViewID:      chatwidget.ReviewPopupViewID,
		Title:       "Review",
		Subtitle:    subtitle,
		AllowCancel: true,
		Items: []chatwidget.SelectionItem{{
			Name:     "Loading...",
			Disabled: true,
		}},
	}
}

func reviewErrorView(message string) chatwidget.SelectionView {
	return chatwidget.SelectionView{
		ViewID:      chatwidget.ReviewPopupViewID,
		Title:       "Review",
		Subtitle:    strings.TrimSpace(message),
		AllowCancel: true,
		Items: []chatwidget.SelectionItem{{
			Name:            "Close",
			DismissOnSelect: true,
		}},
	}
}

func reviewTargetSummary(target chatwidget.ReviewTarget) string {
	switch target.Kind {
	case chatwidget.ReviewTargetBaseBranch:
		return "target: base branch " + strings.TrimSpace(target.Branch)
	case chatwidget.ReviewTargetCommit:
		if title := strings.TrimSpace(target.Title); title != "" {
			return "target: commit " + strings.TrimSpace(target.SHA) + " (" + title + ")"
		}
		return "target: commit " + strings.TrimSpace(target.SHA)
	case chatwidget.ReviewTargetCustom:
		return "target: custom instructions"
	default:
		return "target: uncommitted changes"
	}
}

func stringPtrReviewExact(value string) *string {
	return &value
}
