package app

import (
	"strings"

	"codex_go/appserver"
	chatwidget "codex_go/tui/chatwidget"
)

// interactiveLocalListCollaborationModes mirrors the embedded app server's
// collaborationMode/list handler (appserver.RuntimeRouter.handleCollaborationModeList)
// for Rust #42401 (d4dc882998): the TUI discovers its collaboration-mode
// catalog from the app server and treats that catalog as authoritative.
func interactiveLocalListCollaborationModes() func() ([]chatwidget.CollaborationModeMask, error) {
	return func() ([]chatwidget.CollaborationModeMask, error) {
		response := appserver.NewCollaborationModeService(nil).List(&appserver.CollaborationModeListParams{})
		return collaborationMasksFromAppServer(response.Data), nil
	}
}

// collaborationMasksFromAppServer converts the wire catalog into the TUI masks.
// The RPC omits prompts and developer instructions, so masks arrive without an
// instruction override; the server supplies the mode's prompt on selection.
func collaborationMasksFromAppServer(items []appserver.CollaborationModeMask) []chatwidget.CollaborationModeMask {
	masks := make([]chatwidget.CollaborationModeMask, 0, len(items))
	for _, item := range items {
		mask := chatwidget.CollaborationModeMask{Name: item.Name}
		if strings.TrimSpace(item.Mode) != "" {
			kind := chatwidget.NormalizeCollaborationModeKind(item.Mode)
			mask.Mode = &kind
		}
		if item.Model != nil && strings.TrimSpace(*item.Model) != "" {
			model := strings.TrimSpace(*item.Model)
			mask.Model = &model
		}
		if item.ReasoningEffort != nil && strings.TrimSpace(*item.ReasoningEffort) != "" {
			mask.ReasoningEffort = chatwidget.CollaborationValue(*item.ReasoningEffort)
		}
		masks = append(masks, mask)
	}
	return masks
}
