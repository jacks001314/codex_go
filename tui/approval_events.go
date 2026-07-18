package tui

type ApprovalEvent struct {
	ID      string
	Command string
	Reason  string
}

func (e ApprovalEvent) HasReason() bool {
	return e.Reason != ""
}
