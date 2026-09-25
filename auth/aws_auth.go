package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"

	"codex_go/network"
)

var (
	ErrAWSAuthEmptyService       = errors.New("AWS service name must not be empty")
	ErrAWSAuthMissingRegion      = errors.New("AWS region must not be empty")
	ErrAWSAuthMissingCredentials = errors.New("AWS credentials are required")
	// ErrAWSAuthPolicy reports an application network policy denial during AWS
	// authentication (Rust AwsAuthError::Policy); it is never retryable.
	ErrAWSAuthPolicy = errors.New("application network policy denied the AWS request")
)

// awsPolicyError classifies an application network policy denial and passes other
// errors through unchanged.
func awsPolicyError(err error) error {
	if err == nil {
		return nil
	}
	for _, denial := range []error{
		network.ErrNetworkPolicyUnavailable,
		network.ErrNetworkPolicyDestination,
		network.ErrNetworkPolicyRevoked,
		network.ErrNetworkPolicyUnsupportedTransport,
	} {
		if errors.Is(err, denial) {
			return fmt.Errorf("%w: %w", ErrAWSAuthPolicy, err)
		}
	}
	return err
}

type AWSAuthConfig struct {
	Profile string `json:"profile,omitempty"`
	Region  string `json:"region,omitempty"`
	Service string `json:"service"`
}

type AWSAuthCredentials struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	SessionToken    string `json:"sessionToken,omitempty"`
}

// AWSAccessKeys are exported SigV4 signing credentials supplied on demand.
type AWSAccessKeys struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// AWSCredentialsProvider supplies AWS access keys without exposing AWS SDK
// credential types to callers (Rust codex-aws-auth AwsCredentialsProvider).
// Implementations must return current credentials for every call so request
// signing observes refreshes, and errors must not contain secrets.
type AWSCredentialsProvider interface {
	Credentials(ctx context.Context) (AWSAccessKeys, error)
}

// awsCredentialsProviderAdapter adapts an AWSCredentialsProvider to the AWS
// SDK credential provider interface used for SigV4 signing.
type awsCredentialsProviderAdapter struct {
	provider AWSCredentialsProvider
	policy   network.NetworkPolicy
}

func (a awsCredentialsProviderAdapter) Retrieve(ctx context.Context) (awssdk.Credentials, error) {
	// Rust #47408: a caller-supplied exporter requires unrestricted application
	// policy, and its work is cancelled when the permission is revoked.
	permit, err := a.policy.AcquireForUnsupportedSDK()
	if err != nil {
		return awssdk.Credentials{}, awsPolicyError(err)
	}
	var keys AWSAccessKeys
	var providerErr error
	if _, err := network.RunWithNetworkPermit(ctx, permit, func(runCtx context.Context) struct{} {
		keys, providerErr = a.provider.Credentials(runCtx)
		return struct{}{}
	}); err != nil {
		return awssdk.Credentials{}, awsPolicyError(err)
	}
	if providerErr != nil {
		return awssdk.Credentials{}, awsPolicyError(providerErr)
	}
	return awssdk.Credentials{
		AccessKeyID:     strings.TrimSpace(keys.AccessKeyID),
		SecretAccessKey: strings.TrimSpace(keys.SecretAccessKey),
		SessionToken:    strings.TrimSpace(keys.SessionToken),
		Source:          "codex-bedrock-credential-export",
	}, nil
}

type AWSAuthRequestToSign struct {
	Method  string      `json:"method"`
	URL     string      `json:"url"`
	Headers http.Header `json:"headers,omitempty"`
	Body    []byte      `json:"body,omitempty"`
}

type AWSAuthSignedRequest struct {
	URL     string      `json:"url"`
	Headers http.Header `json:"headers"`
}

type AWSAuthContext struct {
	config      awssdk.Config
	credentials awssdk.CredentialsProvider
	region      string
	service     string
	// policy checks the signing destination before credentials are loaded and
	// cancels a credential fetch whose permission is revoked (Rust #47408).
	policy network.NetworkPolicy
}

// AWSAuthLoadOptions carries the host dependencies of the AWS config chain.
//
// Rust parity: codex-rs/aws-auth (see `transport.rs`, #47408) - the credential
// and region requests the AWS SDK makes are routed through the application-owned
// client, so a managed application network policy checks the destination before
// the request leaves the process.
type AWSAuthLoadOptions struct {
	// HTTPClient, when set, carries the application network policy (and the
	// configured proxy behaviour) for the SDK's credential and region requests.
	HTTPClient *http.Client
	// Policy is the application network policy the resolved context enforces:
	// signing checks the request destination before loading credentials, and a
	// caller-supplied credential exporter requires an unrestricted policy
	// because its I/O runs outside the shared AWS transport (Rust #47408).
	Policy network.NetworkPolicy
}

