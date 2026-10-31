//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

var nextTestPipe uint64

func uniqueTestPipeName() string {
	return fmt.Sprintf(`\\.\pipe\OpenAI.CodexSandbox.Tests.%d.%d`, os.Getpid(), atomic.AddUint64(&nextTestPipe, 1))
}

func createTestPipe(t *testing.T, name string) windows.Handle {
	t.Helper()
	namePointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatalf("UTF16PtrFromString(%q) error = %v", name, err)
	}
	pipe, err := windows.CreateNamedPipe(
		namePointer,
		pipeAccessDuplex|fileFlagFirstPipeInstance,
		pipeTypeByte|pipeReadModeByte|pipeWaitMode|pipeRejectRemoteClients,
		1,
		1024,
		1024,
		0,
		nil,
	)
	if err != nil {
		t.Fatalf("CreateNamedPipe(%q) error = %v", name, err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(pipe) })
	return pipe
}

func dialTestPipe(t *testing.T, name string) windows.Handle {
	t.Helper()
	namePointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatalf("UTF16PtrFromString(%q) error = %v", name, err)
	}
	// A reset instance becomes available asynchronously; poll like
	// WaitNamedPipeW does.
	var client windows.Handle
	for attempt := 0; attempt < 500; attempt++ {
		client, err = windows.CreateFile(
			namePointer,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0,
			nil,
			windows.OPEN_EXISTING,
			0,
			0,
		)
		if err == nil {
			return client
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			t.Fatalf("CreateFile(%q) error = %v", name, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("CreateFile(%q) error = %v", name, err)
	return 0
}

func TestUnpackagedPipeClientsAreRejectedBeforeSendingARequest(t *testing.T) {
	if _, present, err := provisionservice.ProcessPackageFamily(windows.CurrentProcess()); err != nil {
		t.Fatalf("ProcessPackageFamily() error = %v", err)
	} else if present {
		t.Skip("the test host runs with a package identity")
	}
	name := uniqueTestPipeName()
	server := createTestPipe(t, name)
	client := dialTestPipe(t, name)
	defer func() { _ = windows.CloseHandle(client) }()

	if err := windows.ConnectNamedPipe(server, nil); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		t.Fatalf("ConnectNamedPipe() error = %v", err)
	}
	process, err := AuthorizeClientProcess(server)
	if err == nil {
		process.Close()
		t.Fatal("an unpackaged pipe client was authorized without sending a request")
	}
	if !strings.Contains(err.Error(), "installed Codex package identity") {
		t.Fatalf("unexpected package authorization failure: %v", err)
	}
}

func TestDisconnectedPipeClientsDoNotPreventSubsequentConnections(t *testing.T) {
	name := uniqueTestPipeName()
	server := createTestPipe(t, name)
	first := dialTestPipe(t, name)
	_ = windows.CloseHandle(first)

	connected, err := AcceptPipeConnection(server)
	if err != nil {
		t.Fatalf("AcceptPipeConnection() error = %v", err)
	}
	if connected {
		t.Fatal("a client that connected and closed must be reported as a disconnected wakeup")
	}

	// The reset instance only becomes attachable once the server waits again.
	accepted := make(chan bool, 1)
	acceptErr := make(chan error, 1)
	go func() {
		connected, err := AcceptPipeConnection(server)
		accepted <- connected
		acceptErr <- err
	}()
	second := dialTestPipe(t, name)
	defer func() { _ = windows.CloseHandle(second) }()
	if err := <-acceptErr; err != nil {
		t.Fatalf("AcceptPipeConnection() after reset error = %v", err)
	}
	if !<-accepted {
		t.Fatal("the reset instance must accept a subsequent client")
	}
}

func TestAuthorizeSetupRuntimeRefusesLegacyWhileRegisteredCoreOwnsAccounts(t *testing.T) {
	restore := loadRegisteredRuntime
	t.Cleanup(func() { loadRegisteredRuntime = restore })
	loadRegisteredRuntime = func() (*windowssandbox.InstallationRecord, error) {
		return &windowssandbox.InstallationRecord{
			Runtime: &windowssandbox.RuntimeRegistration{PackageFamily: "OpenAI.Codex_testpublisher"},
		}, nil
	}
	request := &ServiceRequest{Provision: &ProvisioningRequest{CodexHome: `C:\Users\owner\home`}}
	_, err := AuthorizeSetupRuntime(nil, request)
	if err == nil || !strings.Contains(err.Error(), "legacy provisioning is not permitted") {
		t.Fatalf("AuthorizeSetupRuntime() error = %v, want the registered-Core refusal", err)
	}

	regression := &ServiceRequest{Register: &RegisterInstallationRequest{CodexHome: `C:\Users\owner\home`}}
	runtime, err := AuthorizeSetupRuntime(nil, regression)
	if err != nil {
		t.Fatalf("registration-only setup runtime error = %v", err)
	}
	if runtime != windowssandbox.SetupRuntimeLegacy {
		t.Fatalf("registration-only setup runtime = %q, want legacy", runtime)
	}
}

func TestRegisteredCoreRequiresAnInstalledServicePackage(t *testing.T) {
	_, present, err := provisionservice.ProcessPackageFamily(windows.CurrentProcess())
	if err != nil {
		t.Fatalf("ProcessPackageFamily() error = %v", err)
	}
	if present {
		t.Skip("the test host runs with a package identity")
	}
	request := &ServiceRequest{Provision: &ProvisioningRequest{
		CodexHome:      `C:\Users\owner\home`,
		RegisteredCore: true,
	}}
	_, err = AuthorizeSetupRuntime(&AuthorizedClientProcess{handle: windows.CurrentProcess()}, request)
	if err == nil || !strings.Contains(err.Error(), "installed package family") {
		t.Fatalf("registered Core admission error = %v, want the package-family refusal", err)
	}
}
