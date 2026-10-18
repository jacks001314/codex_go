package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProviderAuthRecoveryMessagesForBedrockLikeRust(t *testing.T) {
	refresh := ProviderInfo{
		Name: AmazonBedrockProviderName,
		AWS:  &ProviderAWSAuthInfo{AuthRefresh: &ProviderAuthRefreshInfo{Command: "aws"}},
	}
	for name, info := range map[string]ProviderInfo{
		"auth refresh": refresh,
		"credential export": {
			Name: AmazonBedrockRuntimeProviderName,
			AWS:  &ProviderAWSAuthInfo{CredentialExport: &ProviderCredentialExportInfo{Command: "exporter"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			messages := ProviderAuthRecoveryMessagesForInfo(info)
			if messages == nil ||
				messages.Started != "AWS session has expired. Reauthenticating..." ||
				messages.Succeeded != "Signed in with AWS." {
				t.Fatalf("messages = %#v", messages)
			}
		})
	}
	for name, info := range map[string]ProviderInfo{
		"openai":             {Name: OpenAIProviderName},
		"bedrock no recover": {Name: AmazonBedrockProviderName, AWS: &ProviderAWSAuthInfo{}},
	} {
		t.Run(name, func(t *testing.T) {
			if messages := ProviderAuthRecoveryMessagesForInfo(info); messages != nil {
				t.Fatalf("messages = %#v, want nil", messages)
			}
		})
	}
}

func TestResponsesAgentRunnerEmitsProviderAuthRecoveryProgressLikeRust(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		t.Setenv("GO_WANT_AWS_CREDENTIAL_EXPORT_HELPER", "1")
		t.Setenv("AWS_CREDENTIAL_EXPORT_HELPER_COUNTER", filepath.Join(t.TempDir(), "counter"))
		var events []ProviderAuthRecoveryEvent
		runner := &ResponsesAgentRunner{
			Provider: &APIProvider{Name: AmazonBedrockProviderName},
			AWS: &ProviderAWSAuthInfo{
				Region: "us-east-1",
				CredentialExport: &ProviderCredentialExportInfo{
					Command: os.Args[0],
					Args:    []string{"-test.run=TestAWSCredentialExportHelper"},
				},
			},
			AuthRecoveryHandler: func(event ProviderAuthRecoveryEvent) {
				events = append(events, event)
			},
		}
		if _, _, err := runner.recoverUnauthorizedAuth(context.Background(), &authRecoveryState{}, authRecoveryDebug{}); err != nil {
			t.Fatalf("recoverUnauthorizedAuth() error = %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("events = %#v, want started and completed", events)
		}
		if events[0] != (ProviderAuthRecoveryEvent{
			Phase: ProviderAuthRecoveryPhaseStarted, Provider: AmazonBedrockProviderName,
			Message: "AWS session has expired. Reauthenticating...",
		}) {
			t.Fatalf("started event = %#v", events[0])
		}
		if events[1] != (ProviderAuthRecoveryEvent{
			Phase: ProviderAuthRecoveryPhaseCompleted, Provider: AmazonBedrockProviderName,
			Message: "Signed in with AWS.",
		}) {
			t.Fatalf("completed event = %#v", events[1])
		}
	})

	t.Run("failure reports only started", func(t *testing.T) {
		var events []ProviderAuthRecoveryEvent
		runner := &ResponsesAgentRunner{
			Provider: &APIProvider{Name: AmazonBedrockProviderName},
			AWS: &ProviderAWSAuthInfo{
				AuthRefresh: &ProviderAuthRefreshInfo{Command: ""},
			},
			AuthRecoveryHandler: func(event ProviderAuthRecoveryEvent) {
				events = append(events, event)
			},
		}
		_, _, err := runner.recoverUnauthorizedAuth(context.Background(), &authRecoveryState{}, authRecoveryDebug{})
		if !errors.Is(err, errNoAuthRecoveryStep) {
			t.Fatalf("recoverUnauthorizedAuth() error = %v, want errNoAuthRecoveryStep", err)
		}
		if len(events) != 1 || events[0].Phase != ProviderAuthRecoveryPhaseStarted {
			t.Fatalf("events = %#v, want only started", events)
		}
	})
}
