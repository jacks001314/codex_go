package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/codexapi"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

func writeDaybreakAuth(t *testing.T, home string, accessToken string, accountID string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token": accessToken,
			"account_id":   accountID,
		},
	})
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), payload, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
}

func TestFetchDaybreakNoticeUsesAuthenticatedRequest(t *testing.T) {
	var gotAuth, gotAccount, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("ChatGPT-Account-ID")
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"programs":[{"program":"cyber","state":"inactive","grants":[]}]}`)
	}))
	defer server.Close()

	notice, ok := fetchDaybreakNotice(context.Background(), server.URL+"/backend-api/", "tok", "acc")
	if !ok || notice != codextui.DaybreakNoticeApply {
		t.Fatalf("notice = %v ok=%v, want apply", notice, ok)
	}
	if gotPath != "/backend-api/accounts/verified_access" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok" || gotAccount != "acc" {
		t.Fatalf("headers = %q / %q", gotAuth, gotAccount)
	}
}

func TestFetchDaybreakNoticeFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	if _, ok := fetchDaybreakNotice(context.Background(), server.URL, "tok", ""); ok {
		t.Fatal("a non-2xx response must not produce a notice")
	}
	if _, ok := fetchDaybreakNotice(context.Background(), "  ", "tok", ""); ok {
		t.Fatal("a missing base URL must not produce a notice")
	}
}

func TestLoadDaybreakNoticeReadsLocalChatGPTAuth(t *testing.T) {
	home := t.TempDir()
	writeDaybreakAuth(t, home, "tok", "acc")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("ChatGPT-Account-ID") != "acc" {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"programs":[{"program":"cyber","state":"active","grants":[]}]}`)
	}))
	defer server.Close()

	method, token := "chatgpt", "tok"
	statusFn := func(context.Context) (appserver.AuthStatusResponse, error) {
		return appserver.AuthStatusResponse{AuthMethod: &method, AuthToken: &token}, nil
	}
	if notice := loadDaybreakNotice(context.Background(), statusFn, server.URL, home); notice != codextui.DaybreakNoticeLimited {
		t.Fatalf("notice = %v, want limited for an active cyber program", notice)
	}
}

func TestLoadDaybreakNoticeFailsClosedOnMismatchedServerAuth(t *testing.T) {
	home := t.TempDir()
	writeDaybreakAuth(t, home, "tok", "acc")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("the verified-access request must not run with mismatched auth")
	}))
	defer server.Close()

	method, token := "chatgpt", "other-token"
	statusFn := func(context.Context) (appserver.AuthStatusResponse, error) {
		return appserver.AuthStatusResponse{AuthMethod: &method, AuthToken: &token}, nil
	}
	if notice := loadDaybreakNotice(context.Background(), statusFn, server.URL, home); notice != codextui.DaybreakNoticeLimited {
		t.Fatalf("notice = %v, want limited", notice)
	}
}

func TestLoadDaybreakNoticeFailsClosedWithoutChatGPTAuth(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"api-key","OPENAI_API_KEY":"sk"}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("the verified-access request must not run without ChatGPT auth")
	}))
	defer server.Close()
	method, token := "apikey", ""
	statusFn := func(context.Context) (appserver.AuthStatusResponse, error) {
		return appserver.AuthStatusResponse{AuthMethod: &method, AuthToken: &token}, nil
	}
	if notice := loadDaybreakNotice(context.Background(), statusFn, server.URL, home); notice != codextui.DaybreakNoticeLimited {
		t.Fatalf("notice = %v, want limited", notice)
	}
}

func TestDaybreakNoticeForModelGatesProviderAndCache(t *testing.T) {
	cache := &daybreakNoticeCache{}
	if got := daybreakNoticeForModel("openai", cache, "gpt-5.6-sol"); got != codextui.DaybreakNoticeLimited {
		t.Fatalf("pending notice = %v, want limited", got)
	}
	cache.set(codextui.DaybreakNoticeApply)
	if got := daybreakNoticeForModel("openai", cache, "gpt-5.6-sol"); got != codextui.DaybreakNoticeApply {
		t.Fatalf("cached notice = %v, want apply", got)
	}
	if got := daybreakNoticeForModel("openai", cache, "gpt-6-astra"); got != codextui.DaybreakNoticeAstra {
		t.Fatalf("astra notice = %v", got)
	}
	if got := daybreakNoticeForModel("anthropic", cache, "gpt-5.6-sol"); got != codextui.DaybreakNoticeLimited {
		t.Fatalf("non-openai notice = %v, want limited", got)
	}
}

func TestTurnErrorIsCyberPolicy(t *testing.T) {
	cases := []struct {
		name string
		info any
		want bool
	}{
		{"string camel", "cyberPolicy", true},
		{"string snake", "cyber_policy", true},
		{"object tagged", map[string]any{"type": "cyberPolicy"}, true},
		{"other string", "badRequest", false},
		{"other object", map[string]any{"type": "responseStreamDisconnected"}, false},
		{"nil", nil, false},
	}
	for _, test := range cases {
		if got := turnErrorIsCyberPolicy(appserver.TurnError{CodexErrorInfo: test.info}); got != test.want {
			t.Fatalf("%s: turnErrorIsCyberPolicy = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestCyberPolicyErrorClassifiesLocalErrors(t *testing.T) {
	cyber := codexapi.NewAPIErrorWithDetails(codexapi.APIErrorDetails{Kind: codexapi.ErrorCyberPolicy})
	if !cyberPolicyError(cyber) {
		t.Fatal("a cyber policy API error should classify as cyber policy")
	}
	other := codexapi.NewAPIErrorWithDetails(codexapi.APIErrorDetails{Kind: codexapi.ErrorInvalidRequest})
	if cyberPolicyError(other) {
		t.Fatal("an unrelated API error must not classify as cyber policy")
	}
	if cyberPolicyError(nil) {
		t.Fatal("nil must not classify as cyber policy")
	}
}

// TestRemoteClientCyberPolicyNotificationRendersRefusalCell covers the remote
// error path: the server's cyber-policy classification reaches the TUI as a
// refusal message rather than a generic turn error.
func TestRemoteClientCyberPolicyNotificationRendersRefusalCell(t *testing.T) {
	messages := make(chan bubbletea.Msg, 4)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	client := &remoteAppServerTUIClient{messages: messages, state: state}

	params, err := json.Marshal(appserver.ErrorNotification{
		Error:    appserver.TurnError{Message: "blocked by policy", CodexErrorInfo: "cyberPolicy"},
		ThreadID: "thread-1",
		TurnID:   "turn-1",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{Method: string(appserver.NotificationError), Params: params}); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}

	found := false
	for {
		select {
		case msg := <-messages:
			if _, ok := msg.(codextea.CyberPolicyErrorMsg); ok {
				found = true
			}
		default:
			if !found {
				t.Fatal("expected a CyberPolicyErrorMsg")
			}
			return
		}
	}
}
