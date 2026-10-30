package network

import (
	"net/http"
	"sort"
	"strings"
	"testing"
)

// Mirrors Rust #57710 (chatgpt_routing_affinity_tests): a pin keeps only the
// handshake's routing cookies with their scope and expiry, merges over
// unrelated cookies, and never escapes its origin or endpoint path.
func TestRoutingAffinityRetainsOnlyTheResponsePinLikeRust(t *testing.T) {
	const uri = "wss://affinity.chatgpt.com/backend-api/codex/responses"
	header := http.Header{}
	header.Add("Set-Cookie", "__oailb=parent; Path=/backend-api; Max-Age=3600; Secure")
	header.Add("Set-Cookie", "__cflb=parent-native; Path=/backend-api; Max-Age=3600; Secure")
	header.Add("Set-Cookie", "chatgpt_session=secret; Path=/; Secure")

	affinity := RoutingAffinityFromResponse(uri, header)
	if affinity == nil {
		t.Fatal("RoutingAffinityFromResponse() = nil, want a pin")
	}
	pin, ok := affinity.CookieHeader(uri)
	if !ok {
		t.Fatal("CookieHeader() rejected the captured pin")
	}
	if got := sortedCookiePairs(pin); strings.Join(got, "; ") != "__cflb=parent-native; __oailb=parent" {
		t.Fatalf("pin = %q", got)
	}
	existing := "__oailb=old; cf_clearance=keep; __cflb=old-native; configured=keep"
	merged, ok := affinity.MergeCookieHeader(uri, existing)
	if !ok {
		t.Fatal("MergeCookieHeader() rejected the captured pin")
	}
	wantMerged := []string{"__cflb=parent-native", "__oailb=parent", "cf_clearance=keep", "configured=keep"}
	if got := sortedCookiePairs(merged); strings.Join(got, "; ") != strings.Join(wantMerged, "; ") {
		t.Fatalf("merged = %q, want %q", got, wantMerged)
	}
	if got, ok := affinity.MergeCookieHeader(uri, ""); !ok || got != pin {
		t.Fatalf("MergeCookieHeader(no existing) = %q, %v; want %q", got, ok, pin)
	}
	for _, outside := range []string{
		"ws://affinity.chatgpt.com/backend-api/codex/responses",
		"wss://other.chatgpt.com/backend-api/codex/responses",
		"wss://affinity.chatgpt.com:444/backend-api/codex/responses",
		"wss://affinity.chatgpt.com/backend-api/codex/other",
		"wss://api.openai.com/backend-api/codex/responses",
	} {
		if got, ok := affinity.CookieHeader(outside); ok || got != "" {
			t.Fatalf("CookieHeader(%s) = %q, %v; want none", outside, got, ok)
		}
		if got, ok := affinity.MergeCookieHeader(outside, existing); ok || got != "" {
			t.Fatalf("MergeCookieHeader(%s) = %q, %v; want none", outside, got, ok)
		}
	}

	for _, name := range routingCookieNames {
		single := http.Header{}
		single.Set("Set-Cookie", name+"=only; Path=/; Secure")
		pinned := RoutingAffinityFromResponse(uri, single)
		if pinned == nil {
			t.Fatalf("%s: pin = nil", name)
		}
		if got, ok := pinned.CookieHeader(uri); !ok || got != name+"=only" {
			t.Fatalf("%s pin = %q, %v", name, got, ok)
		}
		for _, invalid := range []string{
			"__oailb=deleted; Path=/; Max-Age=0; Secure",
			"__oailb=expired; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT; Secure",
			"__oailb=wrong-path; Path=/elsewhere; Secure",
			"__oailb=wrong-domain; Domain=other.chatgpt.com; Path=/; Secure",
			"chatgpt_session=secret; Path=/; Secure",
		} {
			headers := http.Header{}
			headers.Set("Set-Cookie", strings.ReplaceAll(invalid, "__oailb", name))
			if pin := RoutingAffinityFromResponse(uri, headers); pin != nil {
				t.Fatalf("%s: pin for %q = %#v, want nil", name, invalid, pin)
			}
		}
		duplicate := http.Header{}
		duplicate.Add("Set-Cookie", name+"=one; Path=/; Secure")
		duplicate.Add("Set-Cookie", name+"=two; Path=/; Secure")
		if pin := RoutingAffinityFromResponse(uri, duplicate); pin != nil {
			t.Fatalf("%s: ambiguous pin accepted", name)
		}
	}
	if got, ok := affinity.CookieHeader(uri); !ok || got != pin {
		t.Fatalf("pin changed after later captures: %q, %v", got, ok)
	}
}

// A pin is identified by the captured connection, not by cookie contents, so
// two handshakes with identical cookies are still distinct pins.
func TestRoutingAffinityIdentityIsPerConnectionLikeRust(t *testing.T) {
	const uri = "wss://affinity.chatgpt.com/backend-api/codex/responses"
	header := http.Header{}
	header.Set("Set-Cookie", "__oailb=same; Path=/; Secure")
	first := RoutingAffinityFromResponse(uri, header)
	second := RoutingAffinityFromResponse(uri, header)
	if first == nil || second == nil {
		t.Fatal("expected two pins")
	}
	if first == second {
		t.Fatal("identical cookies produced the same pin identity")
	}
}

func sortedCookiePairs(header string) []string {
	parts := strings.Split(header, "; ")
	sort.Strings(parts)
	return parts
}
