package chatcomposer

// Rust parity subset: codex-rs/tui/src/bottom_pane/chat_composer/popup_state.rs.

type ActivePopupKind string

const (
	ActivePopupNone      ActivePopupKind = "none"
	ActivePopupCommand   ActivePopupKind = "command"
	ActivePopupFile      ActivePopupKind = "file"
	ActivePopupSkill     ActivePopupKind = "skill"
	ActivePopupMentionV2 ActivePopupKind = "mention_v2"
)

type PopupState struct {
	Open                  bool
	Active                ActivePopupKind
	DismissedFileToken    string
	CurrentFileQuery      string
	DismissedMentionToken string
}

func NewPopupState() PopupState {
	return PopupState{Active: ActivePopupNone}
}

func (p PopupState) ActivePopup() bool {
	return p.Open && p.Active != "" && p.Active != ActivePopupNone
}

func (p *PopupState) SetActive(kind ActivePopupKind) {
	if p == nil {
		return
	}
	p.Active = kind
	p.Open = kind != ActivePopupNone && kind != ""
}

func (p *PopupState) OpenCommand() {
	p.SetActive(ActivePopupCommand)
}

func (p *PopupState) OpenFile(query string) {
	if p == nil {
		return
	}
	p.CurrentFileQuery = query
	p.SetActive(ActivePopupFile)
}

func (p *PopupState) OpenSkill() {
	p.SetActive(ActivePopupSkill)
}

func (p *PopupState) OpenMentionV2() {
	p.SetActive(ActivePopupMentionV2)
}

func (p *PopupState) Clear() {
	if p == nil {
		return
	}
	p.Open = false
	p.Active = ActivePopupNone
	p.CurrentFileQuery = ""
}

func (p *PopupState) DismissFileToken(token string) {
	if p != nil {
		p.DismissedFileToken = token
		p.Clear()
	}
}

func (p PopupState) ShouldShowFilePopup(token string, query string) bool {
	return token != "" && token != p.DismissedFileToken && query != p.CurrentFileQuery
}

func (p *PopupState) DismissMentionToken(token string) {
	if p != nil {
		p.DismissedMentionToken = token
		p.Clear()
	}
}

func (p PopupState) ShouldShowMentionPopup(token string) bool {
	return token != "" && token != p.DismissedMentionToken
}
