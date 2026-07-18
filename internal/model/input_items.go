package model

import "strings"

func UserMessageInputItem(text string) any {
	return MessageInputItem("user", text)
}

func DeveloperMessageInputItem(text string) any {
	return MessageInputItem("developer", text)
}

func MessageInputItem(role string, text string) any {
	text = strings.TrimSpace(text)
	role = strings.TrimSpace(role)
	if text == "" || role == "" {
		return nil
	}
	return map[string]any{
		"type": "message",
		"role": role,
		"content": []map[string]any{{
			"type": "input_text",
			"text": text,
		}},
	}
}
