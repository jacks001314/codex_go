package runtimeauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"codex_go/auth"
)

// Mode selects how a request presents runtime credentials (Rust
// features::RuntimeAuthMode).
type Mode string

const (
	// ModeDualHeader sends the actor credential together with a runtime
	// assertion while hosts migrate to assertion-only requests.
	ModeDualHeader Mode = "dual_header"
	// ModeAssertionOnly sends only the runtime assertion as the request
	// credential.
	ModeAssertionOnly Mode = "assertion_only"
)

// ParseMode maps a configured value to a mode, defaulting to dual headers
// exactly like the Rust `#[serde(default)]` deserializer.
func ParseMode(input string) Mode {
	if strings.EqualFold(strings.TrimSpace(input), string(ModeAssertionOnly)) {
		return ModeAssertionOnly
	}
	return ModeDualHeader
}

// Selection carries API resource selectors that authenticate an API-key actor
// (Rust PlatformResourceSelection). They are selectors, not identity claims.
type Selection struct {
	OrganizationID string
	ProjectID      string
}

func (s Selection) empty() bool {
	return strings.TrimSpace(s.OrganizationID) == "" && strings.TrimSpace(s.ProjectID) == ""
}

// Role is the registered runtime's execution role.
type Role string

const (
	RoleOrchestrator            Role = "orchestrator"
	RoleExecutor                Role = "executor"
	RoleOrchestratorAndExecutor Role = "orchestrator_and_executor"
)

// Metadata is the non-credential registration metadata.
type Metadata struct {
	Harness        string
	HarnessVersion string
	Role           Role
}

type registrationMetadata struct {
	Harness        string `json:"harness,omitempty"`
	HarnessVersion string `json:"harness_version,omitempty"`
	Role           string `json:"role"`
}

func (m Metadata) wire() registrationMetadata {
	role := m.Role
	if role == "" {
		role = RoleOrchestratorAndExecutor
	}
	return registrationMetadata{
		Harness:        strings.TrimSpace(m.Harness),
		HarnessVersion: strings.TrimSpace(m.HarnessVersion),
		Role:           string(role),
	}
}

// ActorKind distinguishes the two actor shapes a registration can return.
type ActorKind string

const (
	ActorChatGPT  ActorKind = "chatgpt"
	ActorPlatform ActorKind = "platform"
)

// ActorInfo is the identity the registration endpoint confirmed.
type ActorInfo struct {
	Kind           ActorKind
	UserID         string
	AccountID      string
	OrganizationID string
	ProjectID      string
}

type actorInfoWire struct {
	Kind           string `json:"kind"`
	UserID         string `json:"user_id"`
	AccountID      string `json:"account_id"`
	OrganizationID string `json:"organization_id"`
	ProjectID      string `json:"project_id"`
}

func (w *actorInfoWire) toActorInfo() ActorInfo {
	kind := ActorPlatform
	if strings.EqualFold(strings.TrimSpace(w.Kind), string(ActorChatGPT)) {
		kind = ActorChatGPT
	}
	return ActorInfo{
		Kind:           kind,
		UserID:         strings.TrimSpace(w.UserID),
		AccountID:      strings.TrimSpace(w.AccountID),
		OrganizationID: strings.TrimSpace(w.OrganizationID),
		ProjectID:      strings.TrimSpace(w.ProjectID),
	}
}

// Errors mirror Rust RuntimeAuthError. They intentionally omit transport detail
// and server messages, which can contain secrets.
var (
	ErrUnavailable               = errors.New("runtime registration requires an actor credential")
	ErrUnsupported               = errors.New("this credential cannot register a runtime; use a supported OpenAI sign-in")
	ErrUnsupportedResourceSelect = errors.New("organization and project selection requires an OpenAI API key")
	ErrChanged                   = errors.New("credentials changed during runtime registration; try again")
	ErrRefreshFailed             = errors.New("runtime registration could not refresh actor credentials")
	ErrKeyGeneration             = errors.New("failed to generate runtime signing key")
	ErrInvalidEndpoint           = errors.New("runtime registration requires a secure endpoint without credentials or query parameters")
	ErrInvalidRequest            = errors.New("invalid runtime registration request")
	ErrTransport                 = errors.New("runtime registration transport failed")
	ErrInvalidResponse           = errors.New("invalid runtime registration response")
)

// RegistrationError is a rejected registration, carrying only the status and
// the server's stable error code.
type RegistrationError struct {
	Status int
	Code   string
}

func (e *RegistrationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return fmt.Sprintf("runtime registration rejected (%d)", e.Status)
	}
	return fmt.Sprintf("runtime registration rejected (%d, %s)", e.Status, e.Code)
}

// Unauthorized reports whether the rejection was an expired or invalid actor
// credential, which the caller may recover by refreshing and retrying.
func (e *RegistrationError) Unauthorized() bool {
	return e != nil && e.Status == http.StatusUnauthorized
}

// Forbidden reports a durable denial for this runtime identity; the manager
// caches it instead of retrying.
func (e *RegistrationError) Forbidden() bool { return e != nil && e.Status == http.StatusForbidden }

// HTTPDoer performs one request; it matches *http.Client.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

const (
	// DefaultRegistrationURL registers production ChatGPT runtimes.
	DefaultRegistrationURL = "https://auth.openai.com/agent/register"
	// StagingRegistrationURL registers ChatGPT staging runtimes.
	StagingRegistrationURL = "https://auth.api.openai.org/agent/register"
)

