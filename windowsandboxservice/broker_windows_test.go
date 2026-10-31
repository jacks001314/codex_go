//go:build windows

package windowsandboxservice

import (
	"errors"
	"testing"
)

func TestRunProvisioningPipeStopsBeforeOpeningTheListener(t *testing.T) {
	ready := false
	err := RunProvisioningPipe(
		func() bool { return true },
		BrokerHooks{OnReady: func() error { ready = true; return nil }},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("RunProvisioningPipe() error = %v", err)
	}
	if ready {
		t.Fatal("RunProvisioningPipe() published readiness after shutdown")
	}
}

func TestRunProvisioningPipeRequiresAShutdownPredicate(t *testing.T) {
	if err := RunProvisioningPipe(nil, BrokerHooks{}, nil, nil); err == nil {
		t.Fatal("RunProvisioningPipe() served without a shutdown predicate")
	}
}

func TestRefreshProvisioningSessionSkipsRefreshingAfterShutdown(t *testing.T) {
	refreshed := false
	active, err := refreshProvisioningSession(
		func() bool { return true },
		func() error { refreshed = true; return nil },
	)
	if err != nil {
		t.Fatalf("refreshProvisioningSession() error = %v", err)
	}
	if active || refreshed {
		t.Fatalf("refreshProvisioningSession() = %t with refreshed = %t, want a stopped session", active, refreshed)
	}
}

func TestRefreshProvisioningSessionPropagatesOwnerRestoreFailures(t *testing.T) {
	failure := errors.New("restore the signed-in owner")
	active, err := refreshProvisioningSession(
		func() bool { return false },
		func() error { return failure },
	)
	if !errors.Is(err, failure) || active {
		t.Fatalf("refreshProvisioningSession() = %t, %v", active, err)
	}
}

func TestRefreshProvisioningSessionKeepsAnActiveSession(t *testing.T) {
	active, err := refreshProvisioningSession(
		func() bool { return false },
		func() error { return nil },
	)
	if err != nil || !active {
		t.Fatalf("refreshProvisioningSession() = %t, %v", active, err)
	}
}
