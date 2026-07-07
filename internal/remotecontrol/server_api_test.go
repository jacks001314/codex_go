package remotecontrol

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex_go/internal/model"
)

func TestRemoteControlConnectionAuthReplacesProviderAccountHeader(t *testing.T) {
	auth := &RemoteControlConnectionAuth{
		AuthHeaders: &model.AuthHeaders{Headers: http.Header{
			"Authorization":      []string{"Bearer token"},
			"ChatGPT-Account-ID": []string{"provider-account-a", "provider-account-b"},
			"X-OpenAI-Fedramp":   []string{"true"},
		}},
		AccountID: "selected-account",
	}
	headers, err := auth.RequestHeaders()
	if err != nil {
		t.Fatalf("RequestHeaders() error = %v", err)
	}
	if got := headers.Values(RemoteControlAccountIDHeader); len(got) != 1 || got[0] != "selected-account" {
		t.Fatalf("account header = %#v, want selected-account", got)
	}
	if headers.Get("Authorization") != "Bearer token" || headers.Get("X-OpenAI-Fedramp") != "true" {
		t.Fatalf("headers = %#v", headers)
	}
}

func TestRemoteControlConnectionAuthRejectsInvalidAccountHeader(t *testing.T) {
	auth := &RemoteControlConnectionAuth{AccountID: "bad\naccount"}
	_, err := auth.RequestHeaders()
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("RequestHeaders() error = %v, want ErrInvalidRequest", err)
	}
}

func TestEnrollRemoteControlServerSendsRustShapeAndStoresToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	expiresAt := now.Add(time.Hour)
	var gotRequest EnrollRemoteServerRequest
	var gotInstallationID string
	var gotAccountID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/wham/remote/control/server/enroll" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		gotInstallationID = r.Header.Get(RemoteControlInstallationIDHeader)
		gotAccountID = r.Header.Get(RemoteControlAccountIDHeader)
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(EnrollRemoteServerResponse{
			ServerID:           "srv_e_first",
			EnvironmentID:      "env_first",
			RemoteControlToken: "server-token",
			ExpiresAt:          expiresAt.Format(time.RFC3339),
		})
	}))
	defer server.Close()
	target, err := NormalizeRemoteControlURL(server.URL + "/backend-api")
	if err != nil {
		t.Fatalf("NormalizeRemoteControlURL() error = %v", err)
	}
	auth := &RemoteControlConnectionAuth{AccountID: "account-a"}

	enrollment, err := EnrollRemoteControlServer(nil, target, auth, "installation-id", "server-name", &ServerAPIOptions{
		HTTPClient:       server.Client(),
		Now:              func() time.Time { return now },
		OS:               "test-os",
		Arch:             "test-arch",
		AppServerVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("EnrollRemoteControlServer() error = %v", err)
	}
	if gotInstallationID != "installation-id" || gotAccountID != "account-a" {
		t.Fatalf("headers installation/account = %q/%q", gotInstallationID, gotAccountID)
	}
	if gotRequest.Name != "server-name" || gotRequest.OS != "test-os" || gotRequest.Arch != "test-arch" || gotRequest.AppServerVersion != "1.2.3" || gotRequest.InstallationID != "installation-id" {
		t.Fatalf("request = %+v", gotRequest)
	}
	if enrollment.ServerID != "srv_e_first" || enrollment.EnvironmentID != "env_first" || enrollment.RemoteControlToken == nil || *enrollment.RemoteControlToken != "server-token" || enrollment.ExpiresAt == nil || !enrollment.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("enrollment = %+v", enrollment)
	}
}

func TestRefreshRemoteControlServerDefersTransientRequiredRefresh(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	retryAt := now.Add(90 * time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", retryAt.Format(http.TimeFormat))
		http.Error(w, `{"error":"busy"}`, http.StatusTooManyRequests)
	}))
	defer server.Close()
	enrollment := testEnrollmentWithBase(t, server.URL+"/backend-api", now)
	enrollment.ClearServerToken()
	auth := &RemoteControlConnectionAuth{AccountID: "account-a"}

	err := RefreshRemoteControlServer(nil, auth, "installation-id", enrollment, &ServerAPIOptions{
		HTTPClient: server.Client(),
		Now:        func() time.Time { return now },
		Backoff:    func() time.Duration { return 30 * time.Second },
	})
	if err == nil {
		t.Fatalf("RefreshRemoteControlServer() unexpectedly succeeded")
	}
	requestError := RemoteControlServerRequestErrorFromError(err)
	if requestError == nil || !requestError.Transient() || requestError.StatusCode == nil || *requestError.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("request error = %#v", requestError)
	}
	if enrollment.NextRefreshAt == nil || !enrollment.NextRefreshAt.Equal(retryAt) {
		t.Fatalf("nextRefreshAt = %v, want %v", enrollment.NextRefreshAt, retryAt)
	}
}

func TestRefreshRemoteControlServerIgnoresTransientProactiveRefresh(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"server"}`, http.StatusBadGateway)
	}))
	defer server.Close()
	enrollment := testEnrollmentWithBase(t, server.URL+"/backend-api", now)
	auth := &RemoteControlConnectionAuth{AccountID: "account-a"}

	err := RefreshRemoteControlServer(nil, auth, "installation-id", enrollment, &ServerAPIOptions{
		HTTPClient: server.Client(),
		Now:        func() time.Time { return now },
		Backoff:    func() time.Duration { return 30 * time.Second },
	})
	if err != nil {
		t.Fatalf("RefreshRemoteControlServer() error = %v, want proactive transient ignored", err)
	}
	if enrollment.NextRefreshAt == nil || !enrollment.NextRefreshAt.Equal(now.Add(30*time.Second)) {
		t.Fatalf("nextRefreshAt = %v", enrollment.NextRefreshAt)
	}
}

func TestRefreshRemoteControlServerRejectsMismatchedEnrollment(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(EnrollRemoteServerResponse{
			ServerID:           "other-server",
			EnvironmentID:      "env_first",
			RemoteControlToken: "new-token",
			ExpiresAt:          now.Add(time.Hour).Format(time.RFC3339),
		})
	}))
	defer server.Close()
	enrollment := testEnrollmentWithBase(t, server.URL+"/backend-api", now)
	auth := &RemoteControlConnectionAuth{AccountID: "account-a"}

	err := RefreshRemoteControlServer(nil, auth, "installation-id", enrollment, &ServerAPIOptions{
		HTTPClient: server.Client(),
		Now:        func() time.Time { return now },
	})
	if err == nil || !strings.Contains(err.Error(), "mismatched enrollment") {
		t.Fatalf("RefreshRemoteControlServer() error = %v, want mismatch", err)
	}
}

func TestParseRetryAfterSupportsDeltaSecondsAndHTTPDates(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	headers := http.Header{"Retry-After": []string{"120"}}
	if retryAt := ParseRetryAfter(headers, now); retryAt == nil || !retryAt.Equal(now.Add(120*time.Second)) {
		t.Fatalf("delta retryAt = %v", retryAt)
	}
	httpDate := now.Add(90 * time.Second)
	headers.Set("Retry-After", httpDate.Format(http.TimeFormat))
	if retryAt := ParseRetryAfter(headers, now); retryAt == nil || !retryAt.Equal(httpDate) {
		t.Fatalf("date retryAt = %v", retryAt)
	}
	headers.Set("Retry-After", "invalid")
	if retryAt := ParseRetryAfter(headers, now); retryAt != nil {
		t.Fatalf("invalid retryAt = %v", retryAt)
	}
}
