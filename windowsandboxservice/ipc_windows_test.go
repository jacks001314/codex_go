//go:build windows

package windowsandboxservice

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox/provisionservice"
)

func dialTestPipeFile(t *testing.T, name string) *os.File {
	t.Helper()
	handle := dialTestPipe(t, name)
	file := os.NewFile(uintptr(handle), name)
	if file == nil {
		_ = windows.CloseHandle(handle)
		t.Fatalf("os.NewFile(%q) = nil", name)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func writeAll(t *testing.T, file *os.File, data []byte) {
	t.Helper()
	if _, err := file.Write(data); err != nil {
		t.Fatalf("write provisioning request: %v", err)
	}
}

func registrationFrame(t *testing.T, home string) []byte {
	t.Helper()
	frame := &provisionservice.Frame{
		Version: provisionservice.ProtocolVersion,
		Message: provisionservice.Message{
			Kind:      provisionservice.KindRegisterInstallationRequest,
			CodexHome: home,
		},
	}
	var buffer bytes.Buffer
	if err := provisionservice.WriteFrame(&buffer, frame); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	return buffer.Bytes()
}

func TestReadProvisioningRequestAcceptsExactlyOneFrame(t *testing.T) {
	name := uniqueTestPipeName()
	server := createTestPipe(t, name)
	client := dialTestPipeFile(t, name)
	home := filepath.Join("C:", "Users", "owner", "home")
	frame := registrationFrame(t, home)
	writeAll(t, client, frame)

	raw, err := ReadProvisioningRequest(server, nil)
	if err != nil {
		t.Fatalf("ReadProvisioningRequest() error = %v", err)
	}
	if !bytes.Equal(raw, frame) {
		t.Fatalf("ReadProvisioningRequest() = %q, want %q", raw, frame)
	}
	request, err := ValidateRequest(raw)
	if err != nil {
		t.Fatalf("ValidateRequest() error = %v", err)
	}
	if request.Register == nil || request.Register.CodexHome != home {
		t.Fatalf("ValidateRequest() = %+v, want the registration request for %q", request, home)
	}
}

func TestReadProvisioningRequestRejectsASecondFrame(t *testing.T) {
	name := uniqueTestPipeName()
	server := createTestPipe(t, name)
	client := dialTestPipeFile(t, name)
	home := filepath.Join("C:", "Users", "owner", "home")
	writeAll(t, client, append(registrationFrame(t, home), registrationFrame(t, home)...))

	_, err := ReadProvisioningRequest(server, nil)
	if err == nil || !strings.Contains(err.Error(), "exactly one IPC frame") {
		t.Fatalf("ReadProvisioningRequest() error = %v, want the single-frame refusal", err)
	}
}

func TestReadProvisioningRequestRejectsAnOversizedFrame(t *testing.T) {
	name := uniqueTestPipeName()
	server := createTestPipe(t, name)
	client := dialTestPipeFile(t, name)
	payload := make([]byte, 14)
	binary.LittleEndian.PutUint32(payload[:4], MaxRequestBytes)
	writeAll(t, client, payload)

	_, err := ReadProvisioningRequest(server, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds size limit") {
		t.Fatalf("ReadProvisioningRequest() error = %v, want the size-limit refusal", err)
	}
}

func TestReadProvisioningRequestReportsShutdownBeforeReading(t *testing.T) {
	name := uniqueTestPipeName()
	server := createTestPipe(t, name)

	_, err := ReadProvisioningRequest(server, func() bool { return true })
	if err == nil || !strings.Contains(err.Error(), "service is stopping") {
		t.Fatalf("ReadProvisioningRequest() error = %v, want the shutdown refusal", err)
	}
}

func TestWriteResponseSendsOneFramedResponse(t *testing.T) {
	name := uniqueTestPipeName()
	server := createTestPipe(t, name)
	client := dialTestPipeFile(t, name)

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- WriteResponse(server, provisionservice.Response{Kind: provisionservice.ResponseUnavailable}, func() bool { return true })
	}()
	frame, err := provisionservice.ReadFrame(client)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("WriteResponse() error = %v", err)
	}
	if frame == nil || frame.Message.Response == nil {
		t.Fatalf("ReadFrame() = %+v, want a response frame", frame)
	}
	if frame.Message.Kind != provisionservice.KindProvisionSandboxResponse || frame.Message.Response.Kind != provisionservice.ResponseUnavailable {
		t.Fatalf("response frame = %+v, want unavailable", frame.Message)
	}
}
