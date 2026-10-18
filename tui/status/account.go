package status

import "strings"

type AccountMode string

const (
	AccountModeUnknown AccountMode = ""
	AccountModeChatGPT AccountMode = "chatgpt"
	AccountModeAPIKey  AccountMode = "api-key"
)

type AccountStatus struct {
	SignedIn bool
	Email    string
	Plan     string
	Mode     AccountMode
}

func ChatGPTAccountStatus(email string, plan string) AccountStatus {
	return AccountStatus{
		SignedIn: true,
		Email:    email,
		Plan:     plan,
		Mode:     AccountModeChatGPT,
	}
}

func APIKeyAccountStatus() AccountStatus {
	return AccountStatus{
		SignedIn: true,
		Mode:     AccountModeAPIKey,
	}
}

func (s AccountStatus) DisplayValue() (string, bool) {
	switch s.displayMode() {
	case AccountModeChatGPT:
		switch {
		case s.Email != "" && s.Plan != "":
			return s.Email + " (" + s.Plan + ")", true
		case s.Email != "":
			return s.Email, true
		case s.Plan != "":
			return s.Plan, true
		default:
			return "ChatGPT", true
		}
	case AccountModeAPIKey:
		return "API key configured (run codex login to use ChatGPT)", true
	default:
		return "", false
	}
}

func (s AccountStatus) IsChatGPT() bool {
	return s.displayMode() == AccountModeChatGPT
}

func (s AccountStatus) displayMode() AccountMode {
	if s.Mode != AccountModeUnknown {
		return s.Mode
	}
	if s.Email != "" || s.Plan != "" {
		return AccountModeChatGPT
	}
	if s.SignedIn {
		return AccountModeChatGPT
	}
	return AccountModeUnknown
}

// AccountDisplayWithEmail restores the email omitted by account/update while
// preserving the current ChatGPT plan label (Rust #50199).
func AccountDisplayWithEmail(current string, email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return strings.TrimSpace(current)
	}
	current = strings.TrimSpace(current)
	switch {
	case current == "", current == "ChatGPT":
		return email
	case strings.HasPrefix(current, "API key configured"):
		return email
	case !strings.Contains(current, "@"):
		return email + " (" + current + ")"
	}
	planStart := strings.Index(current, " (")
	if planStart < 0 {
		return email
	}
	plan := strings.TrimSuffix(current[planStart+2:], ")")
	if plan = strings.TrimSpace(plan); plan == "" {
		return email
	}
	return email + " (" + plan + ")"
}