func (o *AWSAuthLoadOptions) awsConfigOptions() []func(*awsconfig.LoadOptions) error {
	if o == nil || o.HTTPClient == nil {
		return nil
	}
	return []func(*awsconfig.LoadOptions) error{awsconfig.WithHTTPClient(o.HTTPClient)}
}

// networkPolicy returns the application policy the resolved context enforces.
func (o *AWSAuthLoadOptions) networkPolicy() network.NetworkPolicy {
	if o == nil {
		return network.UnmanagedNetworkPolicy()
	}
	return o.Policy
}

func LoadAWSAuthContext(config *AWSAuthConfig) (*AWSAuthContext, error) {
	return LoadAWSAuthContextWithOptions(config, nil)
}

// LoadAWSAuthContextWithOptions mirrors `AwsAuthContext::load` with the host's
// application client, so the SDK's credential discovery honors the application
// network policy (Rust #47408).
func LoadAWSAuthContextWithOptions(config *AWSAuthConfig, options *AWSAuthLoadOptions) (*AWSAuthContext, error) {
	normalized, err := normalizeAWSAuthConfig(config)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	loadOptions := options.awsConfigOptions()
	if normalized.Profile != "" {
		loadOptions = append(loadOptions, awsconfig.WithSharedConfigProfile(normalized.Profile))
	}
	if normalized.Region != "" {
		loadOptions = append(loadOptions, awsconfig.WithRegion(normalized.Region))
	}
	loaded, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, err
	}
	region := strings.TrimSpace(loaded.Region)
	if region == "" {
		return nil, ErrAWSAuthMissingRegion
	}
	if loaded.Credentials == nil {
		return nil, ErrAWSAuthMissingCredentials
	}
	if _, err := loaded.Credentials.Retrieve(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAWSAuthMissingCredentials, err)
	}
	return &AWSAuthContext{
		config:      loaded,
		credentials: loaded.Credentials,
		region:      region,
		service:     normalized.Service,
		policy:      options.networkPolicy(),
	}, nil
}

// LoadAWSAuthContextWithProvider mirrors Rust
// AwsAuthContext::load_with_credentials_provider (#44028): resolve the region
// from the standard AWS config chain, then replace the SDK credential provider
// with a caller-supplied exporter so signing uses the exported credentials.
func LoadAWSAuthContextWithProvider(config *AWSAuthConfig, provider AWSCredentialsProvider) (*AWSAuthContext, error) {
	return LoadAWSAuthContextWithProviderAndOptions(config, provider, nil)
}

// LoadAWSAuthContextWithProviderAndOptions is
// LoadAWSAuthContextWithProvider with the host's application client, so the
// region lookup and the exported credential provider honor the application
// network policy (Rust #47408).
func LoadAWSAuthContextWithProviderAndOptions(config *AWSAuthConfig, provider AWSCredentialsProvider, options *AWSAuthLoadOptions) (*AWSAuthContext, error) {
	normalized, err := normalizeAWSAuthConfig(config)
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return LoadAWSAuthContextWithOptions(config, options)
	}
	ctx := context.Background()
	loadOptions := options.awsConfigOptions()
	if normalized.Profile != "" {
		loadOptions = append(loadOptions, awsconfig.WithSharedConfigProfile(normalized.Profile))
	}
	if normalized.Region != "" {
		loadOptions = append(loadOptions, awsconfig.WithRegion(normalized.Region))
	}
	loaded, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, err
	}
	region := strings.TrimSpace(loaded.Region)
	if region == "" {
		return nil, ErrAWSAuthMissingRegion
	}
	adapter := awsCredentialsProviderAdapter{provider: provider, policy: options.networkPolicy()}
	return &AWSAuthContext{
		config:      loaded,
		credentials: adapter,
		region:      region,
		service:     normalized.Service,
		policy:      options.networkPolicy(),
	}, nil
}

func NewAWSAuthContext(config *AWSAuthConfig, credentials *AWSAuthCredentials) (*AWSAuthContext, error) {
	normalized, err := normalizeAWSAuthConfig(config)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(normalized.Region) == "" {
		return nil, ErrAWSAuthMissingRegion
	}
	if err := validateAWSCredentials(credentials); err != nil {
		return nil, err
	}
	provider := awscredentials.NewStaticCredentialsProvider(
		strings.TrimSpace(credentials.AccessKeyID),
		strings.TrimSpace(credentials.SecretAccessKey),
		strings.TrimSpace(credentials.SessionToken),
	)
	awsConfig := awssdk.Config{
		Region:      normalized.Region,
		Credentials: provider,
	}
	return &AWSAuthContext{
		config:      awsConfig,
		credentials: provider,
		region:      normalized.Region,
		service:     normalized.Service,
	}, nil
}

