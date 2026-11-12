package telemetry

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/headershelper"
	"codex_go/network"
)

func newInjectedHeadersHelper(t *testing.T, interval time.Duration, run func(context.Context, string, string) (headershelper.Headers, error)) *otelHeadersHelper {
	t.Helper()
	helper := &otelHeadersHelper{ready: make(chan struct{}), stop: make(chan struct{}), run: run}
	go helper.loop("helper", t.TempDir(), interval)
	t.Cleanup(helper.Close)
	return helper
}

func waitForHeaders(t *testing.T, helper *otelHeadersHelper, want string) map[string]string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		headers, err := helper.headersForExport(context.Background())
		if err == nil && headers["X-Auth"] == want {
			return headers
		}
		time.Sleep(5 * time.Millisecond)
	}
	headers, err := helper.headersForExport(context.Background())
	t.Fatalf("helper headers = %#v err = %v, want X-Auth=%q", headers, err, want)
	return nil
}

func headerResponse(t *testing.T, payload string) headershelper.Headers {
	t.Helper()
	headers, err := headershelper.Parse([]byte(payload))
	if err != nil {
		t.Fatalf("Parse(%s) error = %v", payload, err)
	}
	return headers
}

// Mirrors Rust #61028: the shared worker refreshes collector credentials, a
// failed refresh suspends consumption, and a later success restores exports.
func TestOTelHeadersHelperRefreshesAndBlocksLikeRust(t *testing.T) {
	var state atomic.Int64 // 0 = first headers, 1 = failure, 2 = refreshed headers
	helper := newInjectedHeadersHelper(t, 5*time.Millisecond, func(context.Context, string, string) (headershelper.Headers, error) {
		switch state.Load() {
		case 0:
			return headerResponse(t, `{"X-Auth":"first"}`), nil
		case 1:
			return headershelper.Headers{}, errors.New("helper failed")
		default:
			return headerResponse(t, `{"X-Auth":"second"}`), nil
		}
	})

	waitForHeaders(t, helper, "first")
	state.Store(1)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := helper.headersForExport(context.Background()); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a failed refresh did not suspend the headers")
		}
		time.Sleep(5 * time.Millisecond)
	}
	state.Store(2)
	waitForHeaders(t, helper, "second")
}

type headerCaptureDoer struct {
	mu      sync.Mutex
	calls   int
	headers http.Header
}

func (d *headerCaptureDoer) Do(request *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.calls++
	d.headers = request.Header.Clone()
	d.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}}, nil
}

func (d *headerCaptureDoer) snapshot() (int, http.Header) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls, d.headers.Clone()
}

// Mirrors Rust #61028: helper headers override static ones, and an export is
// blocked while the helper is failing instead of falling back to the static
// credential.
func TestOTLPLogsExporterUsesRefreshedHeadersLikeRust(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	helper := newInjectedHeadersHelper(t, 5*time.Millisecond, func(context.Context, string, string) (headershelper.Headers, error) {
		if failing.Load() {
			return headershelper.Headers{}, errors.New("collector credential unavailable")
		}
		return headerResponse(t, `{"X-Auth":"fresh","X-Helper":"yes"}`), nil
	})
	doer := &headerCaptureDoer{}
	exporter := NewOTLPLogsExporter(OTLPLogsExporterOptions{
		Endpoint:     "https://collector.test/v1/logs",
		Headers:      map[string]string{"X-Auth": "stale", "X-Static": "kept"},
		HTTPClient:   doer,
		HeaderHelper: helper,
	})
	request := OTLPExportLogsRequest{ResourceLogs: []OTLPResourceLogs{{
		ScopeLogs: []OTLPScopeLogs{{
			Scope:      OTLPScope{Name: LogsScopeName},
			LogRecords: []OTLPLogRecord{{Body: "hello"}},
		}},
	}}}

	if err := exporter.Export(context.Background(), request); err == nil {
		t.Fatal("Export() succeeded while the helper was failing")
	}
	if calls, _ := doer.snapshot(); calls != 0 {
		t.Fatalf("exports while the helper failed = %d, want none", calls)
	}

	failing.Store(false)
	waitForHeaders(t, helper, "fresh")
	if err := exporter.Export(context.Background(), request); err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	calls, headers := doer.snapshot()
	if calls != 1 {
		t.Fatalf("export calls = %d, want one", calls)
	}
	if headers.Get("X-Auth") != "fresh" || headers.Get("X-Static") != "kept" || headers.Get("X-Helper") != "yes" {
		t.Fatalf("export headers = %#v", headers)
	}
}

// Mirrors Rust #61028: the helper can open its own sockets, so a restricted
// application policy disables it entirely instead of running it unobserved.
func TestOTelHeadersHelperRespectsNetworkPolicyLikeRust(t *testing.T) {
	target, err := url.Parse("https://collector.test/v1/logs")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	policy := network.NetworkPolicy{}.RestrictToEndpoints([]*url.URL{target})
	ran := make(chan struct{}, 1)
	helper := &otelHeadersHelper{
		ready:  make(chan struct{}),
		stop:   make(chan struct{}),
		policy: policy,
		run: func(context.Context, string, string) (headershelper.Headers, error) {
			ran <- struct{}{}
			return headerResponse(t, `{"X-Auth":"should-not-run"}`), nil
		},
	}
	go helper.loop("helper", t.TempDir(), time.Hour)
	t.Cleanup(helper.Close)

	if _, err := helper.headersForExport(context.Background()); !errors.Is(err, network.ErrNetworkPolicyUnsupportedTransport) {
		t.Fatalf("headersForExport() error = %v, want the unsupported-transport policy error", err)
	}
	select {
	case <-ran:
		t.Fatal("the helper ran under a restricted network policy")
	default:
	}
}
