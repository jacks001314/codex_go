package auth

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
)

// AccountOAuthGrant is a snapshot of one persisted account grant, checked
// against both the caller's cached credential and storage. It does not
// authorize a destination; consumers must validate the recorded issuer and
// client (Rust codex-login AccountOAuthGrant, #55546).
type AccountOAuthGrant struct {
	tokens            map[string]any
	loginGeneration   uint64
	cached            func() *AuthDotJSON
	loginGenerationFn func() uint64
	codexHome         string
	storeOptions      *StoreOptions
}

// AccountOAuthGrantLease keeps ordinary refresh from rotating the account token
// during an identity-provider exchange. Login and logout remain available and
// invalidate the grant independently of this lease.
type AccountOAuthGrantLease struct {
	grant *AccountOAuthGrant
	gate  *accountRefreshGate
}

// AccountOAuthGrantLeaseOptions supplies the caller's cached credential view.
type AccountOAuthGrantLeaseOptions struct {
	CodexHome    string
	StoreOptions *StoreOptions
	// Cached returns the caller's current credential snapshot.
	Cached func() *AuthDotJSON
	// LoginGeneration returns the caller's login revision. A grant is invalid
	// as soon as this revision advances.
	LoginGeneration func() uint64
}

// Tokens returns a copy of the grant's token data.
func (l *AccountOAuthGrantLease) Tokens() map[string]any {
	if l == nil || l.grant == nil {
		return nil
	}
	return l.grant.Tokens()
}

// Release drops refresh exclusion before contacting a resource authorization
// server.
func (l *AccountOAuthGrantLease) Release() *AccountOAuthGrant {
	if l == nil {
		return nil
	}
	grant := l.grant
	l.grant = nil
	if l.gate != nil {
		l.gate.release()
		l.gate = nil
	}
	return grant
}

// Tokens returns a copy of the granted token data.
func (g *AccountOAuthGrant) Tokens() map[string]any {
	if g == nil {
		return nil
	}
	return cloneAnyMap(g.tokens)
}

// LoginGeneration returns the login revision captured with the grant.
func (g *AccountOAuthGrant) LoginGeneration() uint64 {
	if g == nil {
		return 0
	}
	return g.loginGeneration
}

// SameCredential reports whether two grants carry the same credential
// revision without exposing the refresh token to state holders (Rust #55680).
func (g *AccountOAuthGrant) SameCredential(other *AccountOAuthGrant) bool {
	if g == nil || other == nil {
		return g == other
	}
	return reflect.DeepEqual(g.tokens, other.tokens)
}

// CheckCurrent rechecks the caller's cached credential and persisted storage
// after asynchronous work, before accepting a token or publishing a
// connection. A storage failure is an error, never proof that the saved grant
// is still valid. The complete token data must still match, so an access-token
// rotation also invalidates the grant (Rust #55680).
func (g *AccountOAuthGrant) CheckCurrent() (bool, error) {
	if !g.ownerIsCurrent() {
		return false, nil
	}
	stored, err := readAccountGrantTokens(g.codexHome, g.storeOptions, g.cached())
	if err != nil {
		return false, err
	}
	if !g.ownerIsCurrent() || stored == nil {
		return false, nil
	}
	return reflect.DeepEqual(g.tokens, stored), nil
}

// AccountLoginIsCurrent reports whether the account login captured for an
// enterprise authorization is still the cached and persisted login. A
// persisted replacement can leave the auth cache holding the previous login
// even for the same account, so both views are checked (Rust #55680).
func AccountLoginIsCurrent(codexHome string, storeOptions *StoreOptions, expected *AuthDotJSON, cached func() *AuthDotJSON) (bool, error) {
	if expected == nil {
		return false, nil
	}
	cachedSnapshot := func() *AuthDotJSON {
		if cached == nil {
			return nil
		}
		return cached()
	}
	if expected.Mode() != "chatgpt" {
		current := cachedSnapshot()
		return current != nil && current.Mode() != "chatgpt" &&
			AccountIDFromAuthForRestrictions(current) == AccountIDFromAuthForRestrictions(expected) &&
			ChatGPTUserIDFromAuth(current) == ChatGPTUserIDFromAuth(expected), nil
	}
	if tokenLoginOwnerID(expected.Tokens) == "" {
		return false, nil
	}
	matchesOwner := func(snapshot *AuthDotJSON) bool {
		return snapshot != nil && snapshot.Mode() == "chatgpt" &&
			sameAccountLogin(expected.Tokens, snapshot.Tokens)
	}
	if !matchesOwner(cachedSnapshot()) {
		return false, nil
	}
	stored, err := readPersistedAccountTokens(codexHome, storeOptions)
	if err != nil {
		return false, err
	}
	return matchesOwner(cachedSnapshot()) && matchesOwner(stored), nil
}

