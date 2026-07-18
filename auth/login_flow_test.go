package auth

import (
	"strings"
	"testing"
)

func TestValidateLoginFlowAllowed(t *testing.T) {
	if err := ValidateLoginFlowAllowed(LoginFlowMethodChatGPT, LoginFlowForcedAPI); err == nil || err.Error() != LoginFlowChatGPTLoginDisabledMessage {
		t.Fatalf("unexpected chatgpt validation: %v", err)
	}
	if err := ValidateLoginFlowAllowed(LoginFlowMethodAPIKey, LoginFlowForcedChatGPT); err == nil || err.Error() != LoginFlowAPIKeyLoginDisabledMessage {
		t.Fatalf("unexpected api validation: %v", err)
	}
	if err := ValidateLoginFlowAllowed(LoginFlowMethodDeviceCode, ""); err != nil {
		t.Fatalf("device code should be allowed: %v", err)
	}
}

func TestSafeFormatLoginKey(t *testing.T) {
	if SafeFormatLoginKey("sk-proj-1234567890ABCDE") != "sk-proj-***ABCDE" {
		t.Fatalf("unexpected formatted key")
	}
	if SafeFormatLoginKey("sk-proj-12345") != "***" {
		t.Fatalf("short key should be hidden")
	}
}

func TestReadLoginSecret(t *testing.T) {
	secret, err := ReadLoginSecret(strings.NewReader(" token \n"))
	if err != nil || secret != "token" {
		t.Fatalf("ReadLoginSecret() = %q, %v", secret, err)
	}
	if _, err := ReadLoginSecret(strings.NewReader(" \n")); err == nil {
		t.Fatalf("expected empty secret error")
	}
}
