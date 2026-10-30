package onboarding

type AuthChoice string

const (
	AuthChoiceChatGPT    AuthChoice = "chatgpt"
	AuthChoiceDeviceCode AuthChoice = "device_code"
	AuthChoiceAPIKey     AuthChoice = "api_key"
	AuthChoiceBedrock    AuthChoice = "bedrock"
	AuthChoiceGCP        AuthChoice = "gcp"
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
	SignInBedrock                  SignInState = "bedrock"
	SignInBedrockConfigured        SignInState = "bedrock_configured"
	SignInGCP                      SignInState = "gcp"
	SignInGCPConfigured            SignInState = "gcp_configured"
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
	return AuthOptionsForPolicyWithBedrock(chatGPTAllowed, apiKeyAllowed, false)
}

func AuthOptionsForPolicyWithBedrock(chatGPTAllowed bool, apiKeyAllowed bool, bedrockAllowed bool) []AuthOption {
	return AuthOptionsForPolicyWithProviders(chatGPTAllowed, apiKeyAllowed, bedrockAllowed, false)
}

// AuthOptionsForPolicyWithProviders builds the provider sign-in menu, offering
// the Amazon Bedrock and Google Cloud Vertex AI setup wizards only when API
// login and the matching feature are allowed (Rust #57857).
func AuthOptionsForPolicyWithProviders(chatGPTAllowed bool, apiKeyAllowed bool, bedrockAllowed bool, gcpAllowed bool) []AuthOption {
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
	if apiKeyAllowed && bedrockAllowed {
		options = append(options, AuthOption{
			Choice:      AuthChoiceBedrock,
			Label:       "Set up Amazon Bedrock",
			Description: "Use an AWS profile, access keys, environment credentials, or a Bedrock API key",
		})
	}
	if apiKeyAllowed && gcpAllowed {
		options = append(options, AuthOption{
			Choice:      AuthChoiceGCP,
			Label:       "Use Google Cloud Vertex AI",
			Description: "Connect using your Google Cloud credentials",
		})
	}
	return options
}

func AuthStepState(state SignInState) StepState {
	switch state {
	case SignInChatGPTSuccess, SignInAPIKeyConfigured, SignInBedrockConfigured, SignInGCPConfigured:
		return StepComplete
	default:
		return StepInProgress
	}
}