func ResolveAWSRegion(config *AWSAuthConfig) (string, error) {
	return ResolveAWSRegionWithOptions(config, nil)
}

// ResolveAWSRegionWithOptions is ResolveAWSRegion with the host's application
// client, so the region lookup honors the application network policy
// (Rust #47408).
func ResolveAWSRegionWithOptions(config *AWSAuthConfig, loadOptions *AWSAuthLoadOptions) (string, error) {
	normalized, err := normalizeAWSAuthConfig(&AWSAuthConfig{
		Profile: stringFromAWSConfig(config, "profile"),
		Region:  stringFromAWSConfig(config, "region"),
		Service: firstNonEmptyAWS(stringFromAWSConfig(config, "service"), "sts"),
	})
	if err != nil {
		return "", err
	}
	ctx := context.Background()
	options := loadOptions.awsConfigOptions()
	if normalized.Profile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(normalized.Profile))
	}
	if normalized.Region != "" {
		options = append(options, awsconfig.WithRegion(normalized.Region))
	}
	loaded, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return "", err
	}
	region := strings.TrimSpace(loaded.Region)
	if region == "" {
		return "", ErrAWSAuthMissingRegion
	}
	return region, nil
}

func ResolveAWSCredentials(config *AWSAuthConfig) (*AWSAuthCredentials, error) {
	normalized, err := normalizeAWSAuthConfig(&AWSAuthConfig{
		Profile: stringFromAWSConfig(config, "profile"),
		Region:  stringFromAWSConfig(config, "region"),
		Service: firstNonEmptyAWS(stringFromAWSConfig(config, "service"), "sts"),
	})
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	options := []func(*awsconfig.LoadOptions) error{}
	if normalized.Profile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(normalized.Profile))
	}
	if normalized.Region != "" {
		options = append(options, awsconfig.WithRegion(normalized.Region))
	}
	loaded, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	if loaded.Credentials == nil {
		return nil, ErrAWSAuthMissingCredentials
	}
	credentials, err := loaded.Credentials.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAWSAuthMissingCredentials, err)
	}
	out := &AWSAuthCredentials{
		AccessKeyID:     strings.TrimSpace(credentials.AccessKeyID),
		SecretAccessKey: strings.TrimSpace(credentials.SecretAccessKey),
		SessionToken:    strings.TrimSpace(credentials.SessionToken),
	}
	if err := validateAWSCredentials(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *AWSAuthContext) Region() string {
	if c == nil {
		return ""
	}
	return c.region
}

func (c *AWSAuthContext) Service() string {
	if c == nil {
		return ""
	}
	return c.service
}

func (c *AWSAuthContext) Sign(request *AWSAuthRequestToSign) (*AWSAuthSignedRequest, error) {
	return c.SignAt(request, time.Now().UTC())
}

func (c *AWSAuthContext) SignAt(request *AWSAuthRequestToSign, at time.Time) (*AWSAuthSignedRequest, error) {
	if c == nil || strings.TrimSpace(c.service) == "" {
		return nil, ErrAWSAuthEmptyService
	}
	if strings.TrimSpace(c.region) == "" {
		return nil, ErrAWSAuthMissingRegion
	}
	if c.credentials == nil {
		return nil, ErrAWSAuthMissingCredentials
	}
	// Rust #47408: check the signing destination before loading credentials, then
	// load them under the permit so a revocation cancels the fetch.
	ctx := context.Background()
	permit, err := c.signingPermit(request)
	if err != nil {
		return nil, err
	}
	var credentials awssdk.Credentials
	var credentialErr error
	_, err = network.RunWithNetworkPermit(ctx, permit, func(runCtx context.Context) struct{} {
		credentials, credentialErr = c.credentials.Retrieve(runCtx)
		return struct{}{}
	})
	if err != nil {
		if policyErr := awsPolicyError(err); errors.Is(policyErr, ErrAWSAuthPolicy) {
			return nil, policyErr
		}
		return nil, fmt.Errorf("%w: %v", ErrAWSAuthMissingCredentials, err)
	}
	if credentialErr != nil {
		if policyErr := awsPolicyError(credentialErr); errors.Is(policyErr, ErrAWSAuthPolicy) {
			return nil, policyErr
		}
		return nil, fmt.Errorf("%w: %v", ErrAWSAuthMissingCredentials, credentialErr)
	}
	return SignAWSRequestWithCredentials(&credentials, c.region, c.service, request, at)
}

