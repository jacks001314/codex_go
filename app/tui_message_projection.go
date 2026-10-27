package app

import (
	"strings"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// tuiUserMessageFromPrompt projects a persisted or live user prompt onto the
// shared transcript model. Desktop async-question replies keep their structured
// question/answer pairs so the renderer can shorten only the repeated question
// and render the answer as Markdown (Rust #57292).
func tuiUserMessageFromPrompt(text string, localImages []string, remoteImages []string) codextui.Message {
	display := text
	replies := codextui.ParseAsyncQuestionReplies(text)
	if len(replies) > 0 {
		if readable, ok := codextui.AsyncQuestionReplyDisplayText(text); ok {
			display = readable
		}
	}
	return codextui.Message{
		Role:                   codextui.RoleUser,
		Text:                   display,
		RawText:                display,
		UserPrompt:             display,
		UserPromptLocalImages:  append([]string(nil), localImages...),
		UserPromptRemoteImages: append([]string(nil), remoteImages...),
		AsyncQuestionReplies:   replies,
	}
}

// tuiMessageAsyncQuestions projects structured async question metadata onto a
// cached transcript message.
func tuiMessageAsyncQuestions(value any) []codextui.MessageAsyncQuestion {
	questions := bottompane.ParseAsyncUserInputQuestions(value)
	if len(questions) == 0 {
		return nil
	}
	out := make([]codextui.MessageAsyncQuestion, 0, len(questions))
	for _, question := range questions {
		out = append(out, codextui.MessageAsyncQuestion{
			Title:   question.Title,
			Options: append([]string(nil), question.Options...),
		})
	}
	return out
}

// hasVisibleTUIUserMessageText matches the existing empty-prompt guard while
// allowing a reply envelope whose decoded display text is present.
func hasVisibleTUIUserMessageText(text string) bool {
	return strings.TrimSpace(text) != ""
}
