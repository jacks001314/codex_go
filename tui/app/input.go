package app

import (
	"strings"
	"unicode"
)

const (
	ExternalEditorHint                 = "Save and close external editor to continue."
	MissingExternalEditorMessage       = "Cannot open external editor: set $VISUAL or $EDITOR before starting Codex."
	SideEditPreviousUnavailableMessage = "Editing previous prompts is unavailable in side conversations."
	BacktrackNoSelection               = -1
)

type InputState struct {
	Draft string
}

type ThreadInputState struct {
	Draft string
}

type ExternalEditorState string

const (
	ExternalEditorClosed    ExternalEditorState = "closed"
	ExternalEditorRequested ExternalEditorState = "requested"
	ExternalEditorActive    ExternalEditorState = "active"
)

func CleanExternalEditorText(text string) string {
	return strings.TrimRightFunc(text, unicode.IsSpace)
}

func ExternalEditorErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return "Failed to open editor: " + err.Error()
}

func CanRequestExternalEditorLaunch(overlayActive bool, canLaunchExternalEditor bool, state ExternalEditorState) bool {
	return !overlayActive && canLaunchExternalEditor && state == ExternalEditorClosed
}

func AppKeymapShortcutsAvailable(overlayActive bool, modalOrPopupActive bool) bool {
	return !overlayActive && !modalOrPopupActive
}

func AllowAgentWordMotionFallback(enhancedKeysSupported bool, composerText string) bool {
	return !enhancedKeysSupported && composerText == ""
}

func AgentSwitchShortcutsAvailable(overlayActive bool, modalOrPopupActive bool, composerText string) bool {
	return !overlayActive && !modalOrPopupActive && composerText == ""
}

func ShouldHandleBacktrackEsc(sideConversationActive bool, normalBacktrackMode bool, composerEmpty bool, vimInsertEscape bool) bool {
	return !sideConversationActive && normalBacktrackMode && composerEmpty && !vimInsertEscape
}

func ShouldRejectSideBacktrackEsc(sideConversationActive bool, normalBacktrackMode bool, composerEmpty bool, vimInsertEscape bool) bool {
	return sideConversationActive && normalBacktrackMode && composerEmpty && !vimInsertEscape
}

func ShouldConfirmBacktrackFromMain(primed bool, nthUserMessage int, composerEmpty bool) bool {
	return primed && nthUserMessage != BacktrackNoSelection && composerEmpty
}

func ShouldResetPrimedBacktrackOnKeyPress(primed bool, key string) bool {
	return primed && strings.ToLower(key) != "esc"
}
