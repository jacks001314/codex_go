package network

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
)

// routingCookieNames are the routing hints a ChatGPT WebSocket handshake can
// return. Rust #57710 keeps only these for a connection-owned pin.
var routingCookieNames = []string{"__oailb", "__cflb"}

// RoutingAffinity is a routing pin captured from one ChatGPT WebSocket
// handshake, independent of the process-wide cookie jar (Rust #57710's
// ChatGptRoutingAffinity). It is a best-effort hint: the edge still validates
// the cookie and may fail over. Equality is identity: one zero-size marker
// pointer per captured connection, so a live colocated reviewer socket is not
// disconnected when its handshake cookie merely expires.
type RoutingAffinity struct {
	target *url.URL
	jar    http.CookieJar
	// id gives every captured pin a stable identity for pointer comparison,
	// mirroring Rust's Arc::ptr_eq on the private jar.
	id *struct{}
}

// RoutingAffinityFromResponse captures valid `__oailb` and Cloudflare's native
// `__cflb` response cookies (Rust ChatGptRoutingAffinity::from_response).
// Ambiguous or oversized cookies reject the pin; request cookies alone cannot
// establish where failover landed. Cookie scope and expiry stay owned by the
// private jar. The returned nil means the handshake produced no usable pin.
func RoutingAffinityFromResponse(rawURL string, header http.Header) *RoutingAffinity {
	target := chatGPTCookieURL(rawURL)
	if target == nil || header == nil {
		return nil
	}
	cookies := make([]*http.Cookie, 0, len(routingCookieNames))
	for _, name := range routingCookieNames {
		var match *http.Cookie
		matches := 0
		for _, raw := range header.Values("Set-Cookie") {
			cookie, err := http.ParseSetCookie(raw)
			if err != nil || cookie == nil || !strings.EqualFold(strings.TrimSpace(cookie.Name), name) {
				continue
			}
			matches++
			if len(raw) > 8192 {
				return nil
			}
			match = cookie
		}
		if matches > 1 {
			// Ambiguous pins cannot identify where failover landed.
			return nil
		}
		if match != nil {
			cookies = append(cookies, match)
		}
	}
	if len(cookies) == 0 {
		return nil
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil
	}
	jar.SetCookies(target, cookies)
	if len(jar.Cookies(target)) == 0 {
		return nil
	}
	return &RoutingAffinity{target: target, jar: jar, id: &struct{}{}}
}

// CookieHeader returns the still-valid pin only for the same secure ChatGPT
// origin and endpoint path (Rust CookieHeader). The private jar retains
// Max-Age/Expires and is never refreshed by unrelated traffic. The second
// result is false when the pin does not apply.
func (a *RoutingAffinity) CookieHeader(rawURL string) (string, bool) {
	if a == nil || a.jar == nil || a.target == nil {
		return "", false
	}
	target := chatGPTCookieURL(rawURL)
	if target == nil {
		return "", false
	}
	if !sameCookieOrigin(a.target, target) || a.target.Path != target.Path {
		return "", false
	}
	return formatRoutingCookies(a.jar.Cookies(target))
}

// MergeCookieHeader replaces routing cookies with the scoped pin while
// preserving unrelated cookies (Rust MergeCookieHeader). It reports false when
// the pin is no longer valid for this endpoint.
func (a *RoutingAffinity) MergeCookieHeader(rawURL string, existing string) (string, bool) {
	pin, ok := a.CookieHeader(rawURL)
	if !ok {
		return "", false
	}
	parts := make([]string, 0, 4)
	for _, part := range strings.Split(existing, ";") {
		part = strings.TrimSpace(part)
		if part == "" || isRoutingCookiePair(part) {
			continue
		}
		parts = append(parts, part)
	}
	parts = append(parts, pin)
	return strings.Join(parts, "; "), true
}

func formatRoutingCookies(cookies []*http.Cookie) (string, bool) {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil || strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "; "), true
}

func isRoutingCookiePair(pair string) bool {
	name, _, ok := strings.Cut(pair, "=")
	if !ok {
		return false
	}
	name = strings.TrimSpace(name)
	for _, routing := range routingCookieNames {
		if name == routing {
			return true
		}
	}
	return false
}

func sameCookieOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}
