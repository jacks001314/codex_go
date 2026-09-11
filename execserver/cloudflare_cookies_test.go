package execserver

import (
	"net/http"
	"net/url"
	"sort"
	"testing"
)

func TestChatGPTCloudflareCookieStoreMatchesRustAllowlist(t *testing.T) {
	jar := newChatGPTCloudflareCookieJar()
	chatGPTURL, _ := url.Parse("https://chatgpt.com/backend-api/codex/responses")
	apiURL, _ := url.Parse("https://api.openai.com/v1/responses")
	jar.SetCookies(chatGPTURL, []*http.Cookie{
		{Name: "__cflb", Value: "west", Secure: true, Path: "/"},
		{Name: "_cfuvid", Value: "visitor", Secure: true, Path: "/"},
		{Name: "cf_chl_rc_i", Value: "challenge", Secure: true, Path: "/"},
		{Name: "chatgpt_session", Value: "secret", Secure: true, Path: "/"},
	})

	var got []string
	for _, cookie := range jar.Cookies(chatGPTURL) {
		got = append(got, cookie.Name+"="+cookie.Value)
	}
	sort.Strings(got)
	want := []string{"__cflb=west", "_cfuvid=visitor", "cf_chl_rc_i=challenge"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("ChatGPT cookies = %v, want %v", got, want)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("ChatGPT cookies = %v, want %v", got, want)
	}
	if cookies := jar.Cookies(apiURL); len(cookies) != 0 {
		t.Fatalf("non-ChatGPT cookies = %#v", cookies)
	}
}

// Mirrors Rust #43895: the __oailb routing cookie is replayed with its host,
// path, and HTTPS scope, and removed with Max-Age=0.
func TestChatGPTCloudflareCookieStoreReplaysOailbWithScope(t *testing.T) {
	jar := newChatGPTCloudflareCookieJar()
	setURL, _ := url.Parse("https://chatgpt.com/backend-api/codex/responses")
	jar.SetCookies(setURL, []*http.Cookie{{
		Name:     "__oailb",
		Value:    "route",
		Path:     "/backend-api",
		MaxAge:   3600,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}})

	followup, _ := url.Parse("https://chatgpt.com/backend-api/ps/mcp")
	cookies := jar.Cookies(followup)
	if len(cookies) != 1 || cookies[0].Name != "__oailb" || cookies[0].Value != "route" {
		t.Fatalf("__oailb cookies = %#v", cookies)
	}
	for _, rawURL := range []string{
		"https://chatgpt.com/",
		"https://other.chatgpt.com/backend-api/ps/mcp",
		"https://api.openai.com/backend-api/ps/mcp",
		"http://chatgpt.com/backend-api/ps/mcp",
	} {
		target, _ := url.Parse(rawURL)
		if cookies := jar.Cookies(target); len(cookies) != 0 {
			t.Fatalf("__oailb cookies for %s = %#v, want none", rawURL, cookies)
		}
	}

	// Max-Age=0 (Go: MaxAge<0) removes the cookie.
	jar.SetCookies(setURL, []*http.Cookie{{Name: "__oailb", Value: "", Path: "/backend-api", MaxAge: -1, Secure: true}})
	if cookies := jar.Cookies(followup); len(cookies) != 0 {
		t.Fatalf("expired __oailb cookies = %#v", cookies)
	}
}

func TestChatGPTCloudflareCookieStoreRejectsHTTPAndSuffixTricksLikeRust(t *testing.T) {
	jar := newChatGPTCloudflareCookieJar()
	for _, rawURL := range []string{
		"http://chatgpt.com/backend-api/codex/responses",
		"https://evilchatgpt.com/backend-api/codex/responses",
		"https://chatgpt.com.evil.example/backend-api/codex/responses",
		"https://foo.chat.openai.com/backend-api/codex/responses",
	} {
		target, _ := url.Parse(rawURL)
		jar.SetCookies(target, []*http.Cookie{{Name: "__cflb", Value: "west", Secure: true, Path: "/"}})
		if cookies := jar.Cookies(target); len(cookies) != 0 {
			t.Fatalf("cookies for %s = %#v", rawURL, cookies)
		}
	}
}
