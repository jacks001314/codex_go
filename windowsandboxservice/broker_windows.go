//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox/provisionservice"
)

// BrokerHooks are the lifecycle callbacks the accept loop performs around
// serving clients (Rust ipc::run's on_ready and on_session_change).
type BrokerHooks struct {
	// OnReady publishes listener readiness once the pipe exists.
	OnReady func() error
	// OnSessionChange refreshes the recorded owner before each admission.
	OnSessionChange func() error
}

// RunProvisioningPipe ports ipc::run: publish readiness, then accept one client
// at a time, refresh the recorded owner between connections, authenticate the
// client process, dispatch the policy-checked request and write one response.
func RunProvisioningPipe(stopped func() bool, hooks BrokerHooks, handler AuthenticatedRequestHandler, policy PolicyValidator) error {
	if stopped == nil {
		return errors.New("the provisioning broker needs a shutdown predicate")
	}
	if stopped() {
		return nil
	}
	listener, err := OpenProvisioningListener()
	if err != nil {
		return err
	}
	defer func() { listener.Close() }()
	if hooks.OnReady != nil {
		if err := hooks.OnReady(); err != nil {
			return fmt.Errorf("publish provisioning listener readiness: %w", err)
		}
	}
	// Refresh before blocking even if a direct setup claim's zero-byte wake was lost.
	for {
		active, err := refreshProvisioningSession(stopped, hooks.OnSessionChange)
		if err != nil {
			return err
		}
		if !active {
			break
		}
		connected, err := AcceptPipeConnection(listener.Pipe)
		if err != nil {
			return err
		}
		// A session refresh can finish cleanup. Never dispatch an already accepted
		// connection after it stops the listener, including a disconnected wakeup.
		active, err = refreshProvisioningSession(stopped, hooks.OnSessionChange)
		if err != nil {
			return err
		}
		if !active {
			break
		}
		if !connected {
			continue
		}
		process, err := AuthorizeClientProcess(listener.Pipe)
		if err != nil {
			_ = windows.DisconnectNamedPipe(listener.Pipe)
			continue
		}
		response := HandleRequest(listener.Pipe, process, listener.SandboxSID, stopped, policy, handler)
		process.Close()
		if err := WriteResponse(listener.Pipe, response, stopped); err != nil {
			return err
		}
		if stopped() {
			break
		}
		listener, err = listener.Refresh()
		if err != nil {
			return err
		}
	}
	return nil
}

func refreshProvisioningSession(stopped func() bool, onSessionChange func() error) (bool, error) {
	if stopped() {
		return false, nil
	}
	if onSessionChange != nil {
		if err := onSessionChange(); err != nil {
			return false, err
		}
	}
	return !stopped(), nil
}

// NewProvisioningBroker returns the broker the SCM host runs: it serves the
// provisioning pipe, enforces managed policy and registers uninstall owners
// through the lifecycle (Rust service::service_main_inner and
// runtime_lifecycle::run). Owner-aware uninstall cleanup is not wired yet, so a
// session change restores the recorded owner without checking a removed
// runtime.
func NewProvisioningBroker(lifecycle *Lifecycle, setup SetupRunner) ServiceBroker {
	return func(state *ServiceState) error {
		// Release the retained pins and owner token when the service stops
		// (Rust PackageLifecycle::drop).
		defer lifecycle.Close()
		handler := func(identity *ClientIdentity, request *ServiceRequest, sandboxSID []byte, stopped func() bool) (provisionservice.Response, error) {
			return RunProvisioning(identity, request, sandboxSID, stopped, lifecycle.RegisterOwner, setup)
		}
		return RunProvisioningPipe(func() bool { return state.Shutdown() }, BrokerHooks{
			OnReady: func() error {
				if err := state.ReportStatus(ServiceRunning, NoError); err != nil {
					return err
				}
				LogInformation(EventServiceStarted, "The Codex sandbox service is running.")
				return nil
			},
			OnSessionChange: func() error { return restoreOwnerForBroker(lifecycle) },
		}, handler, MachinePolicyValidator)
	}
}

// restoreOwnerForBroker restores the recorded owner between admissions. A
// failure is logged and does not stop the listener, so other clients can still
// use elevated setup (Rust runtime_lifecycle::run restore_owner).
func restoreOwnerForBroker(lifecycle *Lifecycle) error {
	if err := lifecycle.RestoreRecordedOwner(); err != nil {
		LogError(EventServiceFailed, "unable to restore package uninstall listener: "+ErrorMessage(err))
	}
	return nil
}
