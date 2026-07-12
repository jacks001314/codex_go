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