// readPersistedAccountTokens loads the persisted ChatGPT login without
// requiring an EMA-eligible grant: dedicated enterprise credentials still
// belong to this account login (Rust #55680).
func readPersistedAccountTokens(codexHome string, storeOptions *StoreOptions) (*AuthDotJSON, error) {
	if IsWorkloadIdentitySelected() || (storeOptions != nil && storeOptions.Mode == AuthCredentialsStoreEphemeral) {
		return nil, nil
	}
	stored, err := NewStoreWithOptions(codexHome, storeOptions).LoadAccountGrant()
	if err != nil {
		return nil, errors.New("Account credential storage is unavailable.")
	}
	if stored == nil || stored.Mode() != "chatgpt" {
		return nil, nil
	}
	return stored, nil
}

func (g *AccountOAuthGrant) ownerIsCurrent() bool {
	if g == nil || g.cached == nil {
		return false
	}
	if g.loginGenerationFn != nil && g.loginGenerationFn() != g.loginGeneration {
		return false
	}
	cached := g.cached()
	if cached == nil || cached.Mode() != "chatgpt" {
		return false
	}
	return sameAccountGrant(g.tokens, cached.Tokens)
}

// LeaseAccountOAuthGrant borrows a persisted account grant while excluding this
// process's token refreshes for the same Codex home. Release the lease after the
// identity-provider exchange, then check the grant before using its result
// (Rust #55546 lease_account_oauth_grant).
func LeaseAccountOAuthGrant(ctx context.Context, opts *AccountOAuthGrantLeaseOptions) (*AccountOAuthGrantLease, error) {
	if opts == nil || opts.Cached == nil {
		return nil, nil
	}
	codexHome := strings.TrimSpace(opts.CodexHome)
	if codexHome == "" {
		codexHome = DefaultCodexHome()
	}
	gate := accountRefreshGateFor(codexHome)
	if err := gate.acquire(ctx); err != nil {
		return nil, errors.New("Account refresh coordination is unavailable.")
	}
	var loginGeneration uint64
	if opts.LoginGeneration != nil {
		loginGeneration = opts.LoginGeneration()
	}
	tokens, err := readAccountGrantTokens(codexHome, opts.StoreOptions, opts.Cached())
	if err != nil {
		gate.release()
		return nil, err
	}
	if tokens == nil {
		gate.release()
		return nil, nil
	}
	grant := &AccountOAuthGrant{
		tokens:            tokens,
		loginGeneration:   loginGeneration,
		cached:            opts.Cached,
		loginGenerationFn: opts.LoginGeneration,
		codexHome:         codexHome,
		storeOptions:      opts.StoreOptions,
	}
	if !grant.ownerIsCurrent() {
		gate.release()
		return nil, nil
	}
	return &AccountOAuthGrantLease{grant: grant, gate: gate}, nil
}

// readAccountGrantTokens loads the stored grant and requires OAuth provenance,
// a complete account identity and a matching cached credential. An unavailable
// keyring is an error in Auto mode instead of falling back to an older file.
func readAccountGrantTokens(codexHome string, storeOptions *StoreOptions, cached *AuthDotJSON) (map[string]any, error) {
	// External authentication owns its credentials, and an ephemeral store has
	// no persisted grant to borrow (Rust has_external_auth / Ephemeral).
	if IsWorkloadIdentitySelected() || (storeOptions != nil && storeOptions.Mode == AuthCredentialsStoreEphemeral) {
		return nil, nil
	}
	if cached == nil || cached.Mode() != "chatgpt" {
		return nil, nil
	}
	stored, err := NewStoreWithOptions(codexHome, storeOptions).LoadAccountGrant()
	if err != nil {
		return nil, errors.New("Account credential storage is unavailable.")
	}
	if stored == nil || stored.Mode() != "chatgpt" || !completeAccountGrant(stored.Tokens) {
		return nil, nil
	}
	if !sameAccountGrant(stored.Tokens, cached.Tokens) {
		return nil, nil
	}
	return cloneAnyMap(stored.Tokens), nil
}

func completeAccountGrant(tokens map[string]any) bool {
	provenance := oauthProvenanceFromTokens(tokens)
	return provenance.Issuer != "" && provenance.ClientID != "" &&
		tokenLoginOwnerID(tokens) != "" &&
		tokenRefreshToken(tokens) != "" &&
		tokenChatGPTUserID(tokens) != "" &&
		tokenAccountID(tokens) != ""
}

func sameAccountGrant(a map[string]any, b map[string]any) bool {
	return oauthProvenanceFromTokens(a) == oauthProvenanceFromTokens(b) && sameAccountLogin(a, b)
}

func sameAccountLogin(a map[string]any, b map[string]any) bool {
	return tokenLoginOwnerID(a) == tokenLoginOwnerID(b) &&
		tokenAccountID(a) == tokenAccountID(b) &&
		tokenChatGPTUserID(a) == tokenChatGPTUserID(b)
}

