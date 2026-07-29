package config

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
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

type CloudConfigFetchOptions struct {
	CodexHome     string
	BaseURL       string
	ChatGPTUserID string
	AccountID     string
	HTTPClient    CloudConfigHTTPDoer
	Headers       http.Header
	Authorize     func(context.Context, *http.Request) error
}

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
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	endpoint, err := cloudConfigBundleEndpoint(opts.BaseURL)
	if err != nil {
		return nil, NewCloudConfigLoadError(CloudConfigLoadInternal, nil, err.Error())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, NewCloudConfigLoadError(CloudConfigLoadInternal, nil, err.Error())
	}
	for name, values := range opts.Headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	if opts.Authorize != nil {
		if err := opts.Authorize(ctx, req); err != nil {
			return nil, NewCloudConfigLoadError(CloudConfigLoadAuth, nil, err.Error())
		}
	}
	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		code := CloudConfigLoadRequestFailed
		if ctx.Err() != nil {
			code = CloudConfigLoadTimeout
		}
		return nil, NewCloudConfigLoadError(code, nil, fmt.Sprintf("failed to load cloud config bundle: %v", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		return nil, NewCloudConfigLoadError(CloudConfigLoadRequestFailed, &status, fmt.Sprintf("failed to load cloud config bundle: HTTP %d", status))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, cloudConfigBundleMaxBytes+1))
	if err != nil {
		return nil, NewCloudConfigLoadError(CloudConfigLoadRequestFailed, nil, fmt.Sprintf("failed to read cloud config bundle: %v", err))
	}
	if len(data) > cloudConfigBundleMaxBytes {
		return nil, NewCloudConfigLoadError(CloudConfigLoadInvalidBundle, nil, "cloud config bundle exceeds size limit")
	}
	var bundle CloudConfigBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return nil, NewCloudConfigLoadError(CloudConfigLoadInvalidBundle, nil, fmt.Sprintf("invalid cloud config bundle: %v", err))
	}
	normalizeCloudConfigBundle(&bundle)
	if err := validateCloudConfigBundle(bundle, opts.CodexHome); err != nil {
		return nil, NewCloudConfigLoadError(CloudConfigLoadInvalidBundle, nil, err.Error())
	}
	_ = saveCloudConfigBundleCache(opts, bundle)
	return &bundle, nil
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

func saveCloudConfigBundleCache(opts CloudConfigFetchOptions, bundle CloudConfigBundle) error {
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
		return err
	}
	cache := cloudConfigBundleCacheFile{SignedPayload: payload, Signature: signCloudConfigCachePayload(payloadBytes)}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(opts.CodexHome, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(opts.CodexHome, cloudConfigBundleCacheFilename), append(data, '\n'), 0o600)
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
