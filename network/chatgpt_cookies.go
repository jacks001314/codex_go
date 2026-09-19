package network

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
)

// ChatGPT infrastructure cookies shared by the HTTP and WebSocket transports
// (Rust http-client's chatgpt_cloudflare_cookies, #46506). Only allowlisted
// infrastructure cookies may be stored globally; account and session cookies
// are never added to the shared store.
var sharedChatGPTCloudflareCookieJar = newChatGPTCloudflareCookieJar()

// ChatGPTCloudflareCookieJar is the process-global jar Codex HTTP clients and
// secure WebSocket handshakes share.
type ChatGPTCloudflareCookieJar struct {
	jar http.CookieJar
}

func newChatGPTCloudflareCookieJar() *ChatGPTCloudflareCookieJar {
	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(err)
	}
	return &ChatGPTCloudflareCookieJar{jar: jar}
}

// SharedChatGPTCookieJar returns the process-global ChatGPT cookie jar.
func SharedChatGPTCookieJar() *ChatGPTCloudflareCookieJar {
	return sharedChatGPTCloudflareCookieJar
}

func (j *ChatGPTCloudflareCookieJar) SetCookies(target *url.URL, cookies []*http.Cookie) {
	if j == nil || j.jar == nil || !isChatGPTCookieURL(target) {
		return
	}
	allowed := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie != nil && isAllowedCloudflareCookieName(cookie.Name) {
			allowed = append(allowed, cookie)
		}
	}
	if len(allowed) > 0 {
		j.jar.SetCookies(target, allowed)
	}
}

func (j *ChatGPTCloudflareCookieJar) Cookies(target *url.URL) []*http.Cookie {
	if j == nil || j.jar == nil || !isChatGPTCookieURL(target) {
		return nil
	}
	cookies := j.jar.Cookies(target)
	allowed := cookies[:0]
	for _, cookie := range cookies {
		if cookie != nil && isAllowedCloudflareCookieName(cookie.Name) {
			allowed = append(allowed, cookie)
		}
	}
	return allowed
}

// ChatGPTCookieHeader returns the shared ChatGPT cookies for a ChatGPT HTTPS or
// secure-WebSocket request target, formatted as a Cookie header value (Rust
// HttpClientFactory::chatgpt_cookie_header). It returns "" when the target is
// not a ChatGPT HTTPS/WSS URL, and callers keep an explicit Cookie header.
func ChatGPTCookieHeader(rawURL string) string {
	target := chatGPTCookieURL(rawURL)
	if target == nil {
		return ""
	}
	cookies := SharedChatGPTCookieJar().Cookies(target)
	if len(cookies) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		parts = append(parts, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(parts, "; ")
}

// StoreChatGPTResponseCookies retains the allowlisted infrastructure cookies of
// a ChatGPT HTTPS or secure-WebSocket response (Rust
// HttpClientFactory::store_chatgpt_response_cookies). A rejected WebSocket
// upgrade can refresh infrastructure cookies exactly like an HTTP response.
func StoreChatGPTResponseCookies(rawURL string, header http.Header) {
	target := chatGPTCookieURL(rawURL)
	if target == nil || header == nil {
		return
	}
	response := &http.Response{Header: header}
	SharedChatGPTCookieJar().SetCookies(target, response.Cookies())
}

// chatGPTCookieURL maps a ChatGPT HTTPS or secure-WebSocket request target to
// the HTTPS URL the shared cookie scope uses (Rust #46506: a secure WebSocket
// handshake has the same cookie scope as HTTPS). Insecure `ws` targets and
// non-ChatGPT hosts have no shared cookies.
func chatGPTCookieURL(rawURL string) *url.URL {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil {
		return nil
	}
	if parsed.Scheme == "wss" {
		parsed.Scheme = "https"
	}
	if !isChatGPTCookieURL(parsed) {
		return nil
	}
	return parsed
}

func isChatGPTCookieURL(target *url.URL) bool {
	if target == nil || target.Scheme != "https" {
		return false
	}
	return isAllowedChatGPTHost(strings.ToLower(target.Hostname()))
}

func isAllowedChatGPTHost(host string) bool {
	switch host {
	case "chatgpt.com", "chat.openai.com", "chatgpt-staging.com":
		return true
	default:
		return strings.HasSuffix(host, ".chatgpt.com") || strings.HasSuffix(host, ".chatgpt-staging.com")
	}
}

func isAllowedCloudflareCookieName(name string) bool {
	switch name {
	case "__cf_bm", "__cflb", "__cfruid", "__cfseq", "__cfwaitingroom", "_cfuvid", "cf_clearance", "cf_ob_info", "cf_use_ob":
		return true
	// `__oailb` is an OpenAI infrastructure routing cookie, not an
	// authentication cookie (Rust #43895).
	case "__oailb":
		return true
	default:
		return strings.HasPrefix(name, "cf_chl_")
	}
}
