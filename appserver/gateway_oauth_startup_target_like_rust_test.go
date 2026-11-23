package appserver

import (
	"encoding/json"
	"testing"
)

// Rust #62859: account/gatewayOAuth/login gained an optional explicit target
// that only the dedicated --startup-oauth connection may use. The regular
// app-server handler rejects it before a sign-in starts, and the params wire
// shape keeps "target": null when unrelated callers sign in.
func TestGatewayOAuthLoginRejectsExplicitStartupTargetLikeRust(t *testing.T) {
	router, _, _ := newGatewayOAuthRouter(t)
	request := requestWithParams(t, IntID(2), MethodGatewayOAuthLogin, map[string]any{
		"modelProvider": "gateway",
		"target":        map[string]any{"kind": GatewayOAuthTargetConfigServer},
	})
	response := router.Handle(request)
	if response.Error == nil {
		t.Fatal("regular login accepted an explicit startup target")
	}
	const wantMessage = "Explicit gateway OAuth targets are available only during startup"
	if response.Error.Code != JSONRPCInvalidRequestErrorCode || response.Error.Message != wantMessage {
		t.Fatalf("error = %+v, want code %d message %q", response.Error, JSONRPCInvalidRequestErrorCode, wantMessage)
	}
	router.gatewayLoginMu.Lock()
	active := router.gatewayLogin
	router.gatewayLoginMu.Unlock()
	if active != nil {
		t.Fatal("rejected login left an active gateway sign-in")
	}
}

func TestGatewayOAuthLoginParamsSerializeStartupTargetLikeRust(t *testing.T) {
	provider := "company"
	encoded, err := json.Marshal(&GatewayOAuthLoginParams{ModelProvider: &provider, DeviceAuth: true})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	const want = `{"modelProvider":"company","target":null,"deviceAuth":true}`
	if string(encoded) != want {
		t.Fatalf("login params = %s, want %s", encoded, want)
	}
	root := rustAppServerProtocolSchemaRoot(t)
	requireJSONMatchesRustSchema(t, root, "NullableGatewayOAuthLoginParams", &GatewayOAuthLoginParams{
		Target: &GatewayOAuthTarget{Kind: GatewayOAuthTargetConfigServer},
	})
	requireJSONMatchesRustSchema(t, root, "NullableGatewayOAuthLoginParams", &GatewayOAuthLoginParams{ModelProvider: &provider, DeviceAuth: true})
}

func TestStartupGatewayOAuthChangedNotificationSerializesLikeRust(t *testing.T) {
	notification := StartupGatewayOAuthChangedNotification{
		Target:  GatewayOAuthTarget{Kind: GatewayOAuthTargetConfigServer},
		Status:  GatewayOAuthStatusStarted,
		AuthURL: "https://example.test/startup-auth",
	}
	encoded, err := json.Marshal(&notification)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	const want = `{"target":{"kind":"configServer"},"status":"started","authUrl":"https://example.test/startup-auth"}`
	if string(encoded) != want {
		t.Fatalf("notification = %s, want %s", encoded, want)
	}
	requireJSONMatchesRustSchema(t, rustAppServerProtocolSchemaRoot(t), "StartupGatewayOAuthChangedNotification", &notification)
}
