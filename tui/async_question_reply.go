package tui

import (
	"encoding/json"
	"strings"

	"codex_go/turn"
)

// AsyncQuestionReply is one desktop async-question reply envelope entry
// (Rust #46486 tui::async_question_reply::AsyncQuestionReply).
type AsyncQuestionReply struct {
	QuestionItemID string `json:"questionItemId"`
	Question       string `json:"question"`
	Answer         string `json:"answer"`
}

const (
	asyncQuestionReplyOpenMarker  = "<send_user_message_question_reply>"
	asyncQuestionReplyCloseMarker = "</send_user_message_question_reply>"
	ideContextPrefix              = "# Context from my IDE setup:\n"
	ideContextRequestDelimiter    = "\n## My request for Codex:\n"
)

// ParseAsyncQuestionReplies mirrors Rust's tui::async_question_reply::parse:
// only a complete envelope, optionally following the standard IDE context
// prefix, is interpreted, and it carries either one reply or a non-empty batch
// of them. It returns nil when the text is ordinary user input.
func ParseAsyncQuestionReplies(text string) []AsyncQuestionReply {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, ideContextPrefix) {
		index := strings.LastIndex(trimmed, ideContextRequestDelimiter)
		if index < 0 {
			return nil
		}
		trimmed = strings.TrimSpace(trimmed[index+len(ideContextRequestDelimiter):])
	}
	inner, ok := strings.CutPrefix(trimmed, asyncQuestionReplyOpenMarker)
	if !ok {
		return nil
	}
	inner, ok = strings.CutSuffix(inner, asyncQuestionReplyCloseMarker)
	if !ok {
		return nil
	}
	payload := strings.TrimSpace(inner)
	if payload == "" {
		return nil
	}
	replies, ok := decodeAsyncQuestionReplies(payload)
	if !ok || len(replies) == 0 {
		return nil
	}
	return replies
}

// decodeAsyncQuestionReplies mirrors Rust's untagged Replies enum: the payload
// is either a batch or a single reply, and every entry must be a complete
// envelope (unknown fields are ignored, missing or non-object entries are not).
func decodeAsyncQuestionReplies(payload string) ([]AsyncQuestionReply, bool) {
	if strings.HasPrefix(payload, "[") {
		var raw []json.RawMessage
		if err := json.Unmarshal([]byte(payload), &raw); err != nil {
			return nil, false
		}
		replies := make([]AsyncQuestionReply, 0, len(raw))
		for _, entry := range raw {
			reply, ok := decodeAsyncQuestionReply(entry)
			if !ok {
				return nil, false
			}
			replies = append(replies, reply)
		}
		return replies, true
	}
	reply, ok := decodeAsyncQuestionReply(json.RawMessage(payload))
	if !ok {
		return nil, false
	}
	return []AsyncQuestionReply{reply}, true
}

func decodeAsyncQuestionReply(raw json.RawMessage) (AsyncQuestionReply, bool) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return AsyncQuestionReply{}, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return AsyncQuestionReply{}, false
	}
	reply := AsyncQuestionReply{}
	for key, target := range map[string]*string{
		"questionItemId": &reply.QuestionItemID,
		"question":       &reply.Question,
		"answer":         &reply.Answer,
	} {
		value, ok := fields[key]
		if !ok {
			return AsyncQuestionReply{}, false
		}
		if err := json.Unmarshal(value, target); err != nil {
			return AsyncQuestionReply{}, false
		}
	}
	return reply, true
}

// AsyncQuestionReplyDisplayText mirrors Rust's display_text: the envelope
// renders as readable question-and-answer text, or the text is left alone when
// it is not an envelope.
func AsyncQuestionReplyDisplayText(text string) (string, bool) {
	replies := ParseAsyncQuestionReplies(text)
	if len(replies) == 0 {
		return "", false
	}
	rendered := make([]string, 0, len(replies))
	for _, reply := range replies {
		rendered = append(rendered, "> "+reply.Question+"\n\n"+reply.Answer)
	}
	return strings.Join(rendered, "\n\n"), true
}

// ParseAsyncQuestionReplyInput mirrors Rust's parse_input: a message is an
// envelope reply only when its non-skill, non-mention content is exactly one
// text item.
func ParseAsyncQuestionReplyInput(inputs []turn.TurnUserInput) []AsyncQuestionReply {
	text := ""
	found := false
	for _, input := range inputs {
		switch strings.ToLower(strings.TrimSpace(input.Type)) {
		case "skill", "mention":
			continue
		case "", "text":
			if found {
				return nil
			}
			found = true
			text = input.Text
		default:
			return nil
		}
	}
	if !found {
		return nil
	}
	return ParseAsyncQuestionReplies(text)
}
