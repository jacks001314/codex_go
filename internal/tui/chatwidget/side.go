package chatwidget

type SideConversationState struct {
	Active            bool
	NormalPlaceholder string
	SidePlaceholder   string
	ContextLabel      string
}

func NewSideConversationState(normalPlaceholder string, sidePlaceholder string) SideConversationState {
	return SideConversationState{
		NormalPlaceholder: normalPlaceholder,
		SidePlaceholder:   sidePlaceholder,
	}
}

func (s *SideConversationState) SetActive(active bool) {
	if s == nil {
		return
	}
	s.Active = active
}

func (s SideConversationState) Placeholder() string {
	if s.Active {
		return s.SidePlaceholder
	}
	return s.NormalPlaceholder
}

func (s *SideConversationState) SetContextLabel(label string) {
	if s == nil {
		return
	}
	s.ContextLabel = label
}

func SubmitPlainUserTurnShellEscapePolicy() ShellEscapePolicy {
	return ShellEscapeDisallow
}
