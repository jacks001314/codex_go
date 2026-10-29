//go:build stability

package scenarios

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/stability/harness/metrics"
	"codex_go/stability/harness/mockmodel"
	"codex_go/stability/harness/netproxy"
	"codex_go/stability/harness/report"
)

// TestFaultStreamStatusErrors covers the HTTP-level failures a turn can hit
// before the stream starts: rate limiting, server errors, and slow headers.
func TestFaultStreamStatusErrors(t *testing.T) {
	run := report.New("faults-status")
	defer finishReport(t, run)

	t.Run("rate_limit", func(t *testing.T) {
		server := mockmodel.New(
			mockmodel.Error(http.StatusTooManyRequests, `{"error":{"message":"rate limited"}}`),
			mockmodel.Text("recovered"),
		)
		defer server.Close()
		runner := faultRunner(server.URL(), 3)
		response, err := runner.Run(faultContext(t, 20*time.Second), faultRequest())
		retried := err == nil && response != nil && response.Message == "recovered"
		if retried {
			run.Check("fault.rate_limit.retried", true, "requests=%d", server.RequestCount())
			return
		}
		if err == nil {
			t.Fatalf("run returned %+v without an error and without recovering", response)
		}
		if !strings.Contains(err.Error(), "429") {
			t.Fatalf("rate-limit error is not diagnosable: %v", err)
		}
		run.Check("fault.rate_limit.retried", true, "surfaced 429 after %d request(s)", server.RequestCount())
	})

	t.Run("server_error", func(t *testing.T) {
		server := mockmodel.New(mockmodel.Error(http.StatusInternalServerError, `{"error":{"message":"boom"}}`))
		defer server.Close()
		runner := faultRunner(server.URL(), 0)
		_, err := runner.Run(faultContext(t, 10*time.Second), faultRequest())
		if err == nil {
			t.Fatal("500 response was not surfaced as an error")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Fatalf("500 error is not diagnosable: %v", err)
		}
		run.Check("fault.server_error.diagnosable", true, "%v", err)
	})

	t.Run("slow_header", func(t *testing.T) {
		server := mockmodel.New(mockmodel.Text("never"))
		defer server.Close()
		proxy := netproxy.New(t, server.URL(), netproxy.Fault{Delay: 3 * time.Second})
		runner := faultRunner(proxy.URL, 0)

		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := runner.Run(ctx, faultRequest())
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("a 3s header delay did not trip the 300ms context")
		}
		if elapsed > 2*time.Second {
			t.Fatalf("cancellation took %v, want the context deadline to win", elapsed)
		}
		run.Check("fault.slow_header.times_out", true, "elapsed=%s err=%v", elapsed, err)
	})
}

// TestFaultStreamBodyFailures covers mid-stream damage: aborted connections,
// frames cut in half, and an upstream that speaks the wrong protocol.
func TestFaultStreamBodyFailures(t *testing.T) {
	run := report.New("faults-body")
	defer finishReport(t, run)

	cases := []struct {
		name     string
		response *mockmodel.Response
	}{
		{name: "abort_mid_stream", response: mockmodel.AbortMidStream("partial answer")},
		{name: "partial_frame", response: mockmodel.PartialStream("half a frame")},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := mockmodel.New(testCase.response, mockmodel.Text("recovered"))
			defer server.Close()
			runner := faultRunner(server.URL(), 0)
			if _, err := runner.Run(faultContext(t, 10*time.Second), faultRequest()); err == nil {
				t.Fatal("damaged stream produced no error")
			}
			// The same runner must recover on the next turn; a mid-stream
			// failure may not poison the runner.
			response, err := runner.Run(faultContext(t, 10*time.Second), faultRequest())
			if err != nil {
				t.Fatalf("runner did not recover after %s: %v", testCase.name, err)
			}
			if response.Message != "recovered" {
				t.Fatalf("message after recovery = %q, want recovered", response.Message)
			}
			run.Check("fault."+testCase.name+".recovered", true, "requests=%d", server.RequestCount())
		})
	}

	t.Run("invalid_sse", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: {\"type\":\"response.created\"\n\n")
			_, _ = io.WriteString(w, "data: this is not json\n\n")
		}))
		defer server.Close()
		runner := faultRunner(server.URL, 0)
		if _, err := runner.Run(faultContext(t, 10*time.Second), faultRequest()); err == nil {
			t.Fatal("malformed SSE was accepted")
		}
		run.Check("fault.invalid_sse.rejected", true, "ok")
	})

	t.Run("proxy_drop", func(t *testing.T) {
		server := mockmodel.New(mockmodel.Text(strings.Repeat("x", 4096)), mockmodel.Text("recovered"))
		defer server.Close()
		proxy := netproxy.New(t, server.URL(), netproxy.Fault{DropAfter: 600})
		runner := faultRunner(proxy.URL, 0)
		if _, err := runner.Run(faultContext(t, 10*time.Second), faultRequest()); err == nil {
			t.Fatal("connection dropped mid-body produced no error")
		}
		run.Check("fault.proxy_drop.surfaced", true, "ok")
	})
}