// AssertionHeader carries the signed runtime assertion alongside the legacy
// actor credential.
const AssertionHeader = "x-openai-agent-assertion"

func newSigningKey() (ed25519.PrivateKey, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, ErrKeyGeneration
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// encodeSSHEd25519PublicKey mirrors codex_agent_identity's wire encoding.
func encodeSSHEd25519PublicKey(publicKey ed25519.PublicKey) string {
	var blob []byte
	blob = appendSSHString(blob, []byte("ssh-ed25519"))
	blob = appendSSHString(blob, publicKey)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob)
}

func appendSSHString(buffer []byte, value []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	buffer = append(buffer, length[:]...)
	return append(buffer, value...)
}

// Instance is a server-registered identity plus its in-memory signer.
type Instance struct {
	id         string
	actor      ActorInfo
	hostID     string
	signingKey ed25519.PrivateKey
}

// ID returns the registered runtime agent instance id.
func (i *Instance) ID() string {
	if i == nil {
		return ""
	}
	return i.id
}

// Actor returns the confirmed actor identity.
func (i *Instance) Actor() ActorInfo {
	if i == nil {
		return ActorInfo{}
	}
	return i.actor
}

// HostID returns the registered agent host id.
func (i *Instance) HostID() string {
	if i == nil {
		return ""
	}
	return i.hostID
}

// Assertion signs a fresh v1 assertion. Every request attempt and connection
// handshake gets its own assertion, matching Rust
// RuntimeAgentInstance::assertion.
func (i *Instance) Assertion(now time.Time) (string, error) {
	if i == nil || len(i.signingKey) != ed25519.PrivateKeySize || strings.TrimSpace(i.id) == "" {
		return "", errors.New("runtime agent instance is not registered")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	protected := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","typ":"openai-agent-assertion+jws"}`))
	payloadJSON, err := json.Marshal(map[string]any{
		"version":                1,
		"runtime_agent_instance": i.id,
		"iat":                    now.UTC().Unix(),
		"id":                     newUUIDv4(),
	})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := protected + "." + payload
	signature := base64.RawURLEncoding.EncodeToString(ed25519.Sign(i.signingKey, []byte(signingInput)))
	assertion := "1." + signingInput + "." + signature
	if len(assertion) > 8192 {
		return "", errors.New("runtime assertion exceeds the size limit")
	}
	return assertion, nil
}

// Auth is request-scoped credentials plus the runtime selected with them.
type Auth struct {
	instance *Instance
	snapshot *auth.AuthDotJSON
}

// Instance returns the runtime that signs this request.
func (a *Auth) Instance() *Instance {
	if a == nil {
		return nil
	}
	return a.instance
}

// InstanceID returns the runtime agent instance id.
func (a *Auth) InstanceID() string {
	if a == nil {
		return ""
	}
	return a.instance.ID()
}

// Snapshot returns the credential snapshot the runtime was selected with.
func (a *Auth) Snapshot() *auth.AuthDotJSON {
	if a == nil {
		return nil
	}
	return a.snapshot
}

// Headers returns the request headers for the selected mode.
func (a *Auth) Headers(mode Mode) (http.Header, error) {
	return a.HeadersAt(mode, time.Now().UTC())
}

// HeadersAt is Headers with an explicit assertion timestamp for tests.
func (a *Auth) HeadersAt(mode Mode, now time.Time) (http.Header, error) {
	if a == nil || a.instance == nil || a.snapshot == nil {
		return nil, ErrUnavailable
	}
	assertion, err := a.instance.Assertion(now)
	if err != nil {
		return nil, errors.New("failed to sign runtime assertion")
	}
	headers := http.Header{}
	if ParseMode(string(mode)) == ModeAssertionOnly {
		headers.Set("Authorization", "AgentAssertion "+assertion)
	} else {
		token := authToken(a.snapshot)
		if token == "" {
			return nil, errors.New("runtime actor credential is unavailable")
		}
		headers.Set("Authorization", "Bearer "+token)
		headers.Set(AssertionHeader, assertion)
	}
	if actor := a.instance.Actor(); actor.Kind == ActorChatGPT && strings.TrimSpace(actor.AccountID) != "" {
		headers.Set("chatgpt-account-id", actor.AccountID)
	}
	if auth.IsFedrampAccount(a.snapshot) {
		headers.Set("x-openai-fedramp", "true")
	}
	return headers, nil
}

// Apply writes the runtime request headers onto an outgoing request.
func (a *Auth) Apply(request *http.Request, mode Mode) error {
	if request == nil {
		return errors.New("request is nil")
	}
	headers, err := a.Headers(mode)
	if err != nil {
		return err
	}
	for name, values := range headers {
		request.Header.Del(name)
		for _, value := range values {
			if strings.EqualFold(name, "Authorization") {
				request.Header.Set(name, value)
				continue
			}
			request.Header.Add(name, value)
		}
	}
	return nil
}

func authToken(snapshot *auth.AuthDotJSON) string {
	if snapshot == nil {
		return ""
	}
	if mode := strings.ToLower(strings.TrimSpace(snapshot.AuthMode)); mode == "api-key" {
		return strings.TrimSpace(snapshot.OpenAIAPIKey)
	}
	if strings.TrimSpace(snapshot.PersonalAccessToken) != "" {
		return strings.TrimSpace(snapshot.PersonalAccessToken)
	}
	for _, key := range []string{"access_token", "id_token", "token"} {
		if value, ok := snapshot.Tokens[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(snapshot.OpenAIAPIKey)
}
