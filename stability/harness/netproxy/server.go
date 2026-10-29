// Package netproxy injects transport faults between a client and an HTTP
// target: slow responses, jitter, throttling, fragmented writes, and mid-body
// disconnects. It is the Go counterpart of Rust's app-server `rpc_delay.rs`.
package netproxy

import (
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Fault describes the transport damage a Proxy injects.
type Fault struct {
	// Delay is applied before the request is forwarded, modelling a slow
	// upstream that has not sent headers yet.
	Delay time.Duration
	// Jitter adds a deterministic pseudo-random delay in [0, Jitter).
	Jitter time.Duration
	// Throttle caps the response body rate in bytes per second; 0 is
	// unlimited.
	Throttle int
	// DropAfter closes the connection after forwarding this many body bytes.
	// 0 never drops. A non-zero value models an upstream that dies mid-turn.
	DropAfter int64
	// SplitWrites flushes after every byte so the client observes fragmented
	// delivery instead of one coalesced read.
	SplitWrites bool
	// Seed makes Jitter reproducible.
	Seed int64
}

// Proxy is a fault-injecting reverse proxy for one target URL.
type Proxy struct {
	URL string

	target   string
	fault    Fault
	server   *httptest.Server
	requests atomic.Int64
	randMu   sync.Mutex
	rng      *rand.Rand
}

// New starts a proxy for target. It is closed automatically when the test
// ends.
func New(t testing.TB, target string, fault Fault) *Proxy {
	t.Helper()
	proxy := &Proxy{
		target: target,
		fault:  fault,
		rng:    rand.New(rand.NewSource(fault.Seed)),
	}
	proxy.server = httptest.NewServer(http.HandlerFunc(proxy.serveHTTP))
	proxy.URL = proxy.server.URL
	t.Cleanup(proxy.Close)
	return proxy
}

// Close stops the proxy listener.
func (p *Proxy) Close() {
	if p.server == nil {
		return
	}
	p.server.CloseClientConnections()
	p.server.Close()
}

// RequestCount reports how many requests the proxy forwarded.
func (p *Proxy) RequestCount() int64 { return p.requests.Load() }

func (p *Proxy) delay() time.Duration {
	delay := p.fault.Delay
	if p.fault.Jitter > 0 {
		p.randMu.Lock()
		delay += time.Duration(p.rng.Int63n(int64(p.fault.Jitter)))
		p.randMu.Unlock()
	}
	return delay
}

func (p *Proxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	p.requests.Add(1)
	if delay := p.delay(); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
	}

	outbound, err := http.NewRequestWithContext(r.Context(), r.Method, p.target+r.URL.RequestURI(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	copyHeaders(outbound.Header, r.Header)
	outbound.Header.Del("Connection")
	outbound.Header.Del("Keep-Alive")
	outbound.Header.Del("Transfer-Encoding")
	outbound.Header.Del("Upgrade")

	response, err := http.DefaultTransport.RoundTrip(outbound)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()

	copyHeaders(w.Header(), response.Header)
	w.Header().Del("Connection")
	w.Header().Del("Keep-Alive")
	w.Header().Del("Transfer-Encoding")
	w.WriteHeader(response.StatusCode)

	flush, _ := w.(http.Flusher)
	writer := &faultWriter{
		dst:       w,
		flush:     flush,
		fault:     p.fault,
		remaining: p.fault.DropAfter,
		started:   time.Now(),
	}
	_, _ = io.Copy(writer, response.Body)
}

func copyHeaders(dst, src http.Header) {
	for name, values := range src {
		if strings.EqualFold(name, "Content-Length") {
			continue
		}
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

// faultWriter applies throttling, fragmentation, and mid-body disconnects.
type faultWriter struct {
	dst       io.Writer
	flush     http.Flusher
	fault     Fault
	remaining int64
	written   int64
	started   time.Time
}

func (f *faultWriter) Write(p []byte) (int, error) {
	if f.fault.DropAfter > 0 {
		if f.remaining <= 0 {
			panic(http.ErrAbortHandler)
		}
		if int64(len(p)) > f.remaining {
			written, err := f.emit(p[:f.remaining])
			f.remaining = 0
			f.flushNow()
			if err != nil {
				return written, err
			}
			// Dropping the connection here is what a killed upstream looks
			// like: the client gets a short body and an unexpected EOF.
			panic(http.ErrAbortHandler)
		}
	}
	written, err := f.emit(p)
	f.remaining -= int64(written)
	return written, err
}

func (f *faultWriter) emit(p []byte) (int, error) {
	chunk := len(p)
	if f.fault.SplitWrites {
		chunk = 1
	} else if f.fault.Throttle > 0 && chunk > 4096 {
		chunk = 4096
	}
	if chunk <= 0 {
		return 0, nil
	}
	total := 0
	for total < len(p) {
		end := total + chunk
		if end > len(p) {
			end = len(p)
		}
		written, err := f.dst.Write(p[total:end])
		total += written
		f.written += int64(written)
		if f.fault.SplitWrites || f.fault.Throttle > 0 {
			f.flushNow()
		}
		f.throttle()
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (f *faultWriter) flushNow() {
	if f.flush != nil {
		f.flush.Flush()
	}
}

func (f *faultWriter) throttle() {
	if f.fault.Throttle <= 0 {
		return
	}
	expected := time.Duration(float64(f.written) / float64(f.fault.Throttle) * float64(time.Second))
	if elapsed := time.Since(f.started); elapsed < expected {
		time.Sleep(expected - elapsed)
	}
}
