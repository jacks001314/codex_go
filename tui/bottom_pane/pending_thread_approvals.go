package bottompane

import "codex_go/tui"

type PendingThreadApproval struct {
	ID      string
	Summary string
}

// PendingThreadApprovals mirrors Rust's widget for inactive threads with
// outstanding approval requests.
type PendingThreadApprovals struct {
	threads []string
}

func NewPendingThreadApprovals() *PendingThreadApprovals {
	return &PendingThreadApprovals{}
}

func (p *PendingThreadApprovals) SetThreads(threads []string) bool {
	if p == nil {
		return false
	}
	next := append([]string(nil), threads...)
	if stringSlicesEqual(p.threads, next) {
		return false
	}
	p.threads = next
	return true
}

func (p *PendingThreadApprovals) SetApprovals(approvals []PendingThreadApproval) bool {
	threads := make([]string, 0, len(approvals))
	for _, approval := range approvals {
		label := approval.Summary
		if label == "" {
			label = approval.ID
		}
		if label != "" {
			threads = append(threads, label)
		}
	}
	return p.SetThreads(threads)
}

func (p *PendingThreadApprovals) IsEmpty() bool {
	return p == nil || len(p.threads) == 0
}

func (p *PendingThreadApprovals) Threads() []string {
	if p == nil {
		return nil
	}
	return append([]string(nil), p.threads...)
}

func (p *PendingThreadApprovals) Rows(width int) []string {
	if p == nil || len(p.threads) == 0 || width < 4 {
		return nil
	}
	rows := []string{}
	limit := min(len(p.threads), 3)
	for _, thread := range p.threads[:limit] {
		lines := tui.AdaptiveWrapLine("Approval needed in "+thread, tui.WrapOptions{
			Width:            width,
			InitialIndent:    "  ! ",
			SubsequentIndent: "    ",
			BreakWords:       true,
		})
		rows = append(rows, lines...)
	}
	if len(p.threads) > 3 {
		rows = append(rows, "    ...")
	}
	rows = append(rows, "    /agent to switch threads")
	return rows
}

func (p *PendingThreadApprovals) DesiredHeight(width int) int {
	return len(p.Rows(width))
}

func stringSlicesEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
