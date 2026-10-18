# Rust #47204: standalone web search redirect proxy routes

Upstream commit: `6ea62c4396a1c0942a3ea271e062bdd9e0d2737f`

## Result

Equivalent behavior with Go's standard HTTP client.

- Rust's reqwest client pins a proxy route when each client is created, so the
  standalone web-search transport switches to a route-aware pool that resolves
  the route for every redirect URL.
- Go's `http.Transport` calls its `Proxy` function for every request, including
  redirect hops. The standalone web search client is built from
  `http.DefaultTransport` with `ProxyFromEnvironment` when
  `respect_system_proxy` is enabled, so each hop already selects its own route.
- Go replays the POST body for a 307 redirect through `Request.GetBody` and
  copies non-sensitive headers to the redirected request. Cross-origin
  Authorization is stripped, matching the route-aware pool's credential rule.
- No production change was needed. A regression test now pins the behavior.

## Verification

```text
go test ./turn -run 'TestWebSearchRedirectResolvesProxyRoutePerHopLikeRust' -count=1
go test ./turn -count=1
go test ./appserver -run 'StandaloneWebSearchHonorsSystemProxySettingLikeRust|WebSearchRuntime|AppStandaloneWebSearchEnabled' -count=1
```

The test sends the search through an initial proxy that answers with a 307 to a
second host, routed through a second proxy, and asserts that both proxied
requests carry the same POST body and Codex headers.

Passed on 2026-10-18.
