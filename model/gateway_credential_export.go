package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Rust parity: codex-rs/login/src/credential_export.rs and
// model-provider-info/src/gateway_oauth.rs (#59787).

const (
	defaultGatewayCredentialExportTimeoutMS uint64 = 30_000
	maxGatewayCredentialExportBytes                = 64 * 1024
)

// GatewayCredentialExportConfig is the `gateway_oauth.credential_export` source:
// a command that prints a gateway access token, used instead of browser OAuth.
type GatewayCredentialExportConfig struct {
	Command   string   `json:"command"`
	Args      []string `json:"args,omitempty"`
	TimeoutMS *uint64  `json:"timeout_ms,omitempty"`
}

// UsesCredentialExport reports whether the gateway credentials come from an
// export command rather than an interactive OAuth sign-in.
func (c *GatewayOAuthConfig) UsesCredentialExport() bool {
	return c != nil && c.CredentialExport != nil
}

// gatewayCredentialExport runs the configured command on first use and caches
// its token in memory. A failed or empty export is not cached, so the next
// request retries the command.
type gatewayCredentialExport struct {
	command string
	args    []string
	timeout time.Duration

	mu    sync.Mutex
	token string
}

func newGatewayCredentialExport(config *GatewayCredentialExportConfig) *gatewayCredentialExport {
	if config == nil {
		return nil
	}
	timeoutMS := defaultGatewayCredentialExportTimeoutMS
	if config.TimeoutMS != nil && *config.TimeoutMS > 0 {
		timeoutMS = *config.TimeoutMS
	}
	return &gatewayCredentialExport{
		command: strings.TrimSpace(config.Command),
		args:    append([]string(nil), config.Args...),
		timeout: time.Duration(timeoutMS) * time.Millisecond,
	}
}

// ResolveAccessToken returns the cached token, running the export command when no
// token is cached yet.
func (p *gatewayCredentialExport) ResolveAccessToken(ctx context.Context) (string, error) {
	if p == nil {
		return "", errors.New("gateway credential export is not configured")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.token != "" {
		return p.token, nil
	}
	token, err := p.export(ctx)
	if err != nil {
		return "", err
	}
	p.token = token
	return token, nil
}

// invalidate drops the cached token so a rejected credential is not reused.
func (p *gatewayCredentialExport) invalidate() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.token = ""
	p.mu.Unlock()
}

func (p *gatewayCredentialExport) export(ctx context.Context) (string, error) {
	if !validCredentialExportCommand(p.command) {
		return "", errors.New("gateway_oauth.credential_export command must be an absolute path or a bare executable name")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, p.command, p.args...)
	var stdout bytes.Buffer
	command.Stdout = &boundedWriter{buffer: &stdout, limit: maxGatewayCredentialExportBytes}
	command.Stderr = nil
	if err := command.Run(); err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("gateway credential export command timed out after %d ms", p.timeout.Milliseconds())
		}
		return "", fmt.Errorf("gateway credential export command failed: %v", err)
	}
	if stdout.Len() >= maxGatewayCredentialExportBytes {
		return "", errors.New("gateway credential export output exceeds 64 KiB")
	}
	token := strings.TrimSpace(stdout.String())
	if token == "" {
		return "", errors.New("gateway credential export returned an empty token")
	}
	return token, nil
}

func validCredentialExportCommand(command string) bool {
	if command == "" {
		return false
	}
	if filepath.IsAbs(command) {
		return true
	}
	return !strings.ContainsAny(command, `/\`) && filepath.Base(command) == command
}

type boundedWriter struct {
	buffer *bytes.Buffer
	limit  int
}

func (w *boundedWriter) Write(data []byte) (int, error) {
	remaining := w.limit - w.buffer.Len()
	if remaining <= 0 {
		return len(data), nil
	}
	if len(data) > remaining {
		w.buffer.Write(data[:remaining])
		return len(data), nil
	}
	w.buffer.Write(data)
	return len(data), nil
}
