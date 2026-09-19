package model

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash"
	"sort"
	"strings"

	"codex_go/auth"
)

// modelsCatalogIdentityTag versions the identity digest so that changing the
// fields below invalidates previously stored identities instead of reusing a
// catalog scoped to different inputs (Rust "models-cache-v1").
var modelsCatalogIdentityTag = []byte("models-cache-v1")

// hasStableAccountAuthMode reports whether the auth mode carries a stable owner
// identity (Rust's Chatgpt | ChatgptAuthTokens | AgentIdentity match).
func hasStableAccountAuthMode(mode string) bool {
	switch mode {
	case "chatgpt", "chatgptAuthTokens", "agent-identity":
		return true
	default:
		return false
	}
}

// ModelsCatalogIdentity returns the opaque provider/auth identity used to scope
// cached model catalogs. It mirrors Rust `models_identity::identity`
// (#43897/#43906): the digest covers provider routing (provider name, effective
// base URL, sorted query params, `requires_openai_auth`, command auth), the
// effective auth mode, and - when a stable account is identified - the account
// id, ChatGPT user id, email, plan, raw plan, and FedRAMP flag.
//
// ChatGPT access tokens are deliberately excluded so token rotation keeps the
// catalog for the same account, user, and plan; the resolved auth headers are
// folded in only when the owner is not stable (API keys, command auth) or the
// provider carries an explicit bearer credential. An empty return disables
// cached reuse, matching Rust's `identity().ok()` at the models endpoint.
//
// The digest bytes are an implementation detail: Go and Rust frame optional
// values differently, so a cache file written by one implementation is a miss
// for the other. Only the scoping rules - which inputs change the identity -
// are required to match Rust.
func ModelsCatalogIdentity(provider *ProviderInfo, authSnapshot *auth.AuthDotJSON, authHeaders *AuthHeaders, residency string) string {
	if provider == nil {
		return ""
	}
	// Rust's `provider_info.api_key()?` fails the whole identity computation, and
	// the endpoint maps that error to "no identity" (cached reuse disabled).
	apiKey, err := provider.APIKey()
	if err != nil {
		return ""
	}
	explicitBearer := apiKey != "" || strings.TrimSpace(provider.ExperimentalBearerToken) != ""

	authMode := ""
	authBackendMode := ""
	accountID, userID, email, plan, rawPlan := "", "", "", "", ""
	fedramp := false
	if authSnapshot != nil {
		authMode = authSnapshot.Mode()
		authBackendMode = authSnapshot.BackendMode()
		accountID = auth.AccountIDFromAuthForRestrictions(authSnapshot)
		userID = auth.ChatGPTUserIDFromAuth(authSnapshot)
		if account := auth.AccountFromAuth(authSnapshot); account != nil {
			plan = string(account.PlanType)
			if account.Email != nil {
				email = strings.TrimSpace(*account.Email)
			}
		}
		rawPlan = auth.ChatGPTPlanTypeRawFromAuth(authSnapshot)
		fedramp = fedrampFromMap(authSnapshot.Tokens)
	}
	stableAccount := hasStableAccountAuthMode(authMode) && accountID != "" && (userID != "" || email != "")

	// Rust resolves the routing half through `provider_info.to_api_provider`: the
	// effective backend auth mode (externally managed ChatGPT tokens normalize to
	// `chatgpt`) selects the default base URL and the provider header map.
	apiProvider, err := provider.ToAPIProvider(authBackendMode)
	if err != nil {
		return ""
	}
	// Rust enforces the managed residency requirement on the provider before
	// digesting it, so the residency header is part of the identity.
	apiProvider.ApplyManagedResidency(residency)
	if authHeaders == nil && (!stableAccount || explicitBearer) {
		resolved, err := ResolveProviderAuth(authSnapshot, *provider)
		if err != nil {
			return ""
		}
		authHeaders = &resolved
	}

	digest := sha256.New()
	identityField(digest, modelsCatalogIdentityTag)
	identityField(digest, []byte(apiProvider.Name))
	identityField(digest, []byte(apiProvider.BaseURL))

	queryPairs := make([][2]string, 0, len(apiProvider.QueryParams))
	for name, value := range apiProvider.QueryParams {
		queryPairs = append(queryPairs, [2]string{name, value})
	}
	sort.Slice(queryPairs, func(i, j int) bool {
		if queryPairs[i][0] != queryPairs[j][0] {
			return queryPairs[i][0] < queryPairs[j][0]
		}
		return queryPairs[i][1] < queryPairs[j][1]
	})
	identityCount(digest, len(queryPairs))
	for _, pair := range queryPairs {
		identityField(digest, []byte(pair[0]))
		identityField(digest, []byte(pair[1]))
	}

	identityBool(digest, provider.RequiresOpenAIAuth)
	identityBool(digest, provider.HasCommandAuth())
	identityField(digest, []byte(authMode))
	// Gateway OAuth configuration participates in the identity so a catalog
	// fetched through one gateway configuration is never reused for another
	// (Rust #46490). Providers without gateway OAuth keep their existing
	// identities.
	if provider.GatewayOAuth != nil {
		encoded, err := json.Marshal(provider.GatewayOAuth)
		if err != nil {
			return ""
		}
		identityField(digest, encoded)
	}
	if authSnapshot != nil {
		identityOptionalString(digest, accountID)
		identityOptionalString(digest, userID)
		identityOptionalString(digest, email)
		identityOptionalString(digest, plan)
		identityOptionalString(digest, rawPlan)
		identityBool(digest, fedramp)
	}

	// Non-stable owners and explicit bearer credentials are scoped by the
	// credentials actually sent, so a rotated key cannot reuse another key's
	// catalog. Provider-configured headers participate in the same map, matching
	// Rust's `provider.headers.extend(resolve_provider_auth(..))`.
	headerValues := map[string]string{}
	for name, values := range apiProvider.Headers {
		headerValues[strings.ToLower(strings.TrimSpace(name))] = strings.Join(values, ",")
	}
	if !stableAccount || explicitBearer {
		if authHeaders != nil {
			for name, values := range authHeaders.Headers {
				headerValues[strings.ToLower(strings.TrimSpace(name))] = strings.Join(values, ",")
			}
		}
	}
	headerNames := make([]string, 0, len(headerValues))
	for name := range headerValues {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	identityCount(digest, len(headerNames))
	for _, name := range headerNames {
		identityField(digest, []byte(name))
		identityField(digest, []byte(headerValues[name]))
	}

	return hex.EncodeToString(digest.Sum(nil))
}

func identityField(digest hash.Hash, value []byte) {
	var length [8]byte
	binary.LittleEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write(value)
}

func identityCount(digest hash.Hash, count int) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], uint64(count))
	_, _ = digest.Write(encoded[:])
}

func identityBool(digest hash.Hash, value bool) {
	if value {
		_, _ = digest.Write([]byte{1})
		return
	}
	_, _ = digest.Write([]byte{0})
}

func identityOptionalString(digest hash.Hash, value string) {
	if value == "" {
		identityBool(digest, false)
		return
	}
	identityBool(digest, true)
	identityField(digest, []byte(value))
}
