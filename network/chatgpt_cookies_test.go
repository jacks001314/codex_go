package network

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestChatGPTCookieScopeMatchesRust covers Rust #46506's scope rules: only
// ChatGPT HTTPS targets (and secure WebSocket handshakes, which share the HTTPS
// scope) have shared cookies, while insecure ws targets and other hosts never do.
func TestChatGPTCookieScopeMatchesRust(t *testing.T) {
	jar := newChatGPTCloudflareCookieJar()
	https, err := url.Parse("https://shared-scope.chatgpt.com/api")
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(https, []*http.Cookie{{Name: "__oailb", Value: "from-http", Path: "/", Secure: true}})

	// The shared jar answers for the HTTPS URL itself...
	if cookies := jar.Cookies(https); len(cookies) != 1 || cookies[0].Value != "from-http" {
		t.Fatalf("https cookies = %#v", cookies)
	}
	// ...and the helpers reach the same scope from a secure WebSocket URL.
	if header := chatGPTCookieHeaderFromJar(jar, "wss://shared-scope.chatgpt.com/api"); header != "__oailb=from-http" {
		t.Fatalf("wss cookie header = %q", header)
	}
	// Insecure WebSocket targets and non-ChatGPT hosts stay out of scope.
	for _, target := range []string{
		"ws://shared-scope.chatgpt.com/api",
		"wss://shared-scope.example.com/api",
		"https://shared-scope.example.com/api",
		"not a url",
	} {
		if header := chatGPTCookieHeaderFromJar(jar, target); header != "" {
			t.Fatalf("cookie header for %q = %q, want none", target, header)
		}
	}
	// The jar itself only accepts HTTPS ChatGPT targets.
	other, _ := url.Parse("https://shared-scope.example.com/api")
	jar.SetCookies(other, []*http.Cookie{{Name: "__oailb", Value: "elsewhere"}})
	if cookies := jar.Cookies(other); len(cookies) != 0 {
		t.Fatalf("non-ChatGPT jar cookies = %#v", cookies)
	}
}

// TestChatGPTResponseCookiesOnlyRetainInfrastructureCookies mirrors Rust's
// allowlist: infrastructure cookies (including __oailb) are retained from a
// successful or rejected ChatGPT upgrade, while account and session cookies are
// never stored globally.
func TestChatGPTResponseCookiesOnlyRetainInfrastructureCookies(t *testing.T) {
	jar := newChatGPTCloudflareCookieJar()
	target, _ := url.Parse("https://retain.chatgpt.com/api")
	header := http.Header{}
	header.Add("Set-Cookie", "__oailb=from-wss; Path=/; Secure")
	header.Add("Set-Cookie", "__cf_bm=cf-value; Path=/; Secure")
	header.Add("Set-Cookie", "session=secret; Path=/; Secure")
	header.Add("Set-Cookie", "__Secure-next-auth.session-token=secret; Path=/; Secure")
	storeChatGPTResponseCookiesInJar(jar, "wss://retain.chatgpt.com/api", header)

	cookies := jar.Cookies(target)
	if len(cookies) != 2 {
		t.Fatalf("retained cookies = %#v", cookies)
	}
	for _, cookie := range cookies {
		if cookie.Name != "__oailb" && cookie.Name != "__cf_bm" {
			t.Fatalf("a non-infrastructure cookie was retained: %#v", cookie)
		}
	}
	// A deletion response removes a retained infrastructure cookie.
	deletion := http.Header{}
	deletion.Add("Set-Cookie", "__oailb=; Path=/; Max-Age=0")
	storeChatGPTResponseCookiesInJar(jar, "wss://retain.chatgpt.com/api", deletion)
	for _, cookie := range jar.Cookies(target) {
		if cookie.Name == "__oailb" {
			t.Fatalf("deleted cookie is still present: %#v", cookie)
		}
	}
	// Non-ChatGPT hosts are never stored.
	other, _ := url.Parse("https://retain.example.com/api")
	storeChatGPTResponseCookiesInJar(jar, "wss://retain.example.com/api", header)
	if cookies := jar.Cookies(other); len(cookies) != 0 {
		t.Fatalf("non-ChatGPT response cookies = %#v", cookies)
	}
}

// TestSharedChatGPTCookieHeaderReachesTheSharedStore covers the package-level
// helpers the WebSocket handshake uses.
func TestSharedChatGPTCookieHeaderReachesTheSharedStore(t *testing.T) {
	target, _ := url.Parse("https://shared-header.chatgpt.com/api")
	SharedChatGPTCookieJar().SetCookies(target, []*http.Cookie{{Name: "__oailb", Value: "shared", Path: "/", Secure: true}})
	t.Cleanup(func() {
		SharedChatGPTCookieJar().SetCookies(target, []*http.Cookie{{Name: "__oailb", Value: "", Path: "/", MaxAge: -1}})
	})
	if header := ChatGPTCookieHeader("wss://shared-header.chatgpt.com/api"); header != "__oailb=shared" {
		t.Fatalf("shared cookie header = %q", header)
	}
	if header := ChatGPTCookieHeader("wss://shared-header.example.com/api"); header != "" {
		t.Fatalf("out-of-scope cookie header = %q", header)
	}
	// Storing a rejected upgrade's infrastructure cookie is visible to the next
	// handshake header.
	responseHeaders := http.Header{}
	responseHeaders.Add("Set-Cookie", "__oailb=refreshed; Path=/; Secure")
	StoreChatGPTResponseCookies("wss://shared-header.chatgpt.com/api", responseHeaders)
	if header := ChatGPTCookieHeader("wss://shared-header.chatgpt.com/api"); header != "__oailb=refreshed" {
		t.Fatalf("refreshed cookie header = %q", header)
	}
}

// chatGPTCookieHeaderFromJar mirrors ChatGPTCookieHeader against a specific jar
// so the scope matrix stays isolated from the process-global store.
func chatGPTCookieHeaderFromJar(jar *ChatGPTCloudflareCookieJar, rawURL string) string {
	target := chatGPTCookieURL(rawURL)
	if target == nil {
		return ""
	}
	cookies := jar.Cookies(target)
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

func storeChatGPTResponseCookiesInJar(jar *ChatGPTCloudflareCookieJar, rawURL string, header http.Header) {
	target := chatGPTCookieURL(rawURL)
	if target == nil || header == nil {
		return
	}
	response := &http.Response{Header: header}
	jar.SetCookies(target, response.Cookies())
}
