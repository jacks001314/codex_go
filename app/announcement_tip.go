package app

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"codex_go/auth"
	"codex_go/doctor"
	codextui "codex_go/tui"
)

var startupAnnouncementCache struct {
	once sync.Once
	text string
	ok   bool
}

// fetchStartupAnnouncementTip prewarms the announcement tip once per process.
// Failure is silent: the composer simply falls back to the local catalog.
func fetchStartupAnnouncementTip() (string, bool) {
	startupAnnouncementCache.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		startupAnnouncementCache.text, startupAnnouncementCache.ok = fetchStartupAnnouncementTipFrom(
			ctx,
			codextui.AnnouncementTipURL,
			http.DefaultClient,
			nil,
			doctor.Version(),
			time.Now(),
			codextui.CurrentTooltipTargetOS(),
		)
	})
	return startupAnnouncementCache.text, startupAnnouncementCache.ok
}

func fetchStartupAnnouncementTipFrom(ctx context.Context, endpoint string, client *http.Client, plan *auth.PlanType, version string, now time.Time, targetOS codextui.TooltipTargetOS) (string, bool) {
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false
	}
	response, err := client.Do(request)
	if err != nil {
		return "", false
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 256<<10))
	if err != nil {
		return "", false
	}
	return codextui.ParseAnnouncementTipTOML(string(body), plan, version, now, targetOS)
}
