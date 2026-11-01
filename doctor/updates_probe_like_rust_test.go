package doctor

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex_go/install"
)

// updateProbeTransport answers the release lookup locally and records the
// User-Agent the probe sent.
type updateProbeTransport struct {
	status    int
	body      string
	userAgent string
}

func (t *updateProbeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.userAgent = request.Header.Get("User-Agent")
	recorder := httptest.NewRecorder()
	recorder.Code = t.status
	_, _ = recorder.Body.WriteString(t.body)
	response := recorder.Result()
	response.Request = request
	response.Body = io.NopCloser(strings.NewReader(t.body))
	return response, nil
}

func TestUpdatesCheckRendersRustProbeRowsAndUserAgent(t *testing.T) {
	home := t.TempDir()
	unsetEnvForDoctor(t, "CODEX_MANAGED_BY_NPM")
	unsetEnvForDoctor(t, "CODEX_MANAGED_BY_BUN")
	unsetEnvForDoctor(t, "CODEX_MANAGED_PACKAGE_ROOT")

	transport := &updateProbeTransport{status: http.StatusOK, body: `{"tag_name":"go-v9.9.9"}`}
	builder := NewBuilder()
	builder.httpClient = &http.Client{Transport: transport}
	check := builder.updatesCheck(home, &Options{})
	if transport.userAgent != "codex-doctor/"+Version() {
		t.Fatalf("User-Agent = %q, want codex-doctor/%s", transport.userAgent, Version())
	}
	for _, want := range []string{
		"latest version: 9.9.9",
		"latest version status: newer version is available",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
}

func TestUpdatesCheckReportsFailedProbeLikeRust(t *testing.T) {
	home := t.TempDir()
	unsetEnvForDoctor(t, "CODEX_MANAGED_BY_NPM")
	unsetEnvForDoctor(t, "CODEX_MANAGED_BY_BUN")
	unsetEnvForDoctor(t, "CODEX_MANAGED_PACKAGE_ROOT")

	transport := &updateProbeTransport{status: http.StatusForbidden, body: "{}"}
	builder := NewBuilder()
	builder.httpClient = &http.Client{Transport: transport}
	check := builder.updatesCheck(home, &Options{})
	if check.Status != CheckStatusWarning {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "latest version probe: HTTP 403 Forbidden") {
		t.Fatalf("probe detail = %#v", check.Details)
	}
	for _, banned := range []string{"latest version status", "latest version:"} {
		for _, detail := range check.Details {
			if strings.HasPrefix(detail, banned) {
				t.Fatalf("failed probe still rendered %q in %#v", banned, check.Details)
			}
		}
	}
}

func TestLatestVersionStatusTextMatchesRustNonNewerCopy(t *testing.T) {
	for _, status := range []install.UpdateStatus{
		install.UpdateStatusUpToDate,
		install.UpdateStatusUnknown,
	} {
		if got, want := latestVersionStatusText(status), "current version is not older"; got != want {
			t.Fatalf("latestVersionStatusText(%q) = %q, want %q", status, got, want)
		}
	}
}
