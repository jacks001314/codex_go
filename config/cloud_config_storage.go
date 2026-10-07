package config

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex_go/network"
)

const (
	cloudConfigBundleCacheFilename = "cloud-config-bundle-cache.json"
	cloudConfigBundleCacheVersion  = 1
	cloudConfigBundleMaxBytes      = 16 << 20
)

var cloudConfigBundleCacheHMACKey = []byte("codex-cloud-config-bundle-cache-v1-6160ae70-bcfd-4ca8-a99b-40f73b3b072e")

type CloudConfigHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// scopeCloudConfigFetchOptions restricts the bootstrap clients to the exact
// config bundle endpoint, mirroring Rust's
// `restrict_to_endpoints(config_bundle_url)`. The endpoint scope is enforced
// even without a policy owner, so a bootstrap client that was handed an
// unrestricted policy still cannot reach another URL.
func scopeCloudConfigFetchOptions(opts CloudConfigFetchOptions) CloudConfigFetchOptions {
	endpoint, err := cloudConfigBundleEndpoint(opts.BaseURL)
	if err != nil {
		return opts
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return opts
	}
	policy := network.UnmanagedNetworkPolicy()
	if opts.NetworkPolicy != nil {
		policy = *opts.NetworkPolicy
	}
	policy = policy.RestrictToEndpoints([]*url.URL{parsed})
	if opts.HTTPClient != nil {
		opts.HTTPClient = &network.PolicyHTTPDoer{Policy: policy, Next: opts.HTTPClient}
	}
	if opts.FallbackHTTPClient != nil {
		opts.FallbackHTTPClient = &network.PolicyHTTPDoer{Policy: policy, Next: opts.FallbackHTTPClient}
	}
	return opts
}

type CloudConfigFetchOptions struct {
	CodexHome     string
	BaseURL       string
	ChatGPTUserID string
	AccountID     string
	HTTPClient    CloudConfigHTTPDoer
	// FallbackHTTPClient, when set, retries the GET through the system proxy
	// after the primary client fails to connect or times out (Rust #46562).
	FallbackHTTPClient CloudConfigHTTPDoer
	// NetworkPolicy, when set, is the application network policy the bootstrap
	// GET is bound to. Either way the fetch is scoped to the exact config bundle
	// endpoint, so the local policy limits cloud bootstrap
	// (Rust ConfigManager::replace_cloud_config_bundle_loader).
	NetworkPolicy *network.NetworkPolicy
	Headers       http.Header
	Authorize     func(context.Context, *http.Request) error
	// Policy, when set, is the enterprise policy owner this fetch observes.
	// Observing a changed bundle suspends the previous revision before the
	// bundle is validated or cached, and the cache write is published only
	// while that observation is still current (Rust #49269
	// CloudConfigBundleService::policy). A nil policy uses a one-shot policy
	// that keeps every write on the staged atomic path.
	Policy *CloudConfigBundlePolicy
}

// cloudConfigFallbackAttemptTimeout bounds the primary bootstrap GET, including
// its body read, so the system-proxy retry still fits the loader's budget
// (Rust's five-second bootstrap attempt).
const cloudConfigFallbackAttemptTimeout = 5 * time.Second

type cloudConfigBundleCacheFile struct {
	SignedPayload cloudConfigBundleCacheSignedPayload `json:"signed_payload"`
	Signature     string                              `json:"signature"`
}

type cloudConfigBundleCacheSignedPayload struct {
	Version       int               `json:"version"`
	CachedAt      time.Time         `json:"cached_at"`
	ExpiresAt     time.Time         `json:"expires_at"`
	ChatGPTUserID *string           `json:"chatgpt_user_id"`
	AccountID     *string           `json:"account_id"`
	Bundle        CloudConfigBundle `json:"bundle"`
}

func LoadCloudConfigBundle(ctx context.Context, opts CloudConfigFetchOptions) (*CloudConfigBundle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cached := loadCloudConfigBundleCache(opts); cached != nil {
		return cached, nil
	}
	opts = scopeCloudConfigFetchOptions(opts)
	if opts.FallbackHTTPClient != nil {
		// Bound the primary attempt, then retry through the system proxy when it
		// failed to connect or timed out. A caller-cancelled context stops here.
		attemptCtx, cancel := context.WithTimeout(ctx, cloudConfigFallbackAttemptTimeout)
		bundle, retryable, err := loadCloudConfigBundleAttempt(attemptCtx, opts, opts.HTTPClient)
		cancel()
		if err == nil || !retryable || ctx.Err() != nil {
			return bundle, err
		}
		bundle, _, err = loadCloudConfigBundleAttempt(ctx, opts, opts.FallbackHTTPClient)
		return bundle, err
	}
	bundle, _, err := loadCloudConfigBundleAttempt(ctx, opts, opts.HTTPClient)
	return bundle, err
}

