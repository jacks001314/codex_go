package netproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func target(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Target", "yes")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestProxyForwardsPathQueryAndBody(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		seen = r.Method + " " + r.URL.RequestURI() + " " + string(payload)
		w.Header().Set("X-Target", "yes")
		_, _ = io.WriteString(w, "pong")
	}))
	t.Cleanup(server.Close)

	proxy := New(t, server.URL, Fault{})
	response, err := http.Post(proxy.URL+"/v1/responses?trace=1", "application/json", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatalf("post through proxy: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "pong" {
		t.Fatalf("body = %q, want pong", body)
	}
	if got := response.Header.Get("X-Target"); got != "yes" {
		t.Fatalf("X-Target = %q, want yes", got)
	}
	if want := `POST /v1/responses?trace=1 {"a":1}`; seen != want {
		t.Fatalf("target saw %q, want %q", seen, want)
	}
	if proxy.RequestCount() != 1 {
		t.Fatalf("requests = %d, want 1", proxy.RequestCount())
	}
}

func TestProxyDelaysResponse(t *testing.T) {
	server := target(t, "late")
	proxy := New(t, server.URL, Fault{Delay: 200 * time.Millisecond})

	start := time.Now()
	response, err := http.Get(proxy.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer response.Body.Close()
	_, _ = io.ReadAll(response.Body)
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("elapsed = %v, want at least 150ms", elapsed)
	}
}

func TestProxyDropsMidBody(t *testing.T) {
	server := target(t, strings.Repeat("x", 1<<16))
	proxy := New(t, server.URL, Fault{DropAfter: 4096})

	response, err := http.Get(proxy.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err == nil {
		t.Fatalf("expected a read error after the drop, got %d bytes", len(body))
	}
	if len(body) != 4096 {
		t.Fatalf("body = %d bytes, want exactly 4096 forwarded bytes", len(body))
	}
}

func TestProxyThrottlesBody(t *testing.T) {
	const size = 16 << 10
	server := target(t, strings.Repeat("y", size))
	proxy := New(t, server.URL, Fault{Throttle: size / 2})

	start := time.Now()
	response, err := http.Get(proxy.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) != size {
		t.Fatalf("body = %d bytes, want %d", len(body), size)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("elapsed = %v, want at least 150ms for a 16KiB body at 8KiB/s", elapsed)
	}
}

func TestProxySplitWritesPreserveBody(t *testing.T) {
	server := target(t, strings.Repeat("z", 300))
	proxy := New(t, server.URL, Fault{SplitWrites: true})

	response, err := http.Get(proxy.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) != 300 || strings.Trim(string(body), "z") != "" {
		t.Fatalf("body = %d bytes (%.20q), want 300 z bytes", len(body), body)
	}
}
