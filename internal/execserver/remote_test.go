package execserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestRunRemoteEnvironmentRegistersAndServesRendezvousWebSocket(t *testing.T) {
	rpcDone := make(chan error, 1)
	rendezvous := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			rpcDone <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"initialize","params":{"clientName":"remote-test"}}`)); err != nil {
			rpcDone <- err
			return
		}
		messageType, data, err := conn.Read(ctx)
		if err != nil {
			rpcDone <- err
			return
		}
		if messageType != websocket.MessageText {
			rpcDone <- errUnexpectedMessageType(messageType)
			return
		}
		if !bytes.Contains(data, []byte(`"id":1`)) || !bytes.Contains(data, []byte(`"sessionId"`)) {
			rpcDone <- errUnexpectedPayload(data)
			return
		}
		rpcDone <- nil
	}))
	defer rendezvous.Close()

	registerBody := make(chan remoteRegistrationRequest, 1)
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/cloud/environment/env-remote/register" {
			t.Errorf("registry request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer registry-token" {
			t.Errorf("Authorization = %q", got)
		}
		var body remoteRegistrationRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		registerBody <- body
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(remoteRegistrationResponse{
			EnvironmentID:          "env-remote",
			URL:                    "ws" + strings.TrimPrefix(rendezvous.URL, "http") + "/relay?role=environment",
			SecurityProfile:        RemoteSecurityProfile,
			ExecutorRegistrationID: "registration-1",
		})
	}))
	defer registry.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunRemoteEnvironment(ctx, RemoteEnvironmentConfig{
			BaseURL:       registry.URL + "/",
			EnvironmentID: " env-remote ",
			AuthHeaders:   http.Header{"Authorization": []string{"Bearer registry-token"}},
			Backoff:       time.Millisecond,
			MaxBackoff:    time.Millisecond,
		})
	}()

	select {
	case err := <-rpcDone:
		if err != nil {
			t.Fatalf("rendezvous RPC failed: %v", err)
		}
	case err := <-errCh:
		t.Fatalf("RunRemoteEnvironment exited early: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("remote environment did not connect to rendezvous")
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunRemoteEnvironment returned error after cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunRemoteEnvironment did not stop after cancel")
	}

	select {
	case body := <-registerBody:
		if body.SecurityProfile != RemoteSecurityProfile {
			t.Fatalf("security_profile = %q", body.SecurityProfile)
		}
		if body.ExecutorPublicKey.Suite != noiseChannelSuite || body.ExecutorPublicKey.X25519PublicKey == "" || body.ExecutorPublicKey.MLKEM768PublicKey == "" {
			t.Fatalf("executor_public_key = %#v", body.ExecutorPublicKey)
		}
	default:
		t.Fatal("registry did not receive register body")
	}
}

func TestRegisterRemoteEnvironmentReportsRegistryErrors(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"bad_auth","message":"token expired"}}`, http.StatusUnauthorized)
	}))
	defer registry.Close()

	_, err := registerRemoteEnvironment(context.Background(), RemoteEnvironmentConfig{
		BaseURL:       registry.URL,
		EnvironmentID: "env-remote",
		AuthHeaders:   http.Header{"Authorization": []string{"Bearer expired"}},
		HTTPClient:    remoteHTTPClient(),
	}, RemotePublicKey{Suite: noiseChannelSuite, X25519PublicKey: "x", MLKEM768PublicKey: "m"})
	if err == nil || !strings.Contains(err.Error(), "environment registry authentication error") || !strings.Contains(err.Error(), "token expired") {
		t.Fatalf("register error = %v", err)
	}
}

func errUnexpectedMessageType(messageType websocket.MessageType) error {
	return &unexpectedRemoteTestValue{value: fmt.Sprintf("%d", messageType)}
}

func errUnexpectedPayload(data []byte) error {
	return &unexpectedRemoteTestValue{value: string(data)}
}

type unexpectedRemoteTestValue struct {
	value string
}

func (e *unexpectedRemoteTestValue) Error() string {
	return "unexpected rendezvous response: " + e.value
}
