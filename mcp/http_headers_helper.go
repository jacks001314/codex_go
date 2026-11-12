package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"codex_go/headershelper"
)

var mcpHTTPHeadersHelperReserved = map[string]bool{
	"accept": true, "authorization": true, "connection": true,
	"content-encoding": true, "content-length": true, "content-type": true,
	"host": true, "keep-alive": true, "last-event-id": true,
	"mcp-protocol-version": true, "mcp-session-id": true, "origin": true,
	"proxy-connection": true, "referer": true, "te": true, "trailer": true,
	"transfer-encoding": true, "upgrade": true,
}

type mcpHTTPHeadersHelperTransport struct {
	base    http.RoundTripper
	origin  string
	command string
	cwd     string
	run     func(context.Context, string, string) (http.Header, error)

	once sync.Once
	mu   sync.Mutex
	// headers is the last successful helper response. epoch advances after every
	// refresh attempt (success, failure, or unchanged) so concurrent rejections
	// from the same cohort share one helper invocation.
	headers     http.Header
	err         error
	epoch       uint64
	refreshing  bool
	refreshDone chan struct{}
	refreshErr  error
}

func newMCPHTTPHeadersHelperTransport(base http.RoundTripper, serverURL string, command string, cwd string) (*mcpHTTPHeadersHelperTransport, error) {
	parsed, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil {
		return nil, fmt.Errorf("invalid MCP HTTP headers helper URL: %w", err)
	}
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("http_headers_helper must not be empty")
	}
	return &mcpHTTPHeadersHelperTransport{
		base:    base,
		origin:  parsed.Scheme + "://" + parsed.Host,
		command: command,
		cwd:     cwd,
		run:     runMCPHTTPHeadersHelperContext,
	}, nil
}

func (t *mcpHTTPHeadersHelperTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t == nil || request == nil || request.URL == nil {
		return nil, fmt.Errorf("nil MCP HTTP headers helper request")
	}
	if request.URL.Scheme+"://"+request.URL.Host != t.origin {
		return t.roundTrip(request, nil)
	}
	headers, epoch, err := t.headersForRequest()
	if err != nil {
		return nil, err
	}
	response, err := t.roundTrip(request, headers)
	if err != nil || response == nil || request.Method != http.MethodPost || (response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusForbidden) {
		return response, err
	}
	refreshed, nextEpoch, refreshErr := t.refreshHeaders(request.Context(), epoch)
	if refreshErr != nil || nextEpoch == epoch || headersEqual(headers, refreshed) {
		return response, err
	}
	_ = response.Body.Close()
	return t.roundTrip(request, refreshed)
}

func (t *mcpHTTPHeadersHelperTransport) roundTrip(request *http.Request, headers http.Header) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	explicitAuthorization := strings.TrimSpace(cloned.Header.Get("Authorization")) != ""
	for name, values := range headers {
		if explicitAuthorization && strings.EqualFold(name, "Authorization") {
			continue
		}
		cloned.Header.Del(name)
		for _, value := range values {
			cloned.Header.Add(name, value)
		}
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(cloned)
}

func (t *mcpHTTPHeadersHelperTransport) headersForRequest() (http.Header, uint64, error) {
	t.once.Do(func() {
		headers, err := t.run(context.Background(), t.command, t.cwd)
		t.mu.Lock()
		t.headers = headers
		t.err = err
		t.mu.Unlock()
	})
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneHTTPHeaderForHelper(t.headers), t.epoch, t.err
}

func (t *mcpHTTPHeadersHelperTransport) refreshHeaders(ctx context.Context, rejectedEpoch uint64) (http.Header, uint64, error) {
	t.mu.Lock()
	if t.epoch != rejectedEpoch {
		headers, epoch, err := cloneHTTPHeaderForHelper(t.headers), t.epoch, t.refreshErr
		t.mu.Unlock()
		return headers, epoch, err
	}
	if !t.refreshing {
		t.refreshing = true
		t.refreshDone = make(chan struct{})
		t.refreshErr = nil
		done := t.refreshDone
		command, cwd := t.command, t.cwd
		go func() {
			headers, err := t.run(context.Background(), command, cwd)
			t.mu.Lock()
			if err == nil && headers != nil {
				t.headers = headers
			}
			t.refreshErr = err
			t.epoch = rejectedEpoch + 1
			t.refreshing = false
			close(done)
			t.mu.Unlock()
		}()
	}
	done := t.refreshDone
	t.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return nil, rejectedEpoch, ctx.Err()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneHTTPHeaderForHelper(t.headers), t.epoch, t.refreshErr
}

func headersEqual(left http.Header, right http.Header) bool {
	if len(left) != len(right) {
		return false
	}
	for name, values := range left {
		other := right.Values(name)
		if len(other) != len(values) {
			return false
		}
		for index := range values {
			if other[index] != values[index] {
				return false
			}
		}
	}
	return true
}

func cloneHTTPHeaderForHelper(header http.Header) http.Header {
	if header == nil {
		return nil
	}
	return header.Clone()
}

// runMCPHTTPHeadersHelper executes the helper through the shared runner and
// keeps MCP's reserved-header contract (Rust #61018 keeps that validation in
// rmcp-client).
func runMCPHTTPHeadersHelper(command string, cwd string) (http.Header, error) {
	return runMCPHTTPHeadersHelperContext(context.Background(), command, cwd)
}

func runMCPHTTPHeadersHelperContext(ctx context.Context, command string, cwd string) (http.Header, error) {
	headers, err := headershelper.Run(ctx, command, cwd)
	if err != nil {
		return nil, fmt.Errorf("MCP HTTP headers helper failed: %w", err)
	}
	return mcpHTTPHeadersFromHelper(headers)
}

// parseMCPHTTPHeadersHelperOutput parses one helper response and applies MCP's
// reserved-header contract.
func parseMCPHTTPHeadersHelperOutput(data []byte) (http.Header, error) {
	headers, err := headershelper.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("MCP HTTP headers helper failed: %w", err)
	}
	return mcpHTTPHeadersFromHelper(headers)
}

func mcpHTTPHeadersFromHelper(helperHeaders headershelper.Headers) (http.Header, error) {
	headers := helperHeaders.HTTPHeader()
	for name := range headers {
		if mcpHTTPHeadersHelperReserved[strings.ToLower(name)] {
			return nil, fmt.Errorf("MCP HTTP headers helper returned a reserved header")
		}
	}
	return headers, nil
}