// sameLoginForPersistence accepts the same login, or the loss of login metadata
// by an older client while the refresh token still matches (Rust #55646).
func sameLoginForPersistence(expected map[string]any, stored map[string]any) bool {
	if sameAccountLogin(expected, stored) {
		return true
	}
	storedRefresh := tokenRefreshToken(stored)
	return tokenLoginOwnerID(expected) != "" &&
		tokenLoginOwnerID(stored) == "" &&
		tokenAccountID(stored) == tokenAccountID(expected) &&
		tokenChatGPTUserID(stored) == tokenChatGPTUserID(expected) &&
		storedRefresh != "" &&
		storedRefresh == tokenRefreshToken(expected)
}

func tokenLoginOwnerID(tokens map[string]any) string {
	return strings.TrimSpace(stringFromAny(tokens, "login_owner_id"))
}

func tokenRefreshToken(tokens map[string]any) string {
	return strings.TrimSpace(stringFromAny(tokens, "refresh_token"))
}

func tokenAccountID(tokens map[string]any) string {
	if accountID := strings.TrimSpace(stringFromAny(tokens, "account_id")); accountID != "" {
		return accountID
	}
	for _, key := range []string{"id_token", "access_token"} {
		if claims := ChatGPTClaimsFromJWT(stringFromAny(tokens, key)); strings.TrimSpace(claims.AccountID) != "" {
			return strings.TrimSpace(claims.AccountID)
		}
	}
	return ""
}

func tokenChatGPTUserID(tokens map[string]any) string {
	if userID := strings.TrimSpace(stringFromAny(tokens, "chatgpt_user_id")); userID != "" {
		return userID
	}
	for _, key := range []string{"access_token", "id_token"} {
		if claims := ChatGPTClaimsFromJWT(stringFromAny(tokens, key)); strings.TrimSpace(claims.UserID) != "" {
			return strings.TrimSpace(claims.UserID)
		}
	}
	return ""
}

// AccountTokenFacts is the persisted account credential subset that enterprise
// MCP credential selection inspects. It mirrors the Rust TokenData fields that
// account_grant_is_usable and account_configuration_issue read, without
// exposing the raw record or its tokens.
type AccountTokenFacts struct {
	// Issuer and ClientID are the trusted OAuth provenance recorded with the
	// login; token claims never infer them.
	Issuer        string
	ClientID      string
	LoginOwnerID  string
	RefreshToken  string
	ChatGPTUserID string
	// AccountID is the record's own account_id. It deliberately does not fall
	// back to an ID-token claim: the usability check compares the record and
	// the claim, so a fallback would make that comparison vacuous.
	AccountID string
	IDToken   string
}

// AccountTokenFactsFromTokens extracts the facts enterprise MCP credential
// selection needs from one admitted account record.
func AccountTokenFactsFromTokens(tokens map[string]any) AccountTokenFacts {
	provenance := oauthProvenanceFromTokens(tokens)
	return AccountTokenFacts{
		Issuer:        provenance.Issuer,
		ClientID:      provenance.ClientID,
		LoginOwnerID:  tokenLoginOwnerID(tokens),
		RefreshToken:  tokenRefreshToken(tokens),
		ChatGPTUserID: tokenChatGPTUserID(tokens),
		AccountID:     strings.TrimSpace(stringFromAny(tokens, "account_id")),
		IDToken:       strings.TrimSpace(stringFromAny(tokens, "id_token")),
	}
}

// SameAccountGrantCredential reports whether two token sets carry the same
// account credential revision (Rust AccountOAuthGrant::same_credential). An
// enterprise exchange compares the grant it started with against the record
// still stored, so an access-token rotation retires the grant.
func SameAccountGrantCredential(left map[string]any, right map[string]any) bool {
	return reflect.DeepEqual(left, right)
}

// accountRefreshGate allows one account refresh at a time and is also held by
// AccountOAuthGrantLease, so an identity-provider exchange and an ordinary
// refresh cannot rotate the same grant concurrently.
type accountRefreshGate struct {
	token chan struct{}
}

var accountRefreshGates = struct {
	sync.Mutex
	byHome map[string]*accountRefreshGate
}{byHome: map[string]*accountRefreshGate{}}

func accountRefreshGateFor(codexHome string) *accountRefreshGate {
	key := authStoreKey(codexHome)
	accountRefreshGates.Lock()
	defer accountRefreshGates.Unlock()
	gate := accountRefreshGates.byHome[key]
	if gate == nil {
		gate = &accountRefreshGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		accountRefreshGates.byHome[key] = gate
	}
	return gate
}

func (g *accountRefreshGate) acquire(ctx context.Context) error {
	if g == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-g.token:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *accountRefreshGate) release() {
	if g == nil {
		return
	}
	select {
	case g.token <- struct{}{}:
	default:
	}
}
