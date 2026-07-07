package session

import "strings"

const DefaultSessionPrefixLength = 8

func PrefixForSessionID(sessionID string) string {
	value := strings.TrimSpace(sessionID)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "thread-")
	value = strings.TrimPrefix(value, "session-")
	if len(value) <= DefaultSessionPrefixLength {
		return value
	}
	return value[:DefaultSessionPrefixLength]
}
