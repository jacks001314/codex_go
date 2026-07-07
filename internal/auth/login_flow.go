package auth

import (
	"fmt"
	"io"
	"strings"
)

const (
	LoginFlowChatGPTLoginDisabledMessage     = "ChatGPT login is disabled. Use API key login instead."
	LoginFlowAPIKeyLoginDisabledMessage      = "API key login is disabled. Use ChatGPT login instead."
	LoginFlowAccessTokenLoginDisabledMessage = "Access token login is disabled. Use API key login instead."
	LoginFlowSuccessMessage                  = "Successfully logged in"
)

type LoginFlowForcedMethod string

const (
	LoginFlowForcedChatGPT LoginFlowForcedMethod = "chatgpt"
	LoginFlowForcedAPI     LoginFlowForcedMethod = "api"
)

type LoginFlowMethod string

const (
	LoginFlowMethodChatGPT     LoginFlowMethod = "chatgpt"
	LoginFlowMethodAPIKey      LoginFlowMethod = "api_key"
	LoginFlowMethodAccessToken LoginFlowMethod = "access_token"
	LoginFlowMethodDeviceCode  LoginFlowMethod = "device_code"
)

func ValidateLoginFlowAllowed(method LoginFlowMethod, forced LoginFlowForcedMethod) error {
	switch {
	case method == LoginFlowMethodChatGPT && forced == LoginFlowForcedAPI:
		return fmt.Errorf(LoginFlowChatGPTLoginDisabledMessage)
	case method == LoginFlowMethodAPIKey && forced == LoginFlowForcedChatGPT:
		return fmt.Errorf(LoginFlowAPIKeyLoginDisabledMessage)
	case method == LoginFlowMethodAccessToken && forced == LoginFlowForcedAPI:
		return fmt.Errorf(LoginFlowAccessTokenLoginDisabledMessage)
	default:
		return nil
	}
}

func SafeFormatLoginKey(key string) string {
	if len(key) <= 13 {
		return "***"
	}
	return key[:8] + "***" + key[len(key)-5:]
}

func ReadLoginSecret(reader io.Reader) (string, error) {
	if reader == nil {
		return "", fmt.Errorf("reader is nil")
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", fmt.Errorf("no secret provided")
	}
	return secret, nil
}

func LoginFlowServerStartMessage(port uint16, authURL string) string {
	return fmt.Sprintf("Starting local login server on http://localhost:%d.\nIf your browser did not open, navigate to this URL to authenticate:\n\n%s\n\nOn a remote or headless machine? Use `codex login --device-auth` instead.", port, authURL)
}
