package chatwidget

import "strings"

type ChatSessionHeader struct {
	Model string
}

func NewChatSessionHeader(model string) ChatSessionHeader {
	return ChatSessionHeader{Model: strings.TrimSpace(model)}
}

func (h *ChatSessionHeader) SetModel(model string) bool {
	if h == nil {
		return false
	}
	model = strings.TrimSpace(model)
	if h.Model == model {
		return false
	}
	h.Model = model
	return true
}