// signingPermit authorizes the request destination, mirroring Rust's
// `network_policy.acquire(&url)` ahead of `provide_credentials()`.
func (c *AWSAuthContext) signingPermit(request *AWSAuthRequestToSign) (*network.NetworkPermit, error) {
	if c == nil || !c.policy.IsScoped() {
		return network.UnmanagedNetworkPolicy().Acquire(awsRequestURL(request))
	}
	permit, err := c.policy.Acquire(awsRequestURL(request))
	if err != nil {
		return nil, awsPolicyError(err)
	}
	return permit, nil
}

func awsRequestURL(request *AWSAuthRequestToSign) *url.URL {
	if request == nil {
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(request.URL))
	if err != nil {
		return nil
	}
	return parsed
}

func SignAWSRequest(credentials *AWSAuthCredentials, region string, service string, request *AWSAuthRequestToSign, at time.Time) (*AWSAuthSignedRequest, error) {
	if err := validateAWSCredentials(credentials); err != nil {
		return nil, err
	}
	awsCredentials := awssdk.Credentials{
		AccessKeyID:     strings.TrimSpace(credentials.AccessKeyID),
		SecretAccessKey: strings.TrimSpace(credentials.SecretAccessKey),
		SessionToken:    strings.TrimSpace(credentials.SessionToken),
		Source:          "codex-static",
	}
	return SignAWSRequestWithCredentials(&awsCredentials, region, service, request, at)
}

func SignAWSRequestWithCredentials(credentials *awssdk.Credentials, region string, service string, request *AWSAuthRequestToSign, at time.Time) (*AWSAuthSignedRequest, error) {
	if strings.TrimSpace(service) == "" {
		return nil, ErrAWSAuthEmptyService
	}
	if strings.TrimSpace(region) == "" {
		return nil, ErrAWSAuthMissingRegion
	}
	if credentials == nil || strings.TrimSpace(credentials.AccessKeyID) == "" || strings.TrimSpace(credentials.SecretAccessKey) == "" {
		return nil, ErrAWSAuthMissingCredentials
	}
	credentials = &awssdk.Credentials{
		AccessKeyID:     strings.TrimSpace(credentials.AccessKeyID),
		SecretAccessKey: strings.TrimSpace(credentials.SecretAccessKey),
		SessionToken:    strings.TrimSpace(credentials.SessionToken),
		Source:          strings.TrimSpace(credentials.Source),
		CanExpire:       credentials.CanExpire,
		Expires:         credentials.Expires,
		AccountID:       strings.TrimSpace(credentials.AccountID),
	}
	if request == nil {
		request = &AWSAuthRequestToSign{}
	}
	parsed, err := url.Parse(strings.TrimSpace(request.URL))
	if err != nil {
		return nil, err
	}
	method := strings.TrimSpace(request.Method)
	if method == "" {
		method = http.MethodGet
	}
	headers := cloneAWSHeader(request.Headers)
	payloadHash := sha256HexAWS(request.Body)
	httpRequest := &http.Request{
		Method:        strings.ToUpper(method),
		URL:           parsed,
		Header:        headers,
		Body:          io.NopCloser(bytes.NewReader(request.Body)),
		ContentLength: int64(len(request.Body)),
		Host:          parsed.Host,
	}
	signer := awsv4.NewSigner()
	if err := signer.SignHTTP(context.Background(), *credentials, httpRequest, payloadHash, strings.TrimSpace(service), strings.TrimSpace(region), at.UTC()); err != nil {
		return nil, err
	}
	return &AWSAuthSignedRequest{
		URL:     httpRequest.URL.String(),
		Headers: httpRequest.Header,
	}, nil
}

func normalizeAWSAuthConfig(config *AWSAuthConfig) (*AWSAuthConfig, error) {
	if config == nil {
		config = &AWSAuthConfig{}
	}
	service := strings.TrimSpace(config.Service)
	if service == "" {
		return nil, ErrAWSAuthEmptyService
	}
	return &AWSAuthConfig{
		Profile: strings.TrimSpace(config.Profile),
		Region:  strings.TrimSpace(config.Region),
		Service: service,
	}, nil
}

func validateAWSCredentials(credentials *AWSAuthCredentials) error {
	if credentials == nil {
		return ErrAWSAuthMissingCredentials
	}
	if strings.TrimSpace(credentials.AccessKeyID) == "" || strings.TrimSpace(credentials.SecretAccessKey) == "" {
		return ErrAWSAuthMissingCredentials
	}
	return nil
}

func cloneAWSHeader(headers http.Header) http.Header {
	cloned := http.Header{}
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func sha256HexAWS(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func stringFromAWSConfig(config *AWSAuthConfig, key string) string {
	if config == nil {
		return ""
	}
	switch key {
	case "profile":
		return config.Profile
	case "region":
		return config.Region
	case "service":
		return config.Service
	default:
		return ""
	}
}

func firstNonEmptyAWS(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
