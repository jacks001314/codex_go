package runtimeauth

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"net/http"
	"strings"
	"sync"
	"time"

	"codex_go/auth"
)

// ManagerOptions configures a runtime auth manager owned alongside the
// credential store, not per thread or request (Rust RuntimeAuthManager).
type ManagerOptions struct {
	// Credentials returns the currently selected credential snapshot.
	Credentials func(ctx context.Context) (*auth.AuthDotJSON, error)
	// SourceGeneration reports the login revision of the credential source. A
	// new login or logout increments it and invalidates the cached runtime.
	SourceGeneration func() uint64
	// RefreshOnUnauthorized refreshes the actor credential after a rejected
	// registration. Nil leaves the rejection to the caller.
	RefreshOnUnauthorized func(ctx context.Context) error
	// InstallationID identifies this host. It is tracking metadata, not proof
	// of device ownership.
	InstallationID string
	// RegistrationURL overrides the default registration endpoint.
	RegistrationURL string
	// Metadata is the harness identity registered with the runtime.
	Metadata Metadata
	// HTTPClient performs the registration request.
	HTTPClient HTTPDoer
	// Now overrides the clock in tests.
	Now func() time.Time
}

type cachedRuntime struct {
	sourceGeneration uint64
	selection        Selection
	state            runtimeState
}

type runtimeState interface{ isRuntimeState() }

type pendingRuntime struct{ registration *Registration }

func (pendingRuntime) isRuntimeState() {}

type readyRuntime struct{ instance *Instance }

func (readyRuntime) isRuntimeState() {}

type deniedRuntime struct{ err error }

func (deniedRuntime) isRuntimeState() {}

// Manager reuses one active runtime per credential source and organization
// (Rust RuntimeAuthManager).
type Manager struct {
	credentials           func(ctx context.Context) (*auth.AuthDotJSON, error)
	sourceGeneration      func() uint64
	refreshOnUnauthorized func(ctx context.Context) error
	installationID        string
	registrationURL       string
	metadata              Metadata
	httpClient            HTTPDoer
	now                   func() time.Time

	mu     sync.Mutex
	cached *cachedRuntime
}

// NewManager validates the registration endpoint and returns a manager.
func NewManager(options ManagerOptions) (*Manager, error) {
	if options.Credentials == nil {
		return nil, ErrUnavailable
	}
	endpoint := strings.TrimSpace(options.RegistrationURL)
	if endpoint == "" {
		endpoint = DefaultRegistrationURL
	}
	validated, err := ValidateRegistrationURL(endpoint)
	if err != nil {
		return nil, err
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = NewRegistrationHTTPClient()
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if _, err := agentHostAuthorization(options.InstallationID); err != nil {
		return nil, err
	}
	return &Manager{
		credentials:           options.Credentials,
		sourceGeneration:      options.SourceGeneration,
		refreshOnUnauthorized: options.RefreshOnUnauthorized,
		installationID:        strings.TrimSpace(options.InstallationID),
		registrationURL:       validated,
		metadata:              options.Metadata,
		httpClient:            httpClient,
		now:                   now,
	}, nil
}

// SourceGeneration reports the manager's current credential source revision.
func (m *Manager) SourceGeneration() uint64 {
	if m == nil || m.sourceGeneration == nil {
		return 0
	}
	return m.sourceGeneration()
}

// RegistrationURLForBaseURL selects the ChatGPT registration origin for a
// ChatGPT base URL, resolving it only when a first-party request needs a
// runtime (Rust RegistrationTarget::url).
func RegistrationURLForBaseURL(baseURL string) (string, error) {
	lowered := strings.ToLower(strings.TrimSpace(baseURL))
	switch {
	case lowered == "":
		return DefaultRegistrationURL, nil
	case strings.Contains(lowered, "staging"):
		return StagingRegistrationURL, nil
	case strings.Contains(lowered, "chatgpt.com"), strings.Contains(lowered, "chat.openai.com"), strings.Contains(lowered, "api.openai.com"):
		return DefaultRegistrationURL, nil
	default:
		return "", ErrInvalidEndpoint
	}
}

// Auth selects the runtime for the default resource selection.
func (m *Manager) Auth(ctx context.Context) (*Auth, error) {
	return m.AuthWithSelection(ctx, Selection{})
}

// AuthWithSelection selects a runtime for the requested organization and
// project.
func (m *Manager) AuthWithSelection(ctx context.Context, selection Selection) (*Auth, error) {
	return m.authWithSource(ctx, nil, selection)
}

// AuthForSource selects another request within a client's original credential
// source. A different source must not take over that work (Rust
// RuntimeAuthManager::auth_for_source).
func (m *Manager) AuthForSource(ctx context.Context, sourceGeneration uint64, selection Selection) (*Auth, error) {
	expected := sourceGeneration
	return m.authWithSource(ctx, &expected, selection)
}

// Invalidate drops the cached runtime so the next request registers again.
func (m *Manager) Invalidate() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cached = nil
}

