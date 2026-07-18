package onboarding

type AuthChoice string

const (
	AuthChoiceChatGPT    AuthChoice = "chatgpt"
	AuthChoiceDeviceCode AuthChoice = "device_code"
	AuthChoiceAPIKey     AuthChoice = "api_key"
)

type SignInState string

const (
	SignInPickMode                 SignInState = "pick_mode"
	SignInChatGPTContinueInBrowser SignInState = "chatgpt_continue_in_browser"
	SignInChatGPTDeviceCode        SignInState = "chatgpt_device_code"
	SignInChatGPTSuccessMessage    SignInState = "chatgpt_success_message"
	SignInChatGPTSuccess           SignInState = "chatgpt_success"
	SignInAPIKeyEntry              SignInState = "api_key_entry"
	SignInAPIKeyConfigured         SignInState = "api_key_configured"
)

type AuthOption struct {
	Choice      AuthChoice
	Label       string
	Description string
	Disabled    bool
}

func DefaultAuthOptions(apiKeyDisabled bool) []AuthOption {
	return []AuthOption{
		{Choice: AuthChoiceChatGPT, Label: "Sign in with ChatGPT", Description: "Use your ChatGPT plan with Codex."},
		{Choice: AuthChoiceDeviceCode, Label: "Sign in with a code", Description: "Use a one-time browser device code."},
		{Choice: AuthChoiceAPIKey, Label: "Use an API key", Description: "Configure an OpenAI API key.", Disabled: apiKeyDisabled},
	}
}

func AuthStepState(state SignInState) StepState {
	switch state {
	case SignInChatGPTSuccess, SignInAPIKeyConfigured:
		return StepComplete
	default:
		return StepInProgress
	}
}
