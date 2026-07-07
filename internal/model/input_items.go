package model

import "strings"

func UserMessageInputItem(text string) any {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return map[string]any{
		"type": "message",
		"role": "user",
		"content": []map[string]any{{
			"type": "input_text",
			"text": text,
		}},
	}
}
