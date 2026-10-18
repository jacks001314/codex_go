package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	codextui "codex_go/tui"
)

func TestFetchStartupAnnouncementTipLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[[announcements]]\ncontent = \"Fetched announcement\"\ntarget_app = \"cli\"\n"))
	}))
	defer server.Close()

	got, ok := fetchStartupAnnouncementTipFrom(
		context.Background(),
		server.URL,
		server.Client(),
		nil,
		"0.0.0",
		time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
		codextui.TooltipTargetOSWindows,
	)
	if !ok || got != "Fetched announcement" {
		t.Fatalf("announcement = %q ok=%v", got, ok)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	if got, ok := fetchStartupAnnouncementTipFrom(
		context.Background(), failing.URL, failing.Client(), nil, "0.0.0",
		time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC), codextui.TooltipTargetOSWindows,
	); ok {
		t.Fatalf("failed fetch returned announcement %q", got)
	}
}
