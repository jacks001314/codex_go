package execserver

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
)

var sharedChatGPTCloudflareCookieJar = newChatGPTCloudflareCookieJar()

type chatGPTCloudflareCookieJar struct {
	jar http.CookieJar
}

func newChatGPTCloudflareCookieJar() *chatGPTCloudflareCookieJar {
	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(err)
	}
	return &chatGPTCloudflareCookieJar{jar: jar}
}

func (j *chatGPTCloudflareCookieJar) SetCookies(target *url.URL, cookies []*http.Cookie) {
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

func (j *chatGPTCloudflareCookieJar) Cookies(target *url.URL) []*http.Cookie {
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
	default:
		return strings.HasPrefix(name, "cf_chl_")
	}
}
