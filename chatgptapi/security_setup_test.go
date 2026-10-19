package chatgptapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCloudClientGetSecuritySetupLikeRust(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"notice":{"title":"Secure your account","description":"Add a second factor.","action":{"label":"Open settings","url":"https://chatgpt.com/security"}}}`))
	}))
	defer server.Close()

	client := NewCloudClientWithoutRedirects(&CloudClientOptions{BaseURL: server.URL + "/backend-api"})
	notice, err := client.GetSecuritySetup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if path != "/backend-api/wham/security-setup" {
		t.Fatalf("path = %q", path)
	}
	if notice == nil || notice.Title != "Secure your account" || notice.Action.URL != "https://chatgpt.com/security" {
		t.Fatalf("notice = %#v", notice)
	}
}

func TestCloudClientGetSecuritySetupDropsInvalidNoticesLikeRust(t *testing.T) {
	cases := []string{
		`{"notice":null}`,
		`{"notice":{"title":"","description":"x","action":{"label":"go","url":"https://chatgpt.com/x"}}}`,
		`{"notice":{"title":"ok","description":"x","action":{"label":"go","url":"http://chatgpt.com/x"}}}`,
		`{"notice":{"title":"ok","description":"x","action":{"label":"go","url":"https://evil.example/x"}}}`,
		`{"notice":{"title":"ok","description":"x","action":{"label":"go","url":"https://chatgpt.com:8443/x"}}}`,
		`{"notice":{"title":"ok\nline","description":"x","action":{"label":"go","url":"https://chatgpt.com/x"}}}`,
		`{"notice":{"title":"` + strings.Repeat("x", 257) + `","description":"x","action":{"label":"go","url":"https://chatgpt.com/x"}}}`,
	}
	for _, body := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		client := NewCloudClientWithoutRedirects(&CloudClientOptions{BaseURL: server.URL + "/backend-api"})
		notice, err := client.GetSecuritySetup(context.Background())
		server.Close()
		if err != nil {
			t.Errorf("body %q returned error %v", body, err)
		}
		if notice != nil {
			t.Errorf("body %q produced notice %#v", body, notice)
		}
	}
}
