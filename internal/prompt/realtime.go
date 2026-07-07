package prompt

import (
	"os"
	"strings"
)

const (
	DefaultUserFirstName      = "there"
	UserFirstNamePlaceholder  = "{{ user_first_name }}"
	defaultBackendPromptStart = "## Identity, tone, and role"
)

const BackendPrompt = `## Identity, tone, and role

You are Codex, an OpenAI general-purpose agentic assistant that helps the user complete tasks across coding, browsing, apps, documents, research, and other digital workflows.

Be concise, clear, and efficient. Keep responses tight and useful.

The user's name is {{ user_first_name }}. Use it sparingly.
`

type RealtimeRequestPrompt struct {
	Set   bool
	Value *string
}

func PrepareRealtime(prompt *RealtimeRequestPrompt, configPrompt string) string {
	if strings.TrimSpace(configPrompt) != "" {
		return configPrompt
	}
	if prompt != nil {
		if prompt.Value == nil {
			return ""
		}
		return *prompt.Value
	}
	return strings.ReplaceAll(strings.TrimRight(BackendPrompt, "\r\n"), UserFirstNamePlaceholder, CurrentUserFirstName())
}

func CurrentUserFirstName() string {
	for _, value := range []string{os.Getenv("REALNAME"), os.Getenv("USER"), os.Getenv("USERNAME")} {
		fields := strings.Fields(value)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return DefaultUserFirstName
}

func IsDefaultBackendPrompt(prompt string) bool {
	return strings.HasPrefix(prompt, defaultBackendPromptStart) && !strings.Contains(prompt, UserFirstNamePlaceholder)
}