// loadCloudConfigBundleAttempt performs one bootstrap GET. The retryable result
// reports a connection failure or timeout, the only failures the system-proxy
// fallback retries; an HTTP status or decode error is final.
func loadCloudConfigBundleAttempt(ctx context.Context, opts CloudConfigFetchOptions, httpClient CloudConfigHTTPDoer) (*CloudConfigBundle, bool, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	endpoint, err := cloudConfigBundleEndpoint(opts.BaseURL)
	if err != nil {
		return nil, false, NewCloudConfigLoadError(CloudConfigLoadInternal, nil, err.Error())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, NewCloudConfigLoadError(CloudConfigLoadInternal, nil, err.Error())
	}
	for name, values := range opts.Headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	if opts.Authorize != nil {
		if err := opts.Authorize(ctx, req); err != nil {
			return nil, false, NewCloudConfigLoadError(CloudConfigLoadAuth, nil, err.Error())
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		code := CloudConfigLoadRequestFailed
		if ctx.Err() != nil {
			code = CloudConfigLoadTimeout
		}
		return nil, cloudConfigRetryableTransportError(err, ctx), NewCloudConfigLoadError(code, nil, fmt.Sprintf("failed to load cloud config bundle: %v", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		return nil, false, NewCloudConfigLoadError(CloudConfigLoadRequestFailed, &status, fmt.Sprintf("failed to load cloud config bundle: HTTP %d", status))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, cloudConfigBundleMaxBytes+1))
	if err != nil {
		return nil, cloudConfigRetryableTransportError(err, ctx), NewCloudConfigLoadError(CloudConfigLoadRequestFailed, nil, fmt.Sprintf("failed to read cloud config bundle: %v", err))
	}
	if len(data) > cloudConfigBundleMaxBytes {
		return nil, false, NewCloudConfigLoadError(CloudConfigLoadInvalidBundle, nil, "cloud config bundle exceeds size limit")
	}
	var bundle CloudConfigBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return nil, false, NewCloudConfigLoadError(CloudConfigLoadInvalidBundle, nil, fmt.Sprintf("invalid cloud config bundle: %v", err))
	}
	normalizeCloudConfigBundle(&bundle)
	// Rust #49269: observing the delivered bundle suspends the previous policy
	// revision before the bundle is validated or cached. A standalone fetch has
	// no owning service, so it uses a one-shot policy: the observation always
	// stays current while every write still goes through the staged, atomic
	// publication path.
	policy := opts.Policy
	if policy == nil {
		policy = NewCloudConfigBundlePolicy()
	}
	revision := policy.ObserveRemoteBundle(&bundle)
	if err := validateCloudConfigBundle(bundle, opts.CodexHome); err != nil {
		return nil, false, NewCloudConfigLoadError(CloudConfigLoadInvalidBundle, nil, err.Error())
	}
	// Stage the cache payload and publish it only while the observation is
	// still current. A retired policy has no revision, so nothing is published
	// (Rust #49269 CloudConfigBundleService::apply_bundle).
	if revision != nil {
		if staged, err := prepareCloudConfigBundleCache(opts, bundle); err == nil {
			_ = staged.PublishIfCurrent(revision)
		}
	}
	return &bundle, false, nil
}

// cloudConfigRetryableTransportError reports a connection failure, timeout, or
// the attempt's own deadline. A timeout while reading a stalled body is
// retryable for a GET (Rust #46562).
func cloudConfigRetryableTransportError(err error, ctx context.Context) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded))
}

func cloudConfigBundleEndpoint(baseURL string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = DefaultChatGPTBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid ChatGPT base URL %q", baseURL)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/wham/config/bundle"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func validateCloudConfigBundle(bundle CloudConfigBundle, baseDir string) error {
	if _, err := CloudConfigLayersFromBundle(bundle, baseDir); err != nil {
		return err
	}
	for _, fragment := range bundle.RequirementsTOML.EnterpriseManaged {
		if _, err := ParseRequirementsTOML([]byte(fragment.Contents)); err != nil {
			return fmt.Errorf("%w: failed to parse cloud requirements fragment %s (%s): %s", ErrInvalidCloudConfig, fragment.Name, fragment.ID, err)
		}
	}
	return nil
}

func loadCloudConfigBundleCache(opts CloudConfigFetchOptions) *CloudConfigBundle {
	userID := strings.TrimSpace(opts.ChatGPTUserID)
	accountID := strings.TrimSpace(opts.AccountID)
	if userID == "" || accountID == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(opts.CodexHome, cloudConfigBundleCacheFilename))
	if err != nil {
		return nil
	}
	var cache cloudConfigBundleCacheFile
	if json.Unmarshal(data, &cache) != nil || cache.SignedPayload.Version != cloudConfigBundleCacheVersion {
		return nil
	}
	payload, err := marshalCloudConfigCachePayload(cache.SignedPayload)
	if err != nil || !verifyCloudConfigCacheSignature(payload, cache.Signature) {
		return nil
	}
	if cache.SignedPayload.ChatGPTUserID == nil || cache.SignedPayload.AccountID == nil ||
		strings.TrimSpace(*cache.SignedPayload.ChatGPTUserID) != userID || strings.TrimSpace(*cache.SignedPayload.AccountID) != accountID ||
		!cache.SignedPayload.ExpiresAt.After(time.Now()) {
		return nil
	}
	if validateCloudConfigBundle(cache.SignedPayload.Bundle, opts.CodexHome) != nil {
		return nil
	}
	bundle := cache.SignedPayload.Bundle
	return &bundle
}

