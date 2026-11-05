package runtimeauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"codex_go/auth"
)

const (
	maxRegistrationBodyBytes = 64 * 1024
	registrationTimeout      = 30 * time.Second
)

// RegistrationRequest is the composite one-call registration body. Credentials
// travel only in this body, never through redirects (Rust
// RuntimeRegistrationRequest).
type RegistrationRequest struct {
	ActorAuth     any                  `json:"actor_auth"`
	AgentHostAuth any                  `json:"agent_host_auth"`
	PublicKey     string               `json:"public_key"`
	Metadata      registrationMetadata `json:"metadata"`
}

// Registration is one initialization, including retries. Neither the signing
// key nor the resulting runtime is persisted.
type Registration struct {
	initializationID string
	request          RegistrationRequest
	signingKey       ed25519.PrivateKey
}

// NewRegistration generates the runtime signing key and the idempotency id for
// one initialization.
func NewRegistration(snapshot *auth.AuthDotJSON, selection Selection, installationID string, metadata Metadata) (*Registration, error) {
	actorAuth, err := actorAuthorization(snapshot, selection)
	if err != nil {
		return nil, err
	}
	hostAuth, err := agentHostAuthorization(installationID)
	if err != nil {
		return nil, err
	}
	key, err := newSigningKey()
	if err != nil {
		return nil, err
	}
	publicKey := key.Public().(ed25519.PublicKey)
	return &Registration{
		initializationID: newUUIDv4(),
		request: RegistrationRequest{
			ActorAuth:     actorAuth,
			AgentHostAuth: hostAuth,
			PublicKey:     encodeSSHEd25519PublicKey(publicKey),
			Metadata:      metadata.wire(),
		},
		signingKey: key,
	}, nil
}

// refreshActor swaps in the actor credential of a later attempt while keeping
// the key and idempotency id of the pending registration.
func (r *Registration) refreshActor(snapshot *auth.AuthDotJSON, selection Selection) error {
	actorAuth, err := actorAuthorization(snapshot, selection)
	if err != nil {
		return err
	}
	r.request.ActorAuth = actorAuth
	return nil
}

func (r *Registration) finish(info *RegistrationResponse) *Instance {
	return &Instance{
		id:         info.RuntimeAgentInstanceID,
		actor:      info.Actor,
		hostID:     info.AgentHostID,
		signingKey: r.signingKey,
	}
}

type oauthActorAuthorization struct {
	Type       string `json:"type"`
	Credential string `json:"credential"`
}

type personalAccessTokenAuthorization struct {
	Type       string `json:"type"`
	Credential string `json:"credential"`
}

