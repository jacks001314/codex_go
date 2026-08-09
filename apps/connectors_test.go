package apps

import "testing"

func TestConnectorInstallURLUsesConfiguredChatGPTOrigin(t *testing.T) {
	t.Setenv("CODEX_APP_SERVER_CHATGPT_BASE_URL", "https://chatgpt.example.test/backend-api/")
	got := ConnectorInstallURL("Google Calendar", "calendar")
	if got != "https://chatgpt.example.test/apps/google-calendar/calendar" {
		t.Fatalf("connector install URL = %q", got)
	}
}

func TestConnectorInstallURLDefaultsToProduction(t *testing.T) {
	t.Setenv("CODEX_APP_SERVER_CHATGPT_BASE_URL", "")
	got := ConnectorInstallURL("Linear", "linear")
	if got != "https://chatgpt.com/apps/linear/linear" {
		t.Fatalf("connector install URL = %q", got)
	}
}
