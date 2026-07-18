package status

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