type platformAPIKeyAuthorization struct {
	Type           string `json:"type"`
	Credential     string `json:"credential"`
	OrganizationID string `json:"organization_id,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
}

type installationIDAuthorization struct {
	Type           string `json:"type"`
	InstallationID string `json:"installation_id"`
}

func agentHostAuthorization(installationID string) (any, error) {
	installationID = strings.TrimSpace(installationID)
	if installationID == "" {
		return nil, ErrInvalidRequest
	}
	return installationIDAuthorization{Type: "installation_id", InstallationID: installationID}, nil
}

// actorAuthorization maps the selected credential to its registration shape
// (Rust actor_authorization). Only API keys may select organization or project.
func actorAuthorization(snapshot *auth.AuthDotJSON, selection Selection) (any, error) {
	if snapshot == nil {
		return nil, ErrUnavailable
	}
	mode := strings.ToLower(strings.TrimSpace(snapshot.AuthMode))
	if mode == "" {
		if strings.TrimSpace(snapshot.OpenAIAPIKey) != "" {
			mode = "api-key"
		} else if strings.TrimSpace(snapshot.PersonalAccessToken) != "" {
			mode = "personal-access-token"
		}
	}
	if mode != "api-key" && !selection.empty() {
		return nil, ErrUnsupportedResourceSelect
	}
	credential := authToken(snapshot)
	if credential == "" {
		return nil, ErrUnsupported
	}
	switch mode {
	case "api-key":
		return platformAPIKeyAuthorization{
			Type:           "platform_api_key",
			Credential:     credential,
			OrganizationID: strings.TrimSpace(selection.OrganizationID),
			ProjectID:      strings.TrimSpace(selection.ProjectID),
		}, nil
	case "chatgpt", "chatgptauthtokens":
		return oauthActorAuthorization{Type: "oauth", Credential: credential}, nil
	case "personal-access-token":
		return personalAccessTokenAuthorization{Type: "personal_access_token", Credential: credential}, nil
	default:
		return nil, ErrUnsupported
	}
}

// RegistrationResponse is the identity the endpoint confirmed.
type RegistrationResponse struct {
	RuntimeAgentInstanceID string
	AgentHostID            string
	Actor                  ActorInfo
}

type registrationResponseWire struct {
	RuntimeAgentInstanceID string        `json:"runtime_agent_instance_id"`
	AgentHostID            string        `json:"agent_host_id"`
	Actor                  actorInfoWire `json:"actor"`
}

// RegistrationClient makes one bounded, redirect-free registration attempt.
type RegistrationClient struct {
	http HTTPDoer
	url  string
}

// NewRegistrationClient validates the endpoint and keeps credentials out of
// redirects and request logging (Rust RuntimeRegistrationClient::new).
func NewRegistrationClient(httpClient HTTPDoer, endpoint string) (*RegistrationClient, error) {
	validated, err := ValidateRegistrationURL(endpoint)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = NewRegistrationHTTPClient()
	}
	return &RegistrationClient{http: httpClient, url: validated}, nil
}

// NewRegistrationHTTPClient returns a client that never follows redirects and
// bounds each attempt to the registration timeout.
func NewRegistrationHTTPClient() *http.Client {
	return &http.Client{
		Timeout: registrationTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ValidateRegistrationURL accepts HTTPS endpoints, plus HTTP only for loopback
// registration servers, without user information, query, or fragment.
func ValidateRegistrationURL(input string) (string, error) {
	raw := strings.TrimSpace(input)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || strings.Contains(raw, "#") {
		return "", ErrInvalidEndpoint
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && !(scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return "", ErrInvalidEndpoint
	}
	return parsed.String(), nil
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" || host == "::1" || host == "[::1]" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

type registrationErrorEnvelope struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

// Register makes one bounded attempt. Callers reuse the pending registration
// when retrying (Rust RuntimeRegistrationClient::register).
func (c *RegistrationClient) Register(ctx context.Context, registration *Registration) (*RegistrationResponse, error) {
	if c == nil || c.http == nil || registration == nil {
		return nil, ErrInvalidRequest
	}
	body, err := json.Marshal(registration.request)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	if len(body) > maxRegistrationBodyBytes {
		return nil, ErrInvalidRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, ErrInvalidRequest
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", registration.initializationID)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, ErrTransport
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxRegistrationBodyBytes+1))
	if err != nil {
		return nil, ErrTransport
	}
	if len(payload) > maxRegistrationBodyBytes {
		return nil, ErrInvalidResponse
	}
	if response.StatusCode != http.StatusOK {
		code := ""
		var envelope registrationErrorEnvelope
		if json.Unmarshal(payload, &envelope) == nil {
			code = strings.TrimSpace(envelope.Error.Code)
		}
		return nil, &RegistrationError{Status: response.StatusCode, Code: code}
	}
	var wire registrationResponseWire
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, ErrInvalidResponse
	}
	instanceID := strings.TrimSpace(wire.RuntimeAgentInstanceID)
	hostID := strings.TrimSpace(wire.AgentHostID)
	if instanceID == "" || hostID == "" {
		return nil, ErrInvalidResponse
	}
	return &RegistrationResponse{
		RuntimeAgentInstanceID: instanceID,
		AgentHostID:            hostID,
		Actor:                  wire.Actor.toActorInfo(),
	}, nil
}
