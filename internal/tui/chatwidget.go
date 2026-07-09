package tui

type ChatWidgetRuntimeSurface struct {
	Transcript bool
	Composer   bool
	Status     bool
}

func DefaultChatWidgetRuntimeSurface() ChatWidgetRuntimeSurface {
	return ChatWidgetRuntimeSurface{Transcript: true, Composer: true, Status: true}
}
