package runtimeauth

import "net/http"

// actorCredentialHeaders are the request headers a runtime selection owns. Rust
// `replace_auth_headers` (runtime-agent-instance/src/auth.rs:203-213) removes all
// of them before writing the current selection, so switching modes or rotating a
// runtime never leaves the previous mode's credential behind.
var actorCredentialHeaders = []string{
	"Authorization",
	"x-openai-agent-assertion",
	"ChatGPT-Account-ID",
	"x-openai-fedramp",
}

// ReplaceAuthHeaders writes src into dst with replace semantics: every header a
// runtime selection owns is deleted first, then src is added. Merging instead of
// replacing keeps a dual-header Authorization next to an assertion-only one,
// which is the failure this exists to prevent.
func ReplaceAuthHeaders(dst http.Header, src http.Header) {
	if dst == nil {
		return
	}
	for _, name := range actorCredentialHeaders {
		dst.Del(name)
	}
	for name, values := range src {
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}
