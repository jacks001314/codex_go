package telemetry

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"codex_go/headershelper"
)

// DefaultOTelHeadersHelperRefreshInterval is Rust's 29-minute default refresh.
const DefaultOTelHeadersHelperRefreshInterval = 29 * time.Minute

// OTelHeadersHelperConfig configures the collector-credential helper shared by
// the explicit OTLP HTTP log, trace, and metric exporters (Rust #61028). The
// command runs from CWD and prints a JSON object of header strings.
type OTelHeadersHelperConfig struct {
	Command         string
	CWD             string
	RefreshInterval time.Duration
}

// otelHeadersHelper refreshes collector headers on a background worker. One
// worker serves every explicit HTTP signal. A failed refresh replaces the cache
// with its error, so exports are suspended until the helper recovers instead of
// silently falling back to stale or static credentials.
type otelHeadersHelper struct {
	mu      sync.RWMutex
	headers http.Header
	err     error
	ready   chan struct{}
	stop    chan struct{}

	stopOnce sync.Once
	run      func(context.Context, string, string) (headershelper.Headers, error)
}

// startOTelHeadersHelper starts the shared worker. A nil config returns a nil
// helper, which leaves the exporters on their static headers.
func startOTelHeadersHelper(config *OTelHeadersHelperConfig) (*otelHeadersHelper, error) {
	if config == nil {
		return nil, nil
	}
	command := strings.TrimSpace(config.Command)
	if command == "" {
		return nil, errors.New("OTel headers helper requires a command")
	}
	interval := config.RefreshInterval
	if interval <= 0 {
		interval = DefaultOTelHeadersHelperRefreshInterval
	}
	helper := &otelHeadersHelper{
		ready: make(chan struct{}),
		stop:  make(chan struct{}),
		run:   headershelper.Run,
	}
	go helper.loop(command, config.CWD, interval)
	return helper, nil
}

func (h *otelHeadersHelper) loop(command string, cwd string, interval time.Duration) {
	first := true
	for {
		fetched, err := h.run(context.Background(), command, cwd)
		h.mu.Lock()
		if err != nil {
			h.headers = nil
			h.err = err
		} else {
			h.headers = fetched.HTTPHeader()
			h.err = nil
		}
		h.mu.Unlock()
		if first {
			close(h.ready)
			first = false
		}
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
		case <-h.stop:
			timer.Stop()
			return
		}
	}
}

// headers waits for the worker's first result and returns the current collector
// headers. An error from the latest refresh blocks the caller's export.
func (h *otelHeadersHelper) headersForExport(ctx context.Context) (map[string]string, error) {
	if h == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-h.ready:
	case <-h.stop:
		return nil, errors.New("OTel headers helper stopped")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.err != nil {
		return nil, h.err
	}
	out := make(map[string]string, len(h.headers))
	for name, values := range h.headers {
		if len(values) > 0 {
			out[name] = values[0]
		}
	}
	return out, nil
}

// Close stops the refresh worker.
func (h *otelHeadersHelper) Close() {
	if h == nil {
		return
	}
	h.stopOnce.Do(func() { close(h.stop) })
}

// mergeExportHeaders overlays the helper headers on the static ones, so a
// refreshed credential replaces the configured value of the same name.
func mergeExportHeaders(static map[string]string, dynamic map[string]string) map[string]string {
	if len(static) == 0 && len(dynamic) == 0 {
		return nil
	}
	merged := make(map[string]string, len(static)+len(dynamic))
	for name, value := range static {
		merged[name] = value
	}
	for name, value := range dynamic {
		merged[name] = value
	}
	return merged
}
