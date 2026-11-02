package onboarding

type AuthChoice string

const (
	AuthChoiceChatGPT    AuthChoice = "chatgpt"
	AuthChoiceDeviceCode AuthChoice = "device_code"
	// AuthChoiceOtherMethods is the submenu entry that groups the API key,
	// Amazon Bedrock and Google Cloud Vertex AI sign-in paths (Rust #58427).
	AuthChoiceOtherMethods AuthChoice = "other_methods"
	AuthChoiceAPIKey       AuthChoice = "api_key"
	AuthChoiceBedrock      AuthChoice = "bedrock"
	AuthChoiceGCP          AuthChoice = "gcp"
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
		{Choice: AuthChoiceAPIKey, Label: "Use an OpenAI API key", Description: "Pay for what you use", Disabled: apiKeyDisabled},
	}
}

func AuthOptionsForPolicy(chatGPTAllowed bool, apiKeyAllowed bool) []AuthOption {
	return AuthOptionsForPolicyWithBedrock(chatGPTAllowed, apiKeyAllowed, false)
}

func AuthOptionsForPolicyWithBedrock(chatGPTAllowed bool, apiKeyAllowed bool, bedrockAllowed bool) []AuthOption {
	return AuthOptionsForPolicyWithProviders(chatGPTAllowed, apiKeyAllowed, bedrockAllowed, false)
}

// AuthOptionsForPolicyWithProviders builds the provider sign-in menu. The API
// key, Amazon Bedrock and Google Cloud Vertex AI paths live behind the
// "Sign in another way" submenu, while ChatGPT and device code stay on the
// main menu (Rust #58427, #57857).
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
		options = append(options, AuthOption{
			Choice:      AuthChoiceOtherMethods,
			Label:       "Sign in another way",
			Description: alternativeSignInDescription(bedrockAllowed, gcpAllowed),
		})
	}
	return options
}

// AlternativeAuthOptions lists the methods grouped behind "Sign in another
// way": the OpenAI API key, then the provider setup wizards the workspace
// exposes (Rust #58427 displayed_sign_in_options).
func AlternativeAuthOptions(bedrockAllowed bool, gcpAllowed bool) []AuthOption {
	defaults := DefaultAuthOptions(false)
	options := []AuthOption{defaults[2]}
	if bedrockAllowed {
		options = append(options, AuthOption{
			Choice:      AuthChoiceBedrock,
			Label:       "Use Amazon Bedrock",
			Description: "Connect using your AWS credentials",
		})
	}
	if gcpAllowed {
		options = append(options, AuthOption{
			Choice:      AuthChoiceGCP,
			Label:       "Use Google Cloud Vertex AI",
			Description: "Connect using your Google Cloud credentials",
		})
	}
	return options
}

func alternativeSignInDescription(bedrockAllowed bool, gcpAllowed bool) string {
	if bedrockAllowed || gcpAllowed {
		return "Use an API key or another provider"
	}
	return "Use an OpenAI API key"
}

func AuthStepState(state SignInState) StepState {
	switch state {
	case SignInChatGPTSuccess, SignInAPIKeyConfigured, SignInBedrockConfigured, SignInGCPConfigured:
		return StepComplete
	default:
		return StepInProgress
	}
}
