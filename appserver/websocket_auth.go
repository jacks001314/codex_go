package appserver

// The shared WebSocket authentication policy lives in codex_go/websocketauth so
// the app-server and the exec-server listeners use one implementation (Rust
// extracted codex-websocket-auth for the same reason). These aliases keep the
// app-server's existing names available.

import "codex_go/websocketauth"

const (
	DefaultWebSocketMaxClockSkewSeconds = websocketauth.DefaultMaxClockSkewSeconds
	MinSignedBearerSecretBytes          = websocketauth.MinSignedBearerSecretBytes
)

type WebSocketAuthMode = websocketauth.Mode

const (
	WebSocketAuthCapabilityToken   = websocketauth.ModeCapabilityToken
	WebSocketAuthSignedBearerToken = websocketauth.ModeSignedBearerToken
)

type WebSocketAuthSettings = websocketauth.Settings

type WebSocketAuthPolicy = websocketauth.Policy

type WebSocketAuthError = websocketauth.Error

var NewWebSocketAuthPolicy = websocketauth.NewPolicy
