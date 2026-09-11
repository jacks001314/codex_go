package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"codex_go/auth"
)

// Bedrock credential export (Rust #44028): a configured command emits SigV4
// signing credentials as JSON. Exports are cached in memory, refreshed before
// expiration, and shared across sessions with matching configuration. Command
// output is bounded and credential values never appear in errors.

const (
	awsCredentialDefaultLifetime = time.Hour
	awsCredentialRefreshWindow   = 5 * time.Minute
)

type awsExportedCredentials struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"`
}

type awsCredentialOutput struct {
	Credentials *awsExportedCredentials `json:"Credentials"`
}

type awsCachedCredentials struct {
	keys      auth.AWSAccessKeys
	refreshAt time.Time
}

// AWSCredentialExportProvider runs a configured credential-export command and
// caches the resulting AWS signing credentials.
type AWSCredentialExportProvider struct {
	command string
	args    []string
	timeout time.Duration

	mu     sync.Mutex
	cached *awsCachedCredentials
}

// Credentials returns cached credentials when they are still fresh, otherwise
// it runs the export command. Command execution is serialized so concurrent
// callers never run duplicate exports.
func (p *AWSCredentialExportProvider) Credentials(ctx context.Context) (auth.AWSAccessKeys, error) {
	if p == nil {
		return auth.AWSAccessKeys{}, errors.New("AWS credential export is not configured")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached != nil && time.Now().Before(p.cached.refreshAt) {
		return p.cached.keys, nil
	}
	cached, err := p.export(ctx, time.Now())
	if err != nil {
		p.cached = nil
		return auth.AWSAccessKeys{}, err
	}
	p.cached = cached
	return cached.keys, nil
}

// Refresh drops any cached credentials and performs a fresh export. Callers use
// it after the optional auth-refresh command runs so signing picks up rotated
// credentials.
func (p *AWSCredentialExportProvider) Refresh(ctx context.Context) error {
	if p == nil {
		return errors.New("AWS credential export is not configured")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cached = nil
	cached, err := p.export(ctx, time.Now())
	if err != nil {
		return err
	}
	p.cached = cached
	return nil
}

func (p *AWSCredentialExportProvider) export(ctx context.Context, now time.Time) (*awsCachedCredentials, error) {
	if strings.TrimSpace(p.command) == "" || !providerCommandIsAbsoluteOrBare(p.command) {
		return nil, errors.New("AWS credential export command must be an absolute path or a bare executable name")
	}
	timeout := p.timeout
	if timeout <= 0 {
		timeout = time.Duration(DefaultAWSCredentialExportTimeoutMS) * time.Millisecond
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append([]string(nil), p.args...)
	cmd := exec.CommandContext(execCtx, p.command, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to run AWS credential export command `%s`: %v", p.command, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to run AWS credential export command `%s`: %v", p.command, err)
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, int64(MaxAWSCredentialExportOutputBytes)+1))
	waitErr := cmd.Wait()
	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("AWS credential export command timed out after %d ms", timeout.Milliseconds())
	}
	if readErr != nil {
		return nil, errors.New("AWS credential export command output is unavailable")
	}
	if int64(len(output)) > MaxAWSCredentialExportOutputBytes {
		return nil, errors.New("AWS credential export output exceeds 64 KiB")
	}
	if waitErr != nil {
		return nil, fmt.Errorf("AWS credential export command exited with %v", waitErr)
	}
	return parseAWSCredentialOutput(output, now)
}

func parseAWSCredentialOutput(output []byte, now time.Time) (*awsCachedCredentials, error) {
	var wrapper awsCredentialOutput
	var credentials awsExportedCredentials
	if err := json.Unmarshal(output, &wrapper); err == nil && wrapper.Credentials != nil {
		credentials = *wrapper.Credentials
	} else if err := json.Unmarshal(output, &credentials); err != nil {
		// Serde's detailed errors may quote credential values; keep them out.
		return nil, errors.New("AWS credential export returned invalid credentials JSON")
	}
	if strings.TrimSpace(credentials.AccessKeyID) == "" || strings.TrimSpace(credentials.SecretAccessKey) == "" {
		return nil, errors.New("AWS credential export returned empty access keys")
	}
	refreshAt := now.Add(awsCredentialDefaultLifetime)
	if expirationText := strings.TrimSpace(credentials.Expiration); expirationText != "" {
		expiration, err := time.Parse(time.RFC3339, expirationText)
		if err != nil {
			return nil, errors.New("AWS credential export Expiration must be an RFC 3339 timestamp")
		}
		lifetime := expiration.Sub(now)
		if lifetime <= 0 {
			return nil, errors.New("AWS credential export returned expired credentials")
		}
		refreshAt = now.Add(lifetime - awsCredentialRefreshWindow)
	}
	return &awsCachedCredentials{
		keys: auth.AWSAccessKeys{
			AccessKeyID:     credentials.AccessKeyID,
			SecretAccessKey: credentials.SecretAccessKey,
			SessionToken:    credentials.SessionToken,
		},
		refreshAt: refreshAt,
	}, nil
}

var (
	awsCredentialExportRegistryMu sync.Mutex
	awsCredentialExportRegistry   = map[string]*AWSCredentialExportProvider{}
)

// credentialExportProviderForConfig returns the shared exporter for a matching
// AWS configuration so concurrent sessions reuse one credential cache.
func credentialExportProviderForConfig(config ProviderCredentialExportInfo) *AWSCredentialExportProvider {
	key := strings.Join(append([]string{config.Command}, config.Args...), "\x00") +
		fmt.Sprintf("\x00%d", config.TimeoutMS)
	awsCredentialExportRegistryMu.Lock()
	defer awsCredentialExportRegistryMu.Unlock()
	if provider, ok := awsCredentialExportRegistry[key]; ok {
		return provider
	}
	timeout := time.Duration(config.TimeoutMS) * time.Millisecond
	if config.TimeoutMS == 0 {
		timeout = time.Duration(DefaultAWSCredentialExportTimeoutMS) * time.Millisecond
	}
	provider := &AWSCredentialExportProvider{
		command: config.Command,
		args:    append([]string(nil), config.Args...),
		timeout: timeout,
	}
	awsCredentialExportRegistry[key] = provider
	return provider
}
