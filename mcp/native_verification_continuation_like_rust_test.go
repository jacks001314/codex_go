package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These tests mirror Rust #44346: native user verification travels through the
// modern MCP tool-input envelope, is validated before any prompt is shown, and
// a submitted proof blocks session recovery from restarting the tool.

type nativeContinuationRPC struct {
	ID     int64          `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

func serveNativeContinuationDiscover(t *testing.T, w http.ResponseWriter, id int64) {
	t.Helper()
	w.Header().Set(mcpHTTPSessionIDHeader, "session-native")
	writeHTTPMCPResponse(t, w, id, map[string]any{
		"resultType":        "complete",
		"supportedVersions": []string{modernMCPProtocol},
		"capabilities":      map[string]any{"tools": map[string]any{}},
		"serverInfo":        map[string]any{"name": "native", "version": "1"},
	})
}

func nativeVerificationRequestParams() map[string]any {
	return map[string]any{
		"mode":        UserVerificationMode,
		"title":       "Verify action",
		"description": "Verify the requested operation",
		"challenge":   "AQID",
	}
}

func nativeContinuationInputRequired(request map[string]any) map[string]any {
	return map[string]any{
		"resultType":    "input_required",
		"inputRequests": map[string]any{"verification": request},
		"requestState":  "opaque-state",
	}
}

func TestNativeVerificationContinuationValidatesAndPreservesLikeRust(t *testing.T) {
	richForm := map[string]any{
		"method": "openai/elicitation/create",
		"params": map[string]any{
			"mode":            "form",
			"message":         "Rich confirmation",
			"requestedSchema": map[string]any{"type": "object"},
		},
	}
	verification := map[string]any{
		"method": "openai/elicitation/create",
		"params": nativeVerificationRequestParams(),
	}
	standard := map[string]any{
		"method": "elicitation/create",
		"params": map[string]any{
			"message":         "Confirm",
			"requestedSchema": map[string]any{"type": "object"},
		},
	}
	var toolsCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request nativeContinuationRPC
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "server/discover":
			serveNativeContinuationDiscover(t, w, request.ID)
		case "tools/call":
			toolsCalls++
			if _, ok := request.Params["inputResponses"]; !ok {
				writeHTTPMCPResponse(t, w, request.ID, map[string]any{
					"resultType": "input_required",
					"inputRequests": map[string]any{
						"confirmation":     standard,
						"richConfirmation": richForm,
						"verification":     verification,
					},
					"requestState": "opaque-state",
				})
				return
			}
			if request.Params["requestState"] != "opaque-state" {
				t.Fatalf("requestState = %#v", request.Params["requestState"])
			}
			if request.Params["name"] != "verified_operation" {
				t.Fatalf("name = %#v", request.Params["name"])
			}
			if arguments, _ := request.Params["arguments"].(map[string]any); arguments["resource"] != "example" {
				t.Fatalf("arguments = %#v", request.Params["arguments"])
			}
			if meta, _ := request.Params["_meta"].(map[string]any); meta["caller"] != "preserved" {
				t.Fatalf("caller meta was not preserved: %#v", request.Params["_meta"])
			}
			want := map[string]any{
				"confirmation":     map[string]any{"action": "accept", "content": map[string]any{"confirmed": true}},
				"richConfirmation": map[string]any{"action": "accept", "content": map[string]any{"confirmed": true}},
				"verification":     map[string]any{"action": "accept", "content": map[string]any{"credentialId": "test-credential", "signature": "BAUG"}},
			}
			if got := request.Params["inputResponses"]; !reflect.DeepEqual(got, want) {
				t.Fatalf("inputResponses = %#v, want %#v", got, want)
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"resultType": "complete",
				"content":    []any{map[string]any{"type": "text", "text": "done"}},
			})
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	client := newMCPHTTPClientWithFeatures(&ServerConfig{URL: server.URL, ProtocolMode: MCPProtocol20260728}, true, true, nil)
	var result MCPToolCallResponse
	err := client.CallWithOptions(&httpClientCallOptions{
		ServerName: "native",
		Elicitation: MCPElicitationHandlerFunc(func(_ context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			if IsUserVerificationElicitation(request) {
				if request.Title != "Verify action" || request.Description != "Verify the requested operation" || request.Challenge != "AQID" {
					t.Errorf("verification prompt = %#v", request)
				}
				return &MCPElicitationResponse{
					Action:  MCPElicitationActionAccept,
					Content: map[string]any{"credentialId": "test-credential", "signature": "BAUG"},
				}, nil
			}
			return &MCPElicitationResponse{Action: MCPElicitationActionAccept, Content: map[string]any{"confirmed": true}}, nil
		}),
	}, "tools/call", map[string]any{
		"name":      "verified_operation",
		"arguments": map[string]any{"resource": "example"},
		"_meta":     map[string]any{"caller": "preserved"},
	}, &result)
	if err != nil {
		t.Fatalf("CallWithOptions() error = %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "done" {
		t.Fatalf("result = %#v", result)
	}
	if toolsCalls != 2 {
		t.Fatalf("tools/call count = %d, want 2", toolsCalls)
	}
}

func TestNativeVerificationContinuationRejectsBeforePromptingLikeRust(t *testing.T) {
	malformed := map[string]any{
		"method": "openai/elicitation/create",
		"params": map[string]any{
			"mode":        UserVerificationMode,
			"title":       "Verify action",
			"description": "Verify the requested operation",
			"challenge":   "not base64!",
		},
	}
	form := map[string]any{
		"method": "openai/elicitation/create",
		"params": map[string]any{
			"mode":            "form",
			"message":         "Rich confirmation",
			"requestedSchema": map[string]any{"type": "object"},
		},
	}
	cases := []struct {
		name             string
		openAIForm       bool
		userVerification bool
		request          map[string]any
		wantMessage      string
	}{
		{
			name:        "unadvertised verification",
			request:     map[string]any{"method": "openai/elicitation/create", "params": nativeVerificationRequestParams()},
			wantMessage: "method not found: openai/elicitation/create",
		},
		{
			name:             "unadvertised form",
			userVerification: true,
			request:          form,
			wantMessage:      "invalid elicitation mode",
		},
		{
			name:             "malformed challenge",
			openAIForm:       true,
			userVerification: true,
			request:          malformed,
			wantMessage:      "invalid user-verification request",
		},
		{
			name:             "unsupported method",
			openAIForm:       true,
			userVerification: true,
			request:          map[string]any{"method": "arbitrary/custom", "params": map[string]any{}},
			wantMessage:      "unsupported MCP tool input request",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prompted := false
			toolsCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request nativeContinuationRPC
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatalf("Decode() error = %v", err)
				}
				switch request.Method {
				case "server/discover":
					serveNativeContinuationDiscover(t, w, request.ID)
				case "tools/call":
					toolsCalls++
					writeHTTPMCPResponse(t, w, request.ID, nativeContinuationInputRequired(tc.request))
				default:
					t.Fatalf("unexpected method %q", request.Method)
				}
			}))
			defer server.Close()

			client := newMCPHTTPClientWithFeatures(&ServerConfig{URL: server.URL, ProtocolMode: MCPProtocol20260728}, tc.openAIForm, tc.userVerification, nil)
			var result MCPToolCallResponse
			err := client.CallWithOptions(&httpClientCallOptions{
				ServerName: "native",
				Elicitation: MCPElicitationHandlerFunc(func(context.Context, *MCPElicitationRequest) (*MCPElicitationResponse, error) {
					prompted = true
					return &MCPElicitationResponse{Action: MCPElicitationActionCancel}, nil
				}),
			}, "tools/call", map[string]any{"name": "verified_operation", "arguments": map[string]any{}}, &result)
			if err == nil {
				t.Fatalf("expected error")
			}
			if prompted {
				t.Fatalf("rejected request still prompted the user")
			}
			var remote *MCPRemoteError
			if !errors.As(err, &remote) || !strings.Contains(remote.Message, tc.wantMessage) {
				t.Fatalf("error = %v, want message containing %q", err, tc.wantMessage)
			}
			if toolsCalls != 1 {
				t.Fatalf("tools/call count = %d, want 1", toolsCalls)
			}
		})
	}
}

func TestNativeVerificationContinuationRejectsUnsupportedModesLikeRust(t *testing.T) {
	for _, mode := range []struct {
		name  string
		value any
		set   bool
	}{
		{name: "missing"},
		{name: "null", value: nil, set: true},
		{name: "number", value: 0, set: true},
		{name: "url", value: "url", set: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			params := nativeVerificationRequestParams()
			if mode.set {
				params["mode"] = mode.value
			} else {
				delete(params, "mode")
			}
			prompted := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request nativeContinuationRPC
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatalf("Decode() error = %v", err)
				}
				switch request.Method {
				case "server/discover":
					serveNativeContinuationDiscover(t, w, request.ID)
				case "tools/call":
					writeHTTPMCPResponse(t, w, request.ID, nativeContinuationInputRequired(map[string]any{
						"method": "openai/elicitation/create",
						"params": params,
					}))
				default:
					t.Fatalf("unexpected method %q", request.Method)
				}
			}))
			defer server.Close()

			client := newMCPHTTPClientWithFeatures(&ServerConfig{URL: server.URL, ProtocolMode: MCPProtocol20260728}, true, true, nil)
			var result MCPToolCallResponse
			err := client.CallWithOptions(&httpClientCallOptions{
				ServerName: "native",
				Elicitation: MCPElicitationHandlerFunc(func(context.Context, *MCPElicitationRequest) (*MCPElicitationResponse, error) {
					prompted = true
					return &MCPElicitationResponse{Action: MCPElicitationActionCancel}, nil
				}),
			}, "tools/call", map[string]any{"name": "verified_operation", "arguments": map[string]any{}}, &result)
			var remote *MCPRemoteError
			if !errors.As(err, &remote) || remote.Code != mcpJSONRPCInvalidRequestCode || remote.Message != "unsupported OpenAI elicitation mode" {
				t.Fatalf("error = %v, want invalid_request for the unsupported mode", err)
			}
			if prompted {
				t.Fatalf("unsupported mode still prompted the user")
			}
		})
	}
}

func TestNativeVerificationContinuationBacksOffStateOnlyRoundsLikeRust(t *testing.T) {
	var callTimes []time.Time
	var requestStates []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request nativeContinuationRPC
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "server/discover":
			serveNativeContinuationDiscover(t, w, request.ID)
		case "tools/call":
			callTimes = append(callTimes, time.Now())
			requestStates = append(requestStates, request.Params["requestState"])
			switch len(callTimes) {
			case 1:
				writeHTTPMCPResponse(t, w, request.ID, nativeContinuationInputRequired(map[string]any{
					"method": "openai/elicitation/create",
					"params": nativeVerificationRequestParams(),
				}))
			case 2:
				writeHTTPMCPResponse(t, w, request.ID, map[string]any{
					"resultType":   "input_required",
					"requestState": "after-proof",
				})
			default:
				writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resultType": "complete", "content": []any{}})
			}
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	client := newMCPHTTPClientWithFeatures(&ServerConfig{URL: server.URL, ProtocolMode: MCPProtocol20260728}, true, true, nil)
	var result MCPToolCallResponse
	err := client.CallWithOptions(&httpClientCallOptions{
		ServerName: "native",
		Elicitation: MCPElicitationHandlerFunc(func(context.Context, *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			return &MCPElicitationResponse{
				Action:  MCPElicitationActionAccept,
				Content: map[string]any{"credentialId": "test", "signature": "BAUG"},
			}, nil
		}),
	}, "tools/call", map[string]any{"name": "verified_operation", "arguments": map[string]any{}}, &result)
	if err != nil {
		t.Fatalf("CallWithOptions() error = %v", err)
	}
	if len(callTimes) != 3 {
		t.Fatalf("tools/call count = %d, want 3", len(callTimes))
	}
	if got := callTimes[2].Sub(callTimes[1]); got < 45*time.Millisecond {
		t.Fatalf("state-only round backed off %v, want at least 50ms", got)
	}
	wantStates := []any{"opaque-state", "after-proof"}
	if got := requestStates[1:]; !reflect.DeepEqual(got, wantStates) {
		t.Fatalf("requestState sequence = %#v, want %#v", got, wantStates)
	}
}

func TestNativeVerificationContinuationDoesNotRestartAfterProofLikeRust(t *testing.T) {
	toolsCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request nativeContinuationRPC
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "server/discover":
			serveNativeContinuationDiscover(t, w, request.ID)
		case "tools/call":
			toolsCalls++
			if toolsCalls == 1 {
				writeHTTPMCPResponse(t, w, request.ID, nativeContinuationInputRequired(map[string]any{
					"method": "openai/elicitation/create",
					"params": nativeVerificationRequestParams(),
				}))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	client := newMCPHTTPClientWithFeatures(&ServerConfig{URL: server.URL, ProtocolMode: MCPProtocol20260728}, true, true, nil)
	var result MCPToolCallResponse
	err := client.CallWithOptions(&httpClientCallOptions{
		ServerName: "native",
		Elicitation: MCPElicitationHandlerFunc(func(context.Context, *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			return &MCPElicitationResponse{
				Action:  MCPElicitationActionAccept,
				Content: map[string]any{"credentialId": "test", "signature": "BAUG"},
			}, nil
		}),
	}, "tools/call", map[string]any{"name": "verified_operation", "arguments": map[string]any{}}, &result)
	if !errors.Is(err, errMCPToolContinuationNotRestarted) {
		t.Fatalf("error = %v, want the not-restarted error", err)
	}
	if toolsCalls != 2 {
		t.Fatalf("tools/call count = %d, want 2 (the tool must not be replayed)", toolsCalls)
	}
}

func TestNativeVerificationContinuationPreservesAuthChallengesLikeRust(t *testing.T) {
	toolsCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request nativeContinuationRPC
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "server/discover":
			serveNativeContinuationDiscover(t, w, request.ID)
		case "tools/call":
			toolsCalls++
			if toolsCalls == 1 {
				writeHTTPMCPResponse(t, w, request.ID, nativeContinuationInputRequired(map[string]any{
					"method": "openai/elicitation/create",
					"params": nativeVerificationRequestParams(),
				}))
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	client := newMCPHTTPClientWithFeatures(&ServerConfig{URL: server.URL, ProtocolMode: MCPProtocol20260728}, true, true, nil)
	var result MCPToolCallResponse
	err := client.CallWithOptions(&httpClientCallOptions{
		ServerName: "native",
		Elicitation: MCPElicitationHandlerFunc(func(context.Context, *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			return &MCPElicitationResponse{
				Action:  MCPElicitationActionAccept,
				Content: map[string]any{"credentialId": "test", "signature": "BAUG"},
			}, nil
		}),
	}, "tools/call", map[string]any{"name": "verified_operation", "arguments": map[string]any{}}, &result)
	var statusErr *mcpHTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized ||
		statusErr.CombinedWWWAuthenticate() != `Bearer error="invalid_token"` {
		t.Fatalf("error = %v, want the preserved auth challenge", err)
	}
	if toolsCalls != 2 {
		t.Fatalf("tools/call count = %d, want 2", toolsCalls)
	}
}
