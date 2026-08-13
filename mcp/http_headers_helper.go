package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpguts"
)

const (
	mcpHTTPHeadersHelperTimeout   = 10 * time.Second
	maxMCPHTTPHeadersHelperOutput = 64 * 1024
	mcpHTTPHeadersHelperOutputEnv = "CODEX_PLUGIN_METRICS_OUTPUT" // reserved by plugin metrics; kept separate for symmetry
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

	once    sync.Once
	mu      sync.Mutex
	headers http.Header
	err     error
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
	}, nil
}

func (t *mcpHTTPHeadersHelperTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t == nil || request == nil || request.URL == nil {
		return nil, fmt.Errorf("nil MCP HTTP headers helper request")
	}
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	if request.URL.Scheme+"://"+request.URL.Host == t.origin {
		headers, err := t.headersForRequest()
		if err != nil {
			return nil, err
		}
		for name, values := range headers {
			cloned.Header.Del(name)
			for _, value := range values {
				cloned.Header.Add(name, value)
			}
		}
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(cloned)
}

func (t *mcpHTTPHeadersHelperTransport) headersForRequest() (http.Header, error) {
	t.once.Do(func() {
		headers, err := runMCPHTTPHeadersHelper(t.command, t.cwd)
		t.mu.Lock()
		t.headers = headers
		t.err = err
		t.mu.Unlock()
	})
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.headers, t.err
}

func runMCPHTTPHeadersHelper(command string, cwd string) (http.Header, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mcpHTTPHeadersHelperTimeout)
	defer cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		comspec := os.Getenv("COMSPEC")
		if strings.TrimSpace(comspec) == "" {
			comspec = "cmd.exe"
		}
		cmd = exec.CommandContext(ctx, comspec, "/Q", "/D", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = cwd
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("MCP HTTP headers helper failed: %w", err)
	}
	if stdout.Len() > maxMCPHTTPHeadersHelperOutput {
		return nil, fmt.Errorf("MCP HTTP headers helper output exceeds 64 KiB")
	}
	return parseMCPHTTPHeadersHelperOutput(stdout.Bytes())
}

func parseMCPHTTPHeadersHelperOutput(data []byte) (http.Header, error) {
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("MCP HTTP headers helper must output a JSON object of strings")
	}
	headers := http.Header{}
	for name, value := range values {
		if mcpHTTPHeadersHelperReserved[strings.ToLower(name)] {
			return nil, fmt.Errorf("MCP HTTP headers helper returned a reserved header")
		}
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			return nil, fmt.Errorf("MCP HTTP headers helper returned an invalid header")
		}
		if headers.Get(name) != "" {
			return nil, fmt.Errorf("MCP HTTP headers helper returned duplicate header names")
		}
		headers.Set(name, value)
	}
	return headers, nil
}