func (m *Manager) checkSource(expected *uint64) error {
	if expected != nil && m.SourceGeneration() != *expected {
		return ErrChanged
	}
	return nil
}

// authWithSource serializes registration and refresh for one credential
// source. The lock intentionally spans the registration request.
func (m *Manager) authWithSource(ctx context.Context, expectedSource *uint64, selection Selection) (*Auth, error) {
	if m == nil {
		return nil, ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	selection = Selection{
		OrganizationID: strings.TrimSpace(selection.OrganizationID),
		ProjectID:      strings.TrimSpace(selection.ProjectID),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkSource(expectedSource); err != nil {
		return nil, err
	}
	snapshot, err := m.credentials(ctx)
	if err != nil || snapshot == nil {
		return nil, ErrUnavailable
	}
	generation := m.generationFor(snapshot)
	if expectedSource != nil && generation != *expectedSource {
		return nil, ErrChanged
	}
	result, err := m.authOnce(ctx, snapshot, selection, expectedSource, generation)
	for attempts := 0; err != nil && m.refreshOnUnauthorized != nil && attempts < 1; attempts++ {
		var rejected *RegistrationError
		if !errors.As(err, &rejected) || !rejected.Unauthorized() {
			break
		}
		if checkErr := m.checkSource(expectedSource); checkErr != nil {
			return nil, checkErr
		}
		if refreshErr := m.refreshOnUnauthorized(ctx); refreshErr != nil {
			return nil, ErrRefreshFailed
		}
		snapshot, err = m.credentials(ctx)
		if err != nil || snapshot == nil {
			return nil, ErrUnavailable
		}
		result, err = m.authOnce(ctx, snapshot, selection, expectedSource, m.generationFor(snapshot))
	}
	return result, err
}

func (m *Manager) authOnce(ctx context.Context, snapshot *auth.AuthDotJSON, selection Selection, expectedSource *uint64, generation uint64) (*Auth, error) {
	if expectedSource != nil && m.SourceGeneration() != *expectedSource {
		return nil, ErrChanged
	}
	if m.cached != nil {
		replace := m.cached.sourceGeneration != generation
		if !replace && m.cached.selection.OrganizationID != selection.OrganizationID {
			replace = true
		}
		if !replace {
			if _, ready := m.cached.state.(readyRuntime); !ready && m.cached.selection != selection {
				replace = true
			}
		}
		if replace {
			m.cached = nil
		}
	}
	if m.cached != nil {
		switch state := m.cached.state.(type) {
		case readyRuntime:
			return &Auth{instance: state.instance, snapshot: snapshot}, nil
		case deniedRuntime:
			return nil, state.err
		}
	}
	if m.cached == nil {
		registration, err := NewRegistration(snapshot, selection, m.installationID, m.metadata)
		if err != nil {
			return nil, err
		}
		m.cached = &cachedRuntime{
			sourceGeneration: generation,
			selection:        selection,
			state:            pendingRuntime{registration: registration},
		}
	}
	pending, ok := m.cached.state.(pendingRuntime)
	if !ok {
		return nil, ErrUnavailable
	}
	registration := pending.registration
	if err := registration.refreshActor(snapshot, selection); err != nil {
		return nil, err
	}
	client, err := NewRegistrationClient(m.httpClient, m.registrationURL)
	if err != nil {
		return nil, err
	}
	if err := m.checkSource(expectedSource); err != nil {
		return nil, err
	}
	info, err := client.Register(ctx, registration)
	if m.sourceChanged(ctx, generation) {
		// A late response must not publish an old identity after a credential
		// switch.
		m.cached = nil
		return nil, ErrChanged
	}
	if err != nil {
		var rejected *RegistrationError
		if errors.As(err, &rejected) && rejected.Forbidden() {
			m.cached.state = deniedRuntime{err: err}
		}
		return nil, err
	}
	instance := registration.finish(info)
	m.cached.state = readyRuntime{instance: instance}
	return &Auth{instance: instance, snapshot: snapshot}, nil
}

type authContextKey struct{}

// WithAuth attaches one request-scoped runtime selection to a context so
// transports can apply its headers without threading the value through every
// helper signature.
func WithAuth(ctx context.Context, value *Auth) context.Context {
	if ctx == nil || value == nil {
		return ctx
	}
	return context.WithValue(ctx, authContextKey{}, value)
}

// FromContext returns the runtime selection attached by WithAuth.
func FromContext(ctx context.Context) (*Auth, bool) {
	if ctx == nil {
		return nil, false
	}
	value, ok := ctx.Value(authContextKey{}).(*Auth)
	if !ok || value == nil {
		return nil, false
	}
	return value, true
}

// ApplyContextHeaders writes the runtime request headers of the selection
// attached to the request context. Requests without a selection are left
// untouched, so custom backends keep their own authentication.
func ApplyContextHeaders(request *http.Request, mode Mode) error {
	if request == nil {
		return errors.New("request is nil")
	}
	value, ok := FromContext(request.Context())
	if !ok {
		return nil
	}
	return value.Apply(request, mode)
}

// generationFor reports the credential source revision. Callers that track
// login revisions may supply a callback; otherwise the snapshot's source
// identity is digested, so token refreshes keep the cached runtime.
func (m *Manager) generationFor(snapshot *auth.AuthDotJSON) uint64 {
	if m.sourceGeneration != nil {
		return m.sourceGeneration()
	}
	return SourceGeneration(snapshot)
}

// sourceChanged re-reads the credential source after registration so a late
// response cannot publish an old identity after a credential switch.
func (m *Manager) sourceChanged(ctx context.Context, generation uint64) bool {
	if m.sourceGeneration != nil {
		return m.SourceGeneration() != generation
	}
	snapshot, err := m.credentials(ctx)
	if err != nil || snapshot == nil {
		return true
	}
	return SourceGeneration(snapshot) != generation
}

// SourceGeneration derives a stable identifier for one credential source. A
// token refresh keeps it; a new login or rotated API key changes it.
func SourceGeneration(snapshot *auth.AuthDotJSON) uint64 {
	if snapshot == nil {
		return 0
	}
	mode := strings.ToLower(strings.TrimSpace(snapshot.AuthMode))
	if mode == "api-key" || (mode == "" && strings.TrimSpace(snapshot.OpenAIAPIKey) != "") {
		digest := sha256.Sum256([]byte(strings.TrimSpace(snapshot.OpenAIAPIKey)))
		return binary.BigEndian.Uint64(digest[:8])
	}
	if mode == "personal-access-token" {
		digest := sha256.Sum256([]byte(strings.TrimSpace(snapshot.PersonalAccessToken)))
		return binary.BigEndian.Uint64(digest[:8])
	}
	hasher := fnv.New64a()
	for _, part := range []string{
		mode,
		auth.LoginOwnerIDFromAuth(snapshot),
		auth.ChatGPTUserIDFromAuth(snapshot),
		auth.AccountIDFromAuthForRestrictions(snapshot),
	} {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{0})
	}
	return hasher.Sum64()
}

// ModeFromConfigValues reads `runtime_agent_instances.mode`, defaulting to
// dual headers exactly like the Rust config struct.
func ModeFromConfigValues(values map[string]any) Mode {
	if values == nil {
		return ModeDualHeader
	}
	section, ok := values["runtime_agent_instances"].(map[string]any)
	if !ok {
		return ModeDualHeader
	}
	mode, _ := section["mode"].(string)
	return ParseMode(mode)
}

// NewStoreManager builds a manager over the Codex auth store for one home
// directory. The installer's installation id identifies this host for
// registration bookkeeping.
func NewStoreManager(codexHome string, installationID string, metadata Metadata) (*Manager, error) {
	store := auth.NewStore(codexHome)
	return NewManager(ManagerOptions{
		Credentials: func(context.Context) (*auth.AuthDotJSON, error) {
			resolved, err := store.Resolve()
			if err != nil {
				return nil, err
			}
			if resolved == nil {
				return nil, ErrUnavailable
			}
			snapshot := resolved.Auth
			return &snapshot, nil
		},
		InstallationID: installationID,
		Metadata:       metadata,
	})
}