// stagedCloudConfigBundleCache is a cache payload written to a temporary file
// next to its destination but not yet visible there (Rust #49269
// StagedCloudConfigBundleCache). The temporary file lives in the destination
// directory so publication is a same-filesystem atomic rename.
type stagedCloudConfigBundleCache struct {
	temporaryPath string
	destination   string
}

// prepareCloudConfigBundleCache signs the bundle and stages the cache payload
// in a temporary file. It never touches the destination: a reader of the cache
// path either sees the previously published payload or, after
// PublishIfCurrent, the complete new one.
func prepareCloudConfigBundleCache(opts CloudConfigFetchOptions, bundle CloudConfigBundle) (*stagedCloudConfigBundleCache, error) {
	if strings.TrimSpace(opts.CodexHome) == "" {
		return nil, errors.New("cloud config bundle cache requires a codex home")
	}
	normalizeCloudConfigBundle(&bundle)
	now := time.Now().UTC()
	userID := stringPtr(strings.TrimSpace(opts.ChatGPTUserID))
	accountID := stringPtr(strings.TrimSpace(opts.AccountID))
	payload := cloudConfigBundleCacheSignedPayload{
		Version:       cloudConfigBundleCacheVersion,
		CachedAt:      now,
		ExpiresAt:     now.Add(time.Hour),
		ChatGPTUserID: userID,
		AccountID:     accountID,
		Bundle:        bundle,
	}
	payloadBytes, err := marshalCloudConfigCachePayload(payload)
	if err != nil {
		return nil, err
	}
	cache := cloudConfigBundleCacheFile{SignedPayload: payload, Signature: signCloudConfigCachePayload(payloadBytes)}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(opts.CodexHome, 0o700); err != nil {
		return nil, err
	}
	destination := filepath.Join(opts.CodexHome, cloudConfigBundleCacheFilename)
	temporary, err := os.CreateTemp(opts.CodexHome, cloudConfigBundleCacheFilename+".tmp-")
	if err != nil {
		return nil, err
	}
	staged := &stagedCloudConfigBundleCache{temporaryPath: temporary.Name(), destination: destination}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		staged.Discard()
		return nil, err
	}
	// os.CreateTemp already creates the file with 0600; keep the published
	// cache private to the user even if the process umask is permissive.
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		staged.Discard()
		return nil, err
	}
	if err := temporary.Close(); err != nil {
		staged.Discard()
		return nil, err
	}
	return staged, nil
}

// PublishIfCurrent atomically publishes the staged payload over the destination
// only while revision is still current. A superseded or retired revision leaves
// the destination untouched and drops the staged file, so an older refresh can
// never overwrite a newer one (Rust #49269
// StagedCloudConfigBundleCache::publish_if_current).
func (s *stagedCloudConfigBundleCache) PublishIfCurrent(revision *CloudConfigBundlePolicyRevision) error {
	if s == nil {
		return nil
	}
	published := false
	err := revision.CommitIfCurrent(func() error {
		if err := os.Rename(s.temporaryPath, s.destination); err != nil {
			return err
		}
		published = true
		return nil
	})
	if err != nil || !published {
		s.Discard()
	}
	if err != nil {
		return err
	}
	return nil
}

// Discard drops the staged file without touching the destination.
func (s *stagedCloudConfigBundleCache) Discard() {
	if s == nil || s.temporaryPath == "" {
		return
	}
	_ = os.Remove(s.temporaryPath)
	s.temporaryPath = ""
}

func normalizeCloudConfigBundle(bundle *CloudConfigBundle) {
	if bundle == nil {
		return
	}
	if bundle.ConfigTOML.EnterpriseManaged == nil {
		bundle.ConfigTOML.EnterpriseManaged = []CloudConfigFragment{}
	}
	if bundle.RequirementsTOML.EnterpriseManaged == nil {
		bundle.RequirementsTOML.EnterpriseManaged = []CloudConfigFragment{}
	}
}

func marshalCloudConfigCachePayload(payload cloudConfigBundleCacheSignedPayload) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(output.Bytes(), []byte{'\n'}), nil
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func signCloudConfigCachePayload(payload []byte) string {
	mac := hmac.New(sha256.New, cloudConfigBundleCacheHMACKey)
	_, _ = mac.Write(payload)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func verifyCloudConfigCacheSignature(payload []byte, signature string) bool {
	actual, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, cloudConfigBundleCacheHMACKey)
	_, _ = mac.Write(payload)
	return hmac.Equal(actual, mac.Sum(nil))
}
