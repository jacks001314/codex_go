package remotecontrol

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRemoteControlEnrollmentClassifiesServerTokenRefreshRequirement(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	enrollment := testEnrollment(t, now)
	cases := []struct {
		name       string
		enrollment *Enrollment
		want       RemoteControlServerTokenRefreshRequirement
	}{
		{"proactive inside skew", enrollment, ServerTokenRefreshProactive},
		{"not needed outside skew", withEnrollmentExpiry(enrollment, now.Add(301*time.Second)), ServerTokenRefreshNotNeeded},
		{"deferred proactive", withEnrollmentNextRefresh(enrollment, now.Add(30*time.Second)), ServerTokenRefreshNotNeeded},
		{"defer expired", withEnrollmentNextRefresh(enrollment, now), ServerTokenRefreshProactive},
		{"missing token", withEnrollmentToken(enrollment, nil), ServerTokenRefreshRequired},
		{"missing expiry", withEnrollmentExpiryPtr(enrollment, nil), ServerTokenRefreshRequired},
		{"expired wins over defer", withEnrollmentExpiryAndNext(enrollment, now, now.Add(time.Hour)), ServerTokenRefreshRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.enrollment.ServerTokenRefreshRequirementAt(now); got != tc.want {
				t.Fatalf("requirement = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestPreviewRemoteControlResponseBodyRedactsSensitiveFields(t *testing.T) {
	preview := PreviewRemoteControlResponseBody([]byte(`{"server_id":"srv_e_test","remote_control_token":"secret","pairing_code":"pairing-code","manual_pairing_code":"ABCD-EFGH"}`))
	var decoded map[string]string
	if err := json.Unmarshal([]byte(preview), &decoded); err != nil {
		t.Fatalf("redacted preview should be valid JSON: %v: %s", err, preview)
	}
	for _, key := range []string{"remote_control_token", "pairing_code", "manual_pairing_code"} {
		if decoded[key] != "<redacted>" {
			t.Fatalf("%s = %q, want redacted in %s", key, decoded[key], preview)
		}
	}
	if got := PreviewRemoteControlResponseBody([]byte("  \n")); got != "<empty>" {
		t.Fatalf("empty preview = %q", got)
	}
}

func TestEnrollmentStartPairingUsesBackendProtocol(t *testing.T) {
	now := time.Now().UTC()
	expires := now.Add(2 * time.Minute).Format(time.RFC3339)
	var gotAuth string
	var gotRequest StartRemoteControlPairingRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/backend-api/wham/remote/control/server/pair" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("x-request-id", "req-1")
		_ = json.NewEncoder(w).Encode(StartRemoteControlPairingResponse{
			PairingCode:       "pair-code",
			ManualPairingCode: stringPtrIfNotEmpty("123456"),
			ServerID:          "srv_e_first",
			EnvironmentID:     "env_first",
			ExpiresAt:         expires,
		})
	}))
	defer server.Close()
	enrollment := testEnrollmentWithBase(t, server.URL+"/backend-api", now)

	response, err := enrollment.StartPairing(nil, &PairingStartParams{ManualCode: true}, &PairingOptions{HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("StartPairing() error = %v", err)
	}
	if gotAuth != "Bearer token" || !gotRequest.ManualCode {
		t.Fatalf("auth/request = %q/%+v", gotAuth, gotRequest)
	}
	if response.PairingCode != "pair-code" || response.ManualPairingCode == nil || *response.ManualPairingCode != "123456" || response.ExpiresAt != now.Add(2*time.Minute).Unix() {
		t.Fatalf("response = %+v", response)
	}
}

func TestEnrollmentPairingStatusMapsGoneToInvalidRequest(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"pairing_code":"expired"}`, http.StatusGone)
	}))
	defer server.Close()
	enrollment := testEnrollmentWithBase(t, server.URL+"/backend-api", now)
	code := "pair-code"
	_, err := enrollment.PairingStatus(nil, &PairingStatusParams{PairingCode: &code}, &PairingOptions{HTTPClient: server.Client()})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("PairingStatus() error = %v, want ErrInvalidRequest", err)
	}
}

func TestEnrollmentStartPairingRejectsMismatchedEnrollment(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(StartRemoteControlPairingResponse{
			PairingCode:   "pair-code",
			ServerID:      "other-server",
			EnvironmentID: "env_first",
			ExpiresAt:     now.Add(time.Minute).Format(time.RFC3339),
		})
	}))
	defer server.Close()
	enrollment := testEnrollmentWithBase(t, server.URL+"/backend-api", now)
	_, err := enrollment.StartPairing(nil, &PairingStartParams{}, &PairingOptions{HTTPClient: server.Client()})
	if err == nil || !strings.Contains(err.Error(), "mismatched enrollment") {
		t.Fatalf("StartPairing() error = %v, want mismatch", err)
	}
}

func testEnrollment(t *testing.T, now time.Time) *Enrollment {
	t.Helper()
	return testEnrollmentWithBase(t, "http://localhost/backend-api/", now)
}

func testEnrollmentWithBase(t *testing.T, rawURL string, now time.Time) *Enrollment {
	t.Helper()
	target, err := NormalizeRemoteControlURL(rawURL)
	if err != nil {
		t.Fatalf("NormalizeRemoteControlURL() error = %v", err)
	}
	token := "token"
	expiresAt := now.Add(300 * time.Second)
	return &Enrollment{
		RemoteControlTarget: target,
		AccountID:           "account-a",
		EnvironmentID:       "env_first",
		ServerID:            "srv_e_first",
		ServerName:          "first-server",
		RemoteControlToken:  &token,
		ExpiresAt:           &expiresAt,
	}
}

func cloneEnrollment(in *Enrollment) *Enrollment {
	out := *in
	if in.RemoteControlTarget != nil {
		out.RemoteControlTarget = cloneRemoteControlTarget(in.RemoteControlTarget)
	}
	out.RemoteControlToken = cloneStringPtr(in.RemoteControlToken)
	if in.ExpiresAt != nil {
		value := *in.ExpiresAt
		out.ExpiresAt = &value
	}
	if in.NextRefreshAt != nil {
		value := *in.NextRefreshAt
		out.NextRefreshAt = &value
	}
	return &out
}

func withEnrollmentExpiry(in *Enrollment, expiresAt time.Time) *Enrollment {
	out := cloneEnrollment(in)
	out.ExpiresAt = &expiresAt
	return out
}

func withEnrollmentExpiryPtr(in *Enrollment, expiresAt *time.Time) *Enrollment {
	out := cloneEnrollment(in)
	out.ExpiresAt = expiresAt
	return out
}

func withEnrollmentToken(in *Enrollment, token *string) *Enrollment {
	out := cloneEnrollment(in)
	out.RemoteControlToken = token
	return out
}

func withEnrollmentNextRefresh(in *Enrollment, nextRefreshAt time.Time) *Enrollment {
	out := cloneEnrollment(in)
	out.NextRefreshAt = &nextRefreshAt
	return out
}

func withEnrollmentExpiryAndNext(in *Enrollment, expiresAt time.Time, nextRefreshAt time.Time) *Enrollment {
	out := cloneEnrollment(in)
	out.ExpiresAt = &expiresAt
	out.NextRefreshAt = &nextRefreshAt
	return out
}