// TestFaultCancelMidStream proves a cancelled turn returns promptly and does
// not leave a goroutine behind once the upstream gate is released.
func TestFaultCancelMidStream(t *testing.T) {
	run := report.New("faults-cancel")
	defer finishReport(t, run)

	gate := mockmodel.NewGate()
	server := mockmodel.New(mockmodel.Text("slow answer").GateAt(1, gate))
	defer server.Close()
	runner := faultRunner(server.URL(), 0)

	before := metrics.Take()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, faultRequest())
		done <- err
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled turn returned no error")
		}
		run.Check("fault.cancel.returns", true, "%v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled turn did not return within 10s")
	}
	gate.Release()

	waitForGoroutines(t, before.Goroutines, 5, 10*time.Second)
	run.Check("fault.cancel.no_goroutine_leak", true, "baseline=%d", before.Goroutines)
}

// TestFaultOversizedResponse feeds a multi-megabyte answer through the runner
// and bounds the heap it costs.
func TestFaultOversizedResponse(t *testing.T) {
	run := report.New("faults-oversized")
	defer finishReport(t, run)

	const size = 2 << 20
	server := mockmodel.New(mockmodel.Text(strings.Repeat("a", size)))
	defer server.Close()
	runner := faultRunner(server.URL(), 0)
	before := metrics.Take()
	response, err := runner.Run(faultContext(t, 60*time.Second), faultRequest())
	if err != nil {
		t.Fatalf("oversized response failed: %v", err)
	}
	if len(response.Message) != size {
		t.Fatalf("message = %d bytes, want %d", len(response.Message), size)
	}
	after := metrics.Take()
	grown := int64(after.HeapAllocBytes) - int64(before.HeapAllocBytes)
	run.Check("fault.oversized.bounded", grown < 256<<20, "heap grew %d bytes for a %d byte answer", grown, size)
}

func faultRunner(baseURL string, streamRetries uint64) *model.ResponsesAgentRunner {
	return model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{
		Provider: &model.APIProvider{
			BaseURL:           baseURL + "/v1",
			RequestMaxRetries: 0,
			StreamMaxRetries:  streamRetries,
		},
		Stream: true,
	})
}

func faultRequest() *model.AgentRequest {
	return &model.AgentRequest{Prompt: "ping", Model: "gpt-mock"}
}

func faultContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

func finishReport(t *testing.T, run *report.Report) {
	t.Helper()
	run.Finish()
	path, err := run.Write(reportDir())
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	if !run.Passed {
		t.Fatalf("scenario failed: %+v (report %s)", run.Checks, path)
	}
}

func waitForGoroutines(t *testing.T, baseline, slack int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last int
	for time.Now().Before(deadline) {
		last = metrics.Take().Goroutines
		if last <= baseline+slack {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("goroutines settled at %d, want <= %d", last, baseline+slack)
}
