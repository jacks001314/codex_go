package appserverdaemon

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"

	"codex_go/internal/remotecontrol"
)

func TestLocalSocketRemoteControlClientEnable(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	serverDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(serverConn)
		encoder := json.NewEncoder(serverConn)
		if !scanner.Scan() {
			serverDone <- scanner.Err()
			return
		}
		var initialize JSONRPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &initialize); err != nil {
			serverDone <- err
			return
		}
		if initialize.Method != "initialize" {
			serverDone <- &unexpectedMethodError{got: initialize.Method, want: "initialize"}
			return
		}
		if err := encoder.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      InitializeRequestID,
			"result":  map[string]any{"userAgent": "codex/0.0.0"},
		}); err != nil {
			serverDone <- err
			return
		}
		if !scanner.Scan() {
			serverDone <- scanner.Err()
			return
		}
		var initialized JSONRPCNotification
		if err := json.Unmarshal(scanner.Bytes(), &initialized); err != nil {
			serverDone <- err
			return
		}
		if initialized.Method != "initialized" {
			serverDone <- &unexpectedMethodError{got: initialized.Method, want: "initialized"}
			return
		}
		if !scanner.Scan() {
			serverDone <- scanner.Err()
			return
		}
		var enable JSONRPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &enable); err != nil {
			serverDone <- err
			return
		}
		if enable.Method != "remoteControl/enable" {
			serverDone <- &unexpectedMethodError{got: enable.Method, want: "remoteControl/enable"}
			return
		}
		if err := encoder.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      RemoteControlRequestID,
			"result": map[string]any{
				"status":         remotecontrol.StatusConnected,
				"serverName":     "test-machine",
				"installationId": "install-1",
				"environmentId":  "env-1",
			},
		}); err != nil {
			serverDone <- err
			return
		}
		serverDone <- nil
	}()

	status, err := newLocalSocketRemoteControlClient(clientConn).enableRemoteControl()
	if err != nil {
		t.Fatalf("enableRemoteControl returned error: %v", err)
	}
	if status.Status != remotecontrol.StatusConnected || status.ServerName != "test-machine" || status.EnvironmentID == nil || *status.EnvironmentID != "env-1" {
		t.Fatalf("status = %#v", status)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server returned error: %v", err)
	}
}

func TestLocalSocketRemoteControlClientDisable(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	serverDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(serverConn)
		encoder := json.NewEncoder(serverConn)
		if !scanner.Scan() {
			serverDone <- scanner.Err()
			return
		}
		var initialize JSONRPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &initialize); err != nil {
			serverDone <- err
			return
		}
		if initialize.Method != "initialize" {
			serverDone <- &unexpectedMethodError{got: initialize.Method, want: "initialize"}
			return
		}
		if err := encoder.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      InitializeRequestID,
			"result":  map[string]any{"userAgent": "codex/0.0.0"},
		}); err != nil {
			serverDone <- err
			return
		}
		if !scanner.Scan() {
			serverDone <- scanner.Err()
			return
		}
		var initialized JSONRPCNotification
		if err := json.Unmarshal(scanner.Bytes(), &initialized); err != nil {
			serverDone <- err
			return
		}
		if initialized.Method != "initialized" {
			serverDone <- &unexpectedMethodError{got: initialized.Method, want: "initialized"}
			return
		}
		if !scanner.Scan() {
			serverDone <- scanner.Err()
			return
		}
		var disable JSONRPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &disable); err != nil {
			serverDone <- err
			return
		}
		if disable.Method != "remoteControl/disable" {
			serverDone <- &unexpectedMethodError{got: disable.Method, want: "remoteControl/disable"}
			return
		}
		if err := encoder.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      RemoteControlRequestID,
			"result": map[string]any{
				"status":         remotecontrol.StatusDisabled,
				"serverName":     "test-machine",
				"installationId": "install-1",
				"environmentId":  "env-1",
			},
		}); err != nil {
			serverDone <- err
			return
		}
		serverDone <- nil
	}()

	status, err := newLocalSocketRemoteControlClient(clientConn).disableRemoteControl()
	if err != nil {
		t.Fatalf("disableRemoteControl returned error: %v", err)
	}
	if status.Status != remotecontrol.StatusDisabled || status.ServerName != "test-machine" || status.EnvironmentID == nil || *status.EnvironmentID != "env-1" {
		t.Fatalf("status = %#v", status)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server returned error: %v", err)
	}
}

type unexpectedMethodError struct {
	got  string
	want string
}

func (e *unexpectedMethodError) Error() string {
	return "unexpected method " + e.got + ", want " + e.want
}
