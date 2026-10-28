package model

// Prompt cache-key policy for Responses requests (Rust #55587).

// PromptCacheKeyMode selects how a thread derives the Responses
// prompt_cache_key request-body field (Rust codex_core::PromptCacheKey).
type PromptCacheKeyMode uint8

const (
	// PromptCacheKeySessionDerived keeps the default session-derived key,
	// including internal-session and fork routing.
	PromptCacheKeySessionDerived PromptCacheKeyMode = iota
	// PromptCacheKeyCustom sends the caller's key on every model request.
	PromptCacheKeyCustom
	// PromptCacheKeyOmit leaves prompt_cache_key out of model requests.
	PromptCacheKeyOmit
)

// PromptCacheKey is the prompt-cache-key policy for a thread and its child
// agents. The zero value keeps the session-derived key. The policy only
// controls the request body: session and thread identity headers are always
// derived from the session identity (Rust #55587).
type PromptCacheKey struct {
	mode PromptCacheKeyMode
	key  string
}

// SessionDerivedPromptCacheKey returns the default policy.
func SessionDerivedPromptCacheKey() PromptCacheKey {
	return PromptCacheKey{}
}

// CustomPromptCacheKey returns a policy that sends key on every request.
func CustomPromptCacheKey(key string) PromptCacheKey {
	return PromptCacheKey{mode: PromptCacheKeyCustom, key: key}
}

// OmitPromptCacheKey returns a policy that leaves prompt_cache_key out.
func OmitPromptCacheKey() PromptCacheKey {
	return PromptCacheKey{mode: PromptCacheKeyOmit}
}

// Mode reports the policy mode.
func (p PromptCacheKey) Mode() PromptCacheKeyMode {
	return p.mode
}

// CustomKey reports the caller key for a custom policy.
func (p PromptCacheKey) CustomKey() (string, bool) {
	if p.mode != PromptCacheKeyCustom {
		return "", false
	}
	return p.key, true
}

// IsZero reports whether the policy is the default session-derived policy.
func (p PromptCacheKey) IsZero() bool {
	return p == PromptCacheKey{}
}

// Resolve returns the prompt_cache_key to send for sessionDerived and whether
// the request body should include it. Go's Responses payload elides an empty
// key, so an empty custom key degrades to omission.
func (p PromptCacheKey) Resolve(sessionDerived string) (string, bool) {
	switch p.mode {
	case PromptCacheKeyCustom:
		return p.key, true
	case PromptCacheKeyOmit:
		return "", false
	default:
		return sessionDerived, true
	}
}
