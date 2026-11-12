package telemetry

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"codex_go/headershelper"
	"codex_go/network"
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
	// Policy is the application policy the helper runs under. The script can
	// open its own sockets, so it needs the same revocable permit as an SDK that
	// cannot enforce individual destinations (Rust #61028).
	Policy network.NetworkPolicy
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
	policy   network.NetworkPolicy
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
		ready:  make(chan struct{}),
		stop:   make(chan struct{}),
		policy: config.Policy,
		run:    headershelper.Run,
	}
	go helper.loop(command, config.CWD, interval)
	return helper, nil
}

func (h *otelHeadersHelper) loop(command string, cwd string, interval time.Duration) {
	first := true
	for {
		fetched, err := h.execute(context.Background(), command, cwd)
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

// execute runs one helper invocation under a revocable network permit. A
// revoking policy cancels the running helper process instead of waiting for it.
func (h *otelHeadersHelper) execute(ctx context.Context, command string, cwd string) (headershelper.Headers, error) {
	permit, err := h.policy.AcquireForUnsupportedSDK()
	if err != nil {
		return headershelper.Headers{}, err
	}
	defer permit.Release()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		headers headershelper.Headers
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		headers, runErr := h.run(runCtx, command, cwd)
		done <- outcome{headers: headers, err: runErr}
	}()
	select {
	case result := <-done:
		if checkErr := permit.Check(); checkErr != nil {
			return headershelper.Headers{}, checkErr
		}
		return result.headers, result.err
	case <-permit.Revoked():
		cancel()
		<-done
		return headershelper.Headers{}, network.ErrNetworkPolicyRevoked
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
