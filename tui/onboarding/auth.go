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
		{Choice: AuthChoiceChatGPT, Label: "Sign in with ChatGPT", Description: "Usage included with Plus, Pro, Business, and Enterprise plans"},
		{Choice: AuthChoiceDeviceCode, Label: "Sign in with Device Code", Description: "Sign in from another device with a one-time code"},
		{Choice: AuthChoiceAPIKey, Label: "Provide your own API key", Description: "Pay for what you use", Disabled: apiKeyDisabled},
	}
}

func AuthOptionsForPolicy(chatGPTAllowed bool, apiKeyAllowed bool) []AuthOption {
	defaults := DefaultAuthOptions(!apiKeyAllowed)
	options := []AuthOption{{
		Choice:      defaults[0].Choice,
		Label:       defaults[0].Label,
		Description: defaults[0].Description,
		Disabled:    !chatGPTAllowed,
	}}
	if chatGPTAllowed {
		options = append(options, defaults[1])
	}
	if apiKeyAllowed {
		options = append(options, defaults[2])
	}
	return options
}

func AuthStepState(state SignInState) StepState {
	switch state {
	case SignInChatGPTSuccess, SignInAPIKeyConfigured:
		return StepComplete
	default:
		return StepInProgress
	}
}
