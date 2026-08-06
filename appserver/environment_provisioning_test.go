package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"codex_go/execserver"
)

// failingNoiseProvider mirrors Rust's FailingNoiseConnectProvider: it counts
// calls and always fails, so tests can assert that a provisioned environment
// delays connection until provisioning completes.
type failingNoiseProvider struct {
	calls atomic.Int64
}

func (p *failingNoiseProvider) callCount() int64 {
	return p.calls.Load()
}

func (p *failingNoiseProvider) ConnectBundle(context.Context, execserver.RemotePublicKey) (*execserver.NoiseRendezvousConnectBundle, error) {
	p.calls.Add(1)
	return nil, errors.New("test Noise provider called")
}

func provisionedRoot(id string, environmentID string) SelectedCapabilityRoot {
	return SelectedCapabilityRoot{
		ID: id,
		Location: CapabilityRootLocation{
			Type:          CapabilityRootLocationEnvironment,
			EnvironmentID: environmentID,
			Path:          "file:///plugins/root",
		},
	}
}

func provisionedReadyInfo(rootID string, environmentID string) EnvironmentReadyInfo {
	return EnvironmentReadyInfo{
		SelectedCapabilityRoots: []SelectedCapabilityRoot{provisionedRoot(rootID, environmentID)},
	}
}

func TestDeferredEnvironmentWaitsBeforeConnecting(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	provider := &failingNoiseProvider{}
	registration, err := manager.RegisterDeferredNoiseEnvironment("tools", provider)
	if err != nil {
		t.Fatalf("RegisterDeferredNoiseEnvironment() error = %v", err)
	}
	status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "tools"})
	if err != nil || status.Status != EnvironmentStatusPending {
		t.Fatalf("Status(pending) = %+v, %v", status, err)
	}
	if got := provider.callCount(); got != 0 {
		t.Fatalf("provider called %d times while pending, want 0", got)
	}
	if got := manager.SelectedCapabilityRoots("tools"); len(got) != 0 {
		t.Fatalf("SelectedCapabilityRoots(pending) = %+v, want empty", got)
	}

	readyInfo := provisionedReadyInfo("selected-root", "tools")
	if err := registration.CompleteReady(readyInfo); err != nil {
		t.Fatalf("CompleteReady() error = %v", err)
	}
	if got := manager.SelectedCapabilityRoots("tools"); len(got) != 1 || got[0].ID != "selected-root" {
		t.Fatalf("SelectedCapabilityRoots(ready) = %+v, want selected-root", got)
	}
	status, err = manager.Status(&EnvironmentStatusParams{EnvironmentID: "tools"})
	if err != nil || status.Status != EnvironmentStatusDisconnected || status.Error == nil || !strings.Contains(*status.Error, "test Noise provider called") {
		t.Fatalf("Status(ready, failing provider) = %+v, %v", status, err)
	}
	if got := provider.callCount(); got != 1 {
		t.Fatalf("provider called %d times after ready, want 1", got)
	}
}

func TestDeferredRegistrationReplacesAnOrdinaryNoiseEnvironment(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if err := manager.AddNoise("tools", &failingNoiseProvider{}); err != nil {
		t.Fatalf("AddNoise() error = %v", err)
	}
	record, ok := manager.Record("tools")
	if !ok || record.Provisioning != nil {
		t.Fatalf("ordinary record = %#v, %v", record, ok)
	}

	deferredProvider := &failingNoiseProvider{}
	registration, err := manager.RegisterDeferredNoiseEnvironment("tools", deferredProvider)
	if err != nil {
		t.Fatalf("RegisterDeferredNoiseEnvironment() error = %v", err)
	}
	record, ok = manager.Record("tools")
	if !ok || record.Provisioning == nil {
		t.Fatalf("deferred record = %#v, %v", record, ok)
	}
	status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "tools"})
	if err != nil || status.Status != EnvironmentStatusPending {
		t.Fatalf("Status(deferred) = %+v, %v", status, err)
	}
	if got := deferredProvider.callCount(); got != 0 {
		t.Fatalf("deferred provider called %d times, want 0", got)
	}
	if err := registration.CompleteReady(provisionedReadyInfo("selected-root", "tools")); err != nil {
		t.Fatalf("CompleteReady() error = %v", err)
	}
}

func TestOrdinaryNoiseEnvironmentReplacesADeferredRegistration(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	deferredProvider := &failingNoiseProvider{}
	registration, err := manager.RegisterDeferredNoiseEnvironment("tools", deferredProvider)
	if err != nil {
		t.Fatalf("RegisterDeferredNoiseEnvironment() error = %v", err)
	}
	deferredState := registration.state

	ordinaryProvider := &failingNoiseProvider{}
	if err := manager.AddNoise("tools", ordinaryProvider); err != nil {
		t.Fatalf("AddNoise() error = %v", err)
	}
	record, ok := manager.Record("tools")
	if !ok || record.Provisioning != nil {
		t.Fatalf("ordinary record = %#v, %v", record, ok)
	}

	registration.Abandon()
	status, _, failure := deferredState.Current()
	if status != ProvisioningFailed || !strings.Contains(failure, "registration ended before completion") {
		t.Fatalf("abandoned state = %s, %q", status, failure)
	}
	if got := deferredProvider.callCount(); got != 0 {
		t.Fatalf("deferred provider called %d times after replacement, want 0", got)
	}
	statusResp, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "tools"})
	if err != nil || statusResp.Status != EnvironmentStatusDisconnected || statusResp.Error == nil || !strings.Contains(*statusResp.Error, "test Noise provider called") {
		t.Fatalf("Status(ordinary) = %+v, %v", statusResp, err)
	}
	if got := ordinaryProvider.callCount(); got != 1 {
		t.Fatalf("ordinary provider called %d times, want 1", got)
	}
}

func TestFailureAndDroppedRegistrationAreTerminal(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	failedProvider := &failingNoiseProvider{}
	failed, err := manager.RegisterDeferredNoiseEnvironment("failed", failedProvider)
	if err != nil {
		t.Fatalf("RegisterDeferredNoiseEnvironment(failed) error = %v", err)
	}
	if err := failed.CompleteFailed("provisioning failed"); err != nil {
		t.Fatalf("CompleteFailed() error = %v", err)
	}
	status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "failed"})
	if err != nil || status.Status != EnvironmentStatusDisconnected || status.Error == nil || !strings.HasSuffix(*status.Error, "provisioning failed") {
		t.Fatalf("Status(failed) = %+v, %v", status, err)
	}
	if got := failedProvider.callCount(); got != 0 {
		t.Fatalf("failed provider called %d times, want 0", got)
	}

	droppedProvider := &failingNoiseProvider{}
	dropped, err := manager.RegisterDeferredNoiseEnvironment("dropped", droppedProvider)
	if err != nil {
		t.Fatalf("RegisterDeferredNoiseEnvironment(dropped) error = %v", err)
	}
	dropped.Abandon()
	status, err = manager.Status(&EnvironmentStatusParams{EnvironmentID: "dropped"})
	if err != nil || status.Status != EnvironmentStatusDisconnected || status.Error == nil || !strings.Contains(*status.Error, "registration ended before completion") {
		t.Fatalf("Status(dropped) = %+v, %v", status, err)
	}
	if got := droppedProvider.callCount(); got != 0 {
		t.Fatalf("dropped provider called %d times, want 0", got)
	}
	if _, ok := manager.Record("failed"); !ok {
		t.Fatal("failed environment should remain registered")
	}
	if _, ok := manager.Record("dropped"); !ok {
		t.Fatal("dropped environment should remain registered")
	}
}

func TestInvalidReadyInfoIsTerminal(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	provider := &failingNoiseProvider{}
	registration, err := manager.RegisterDeferredNoiseEnvironment("tools", provider)
	if err != nil {
		t.Fatalf("RegisterDeferredNoiseEnvironment() error = %v", err)
	}

	err = registration.CompleteReady(provisionedReadyInfo("selected-root", "other"))
	if err == nil || !strings.Contains(err.Error(), "belong to environment") {
		t.Fatalf("CompleteReady(invalid) error = %v, want belong-to-environment", err)
	}
	status, statusErr := manager.Status(&EnvironmentStatusParams{EnvironmentID: "tools"})
	if statusErr != nil || status.Status != EnvironmentStatusDisconnected || status.Error == nil || !strings.Contains(*status.Error, "belong to environment") {
		t.Fatalf("Status(failed by invalid info) = %+v, %v", status, statusErr)
	}
	if got := manager.SelectedCapabilityRoots("tools"); len(got) != 0 {
		t.Fatalf("SelectedCapabilityRoots(failed) = %+v, want empty", got)
	}
	if got := provider.callCount(); got != 0 {
		t.Fatalf("provider called %d times, want 0", got)
	}
}

func TestLateCompletionIsIsolatedFromReplacement(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	oldProvider := &failingNoiseProvider{}
	oldRegistration, err := manager.RegisterDeferredNoiseEnvironment("tools", oldProvider)
	if err != nil {
		t.Fatalf("RegisterDeferredNoiseEnvironment(old) error = %v", err)
	}
	oldState := oldRegistration.state

	currentProvider := &failingNoiseProvider{}
	currentRegistration, err := manager.RegisterDeferredNoiseEnvironment("tools", currentProvider)
	if err != nil {
		t.Fatalf("RegisterDeferredNoiseEnvironment(current) error = %v", err)
	}
	currentState := currentRegistration.state

	oldReadyInfo := provisionedReadyInfo("old-root", "tools")
	if err := oldRegistration.CompleteReady(oldReadyInfo); err != nil {
		t.Fatalf("CompleteReady(old) error = %v", err)
	}
	status, _, _ := oldState.Current()
	if status != ProvisioningReady {
		t.Fatalf("old state = %s, want ready", status)
	}
	_, oldInfo, _ := oldState.Current()
	if oldInfo == nil || len(oldInfo.SelectedCapabilityRoots) != 1 || oldInfo.SelectedCapabilityRoots[0].ID != "old-root" {
		t.Fatalf("old ready info = %+v, want old-root", oldInfo)
	}
	if got := manager.SelectedCapabilityRoots("tools"); len(got) != 0 {
		t.Fatalf("current SelectedCapabilityRoots = %+v, want empty while pending", got)
	}

	status, _, _ = currentState.Current()
	if status != ProvisioningPending {
		t.Fatalf("current state = %s, want pending", status)
	}
	if got := currentProvider.callCount(); got != 0 {
		t.Fatalf("current provider called %d times while pending, want 0", got)
	}

	currentReadyInfo := provisionedReadyInfo("current-root", "tools")
	if err := currentRegistration.CompleteReady(currentReadyInfo); err != nil {
		t.Fatalf("CompleteReady(current) error = %v", err)
	}
	if got := manager.SelectedCapabilityRoots("tools"); len(got) != 1 || got[0].ID != "current-root" {
		t.Fatalf("SelectedCapabilityRoots(current) = %+v, want current-root", got)
	}
}

func TestEagerNoiseEnvironmentConnectsWithoutRegistration(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	provider := &failingNoiseProvider{}
	if err := manager.AddNoise("tools", provider); err != nil {
		t.Fatalf("AddNoise() error = %v", err)
	}
	status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "tools"})
	if err != nil || status.Status != EnvironmentStatusDisconnected || status.Error == nil || !strings.Contains(*status.Error, "test Noise provider called") {
		t.Fatalf("Status(ordinary) = %+v, %v", status, err)
	}
	if got := provider.callCount(); got != 1 {
		t.Fatalf("provider called %d times, want 1", got)
	}
}

func TestReadinessBeforeMaterializationCreatesTheStableEnvironment(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	readinessProvider := &failingNoiseProvider{}
	materializationProvider := &failingNoiseProvider{}
	selected := provisionedReadyInfo("selected-root", "tools")

	ready, err := manager.ReportProvisioningStatus("tools", &selected, nil, readinessProvider)
	if err != nil || ready == nil {
		t.Fatalf("ReportProvisioningStatus(ready) = %#v, %v", ready, err)
	}
	materialized, err := manager.MaterializePendingNoiseEnvironment("tools", materializationProvider)
	if err != nil {
		t.Fatalf("MaterializePendingNoiseEnvironment() error = %v", err)
	}
	if ready.Provisioning != materialized.Provisioning {
		t.Fatal("materialization should return the same provisioned record created by the readiness report")
	}
	if got := manager.SelectedCapabilityRoots("tools"); len(got) != 1 || got[0].ID != "selected-root" {
		t.Fatalf("SelectedCapabilityRoots = %+v, want selected-root", got)
	}
	status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "tools"})
	if err != nil || status.Status != EnvironmentStatusDisconnected || status.Error == nil || !strings.Contains(*status.Error, "test Noise provider called") {
		t.Fatalf("Status = %+v, %v", status, err)
	}
	if got := readinessProvider.callCount(); got != 1 {
		t.Fatalf("readiness provider called %d times, want 1", got)
	}
	if got := materializationProvider.callCount(); got != 0 {
		t.Fatalf("materialization provider called %d times, want 0", got)
	}
}

func TestMaterializeThenReportReadyReusesThePendingEnvironment(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	pendingProvider := &failingNoiseProvider{}
	pending, err := manager.MaterializePendingNoiseEnvironment("tools", pendingProvider)
	if err != nil {
		t.Fatalf("MaterializePendingNoiseEnvironment() error = %v", err)
	}
	status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "tools"})
	if err != nil || status.Status != EnvironmentStatusPending {
		t.Fatalf("Status(pending) = %+v, %v", status, err)
	}
	if got := pendingProvider.callCount(); got != 0 {
		t.Fatalf("pending provider called %d times, want 0", got)
	}

	reportProvider := &failingNoiseProvider{}
	readyInfo := provisionedReadyInfo("selected-root", "tools")
	ready, err := manager.ReportProvisioningStatus("tools", &readyInfo, nil, reportProvider)
	if err != nil || ready == nil {
		t.Fatalf("ReportProvisioningStatus(ready) = %#v, %v", ready, err)
	}
	if pending.Provisioning != ready.Provisioning {
		t.Fatal("provisioning report should reuse the pending environment")
	}
	if got := manager.SelectedCapabilityRoots("tools"); len(got) != 1 || got[0].ID != "selected-root" {
		t.Fatalf("SelectedCapabilityRoots = %+v, want selected-root", got)
	}
	if got := pendingProvider.callCount(); got != 0 {
		t.Fatalf("pending provider called %d times, want 0", got)
	}
	if got := reportProvider.callCount(); got != 0 {
		t.Fatalf("report provider called %d times, want 0", got)
	}
}

func TestFailureBeforeMaterializationIsTerminalWithoutConnecting(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	provider := &failingNoiseProvider{}
	failure := "provisioning failed"

	failed, err := manager.ReportProvisioningStatus("tools", nil, &failure, provider)
	if err != nil || failed == nil {
		t.Fatalf("ReportProvisioningStatus(failure) = %#v, %v", failed, err)
	}
	materialized, err := manager.MaterializePendingNoiseEnvironment("tools", &failingNoiseProvider{})
	if err != nil {
		t.Fatalf("MaterializePendingNoiseEnvironment() error = %v", err)
	}
	if failed.Provisioning != materialized.Provisioning {
		t.Fatal("materialization should return the same failed record")
	}
	status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "tools"})
	if err != nil || status.Status != EnvironmentStatusDisconnected || status.Error == nil || !strings.HasSuffix(*status.Error, "provisioning failed") {
		t.Fatalf("Status(failed) = %+v, %v", status, err)
	}
	if got := provider.callCount(); got != 0 {
		t.Fatalf("provider called %d times, want 0", got)
	}
}

func TestFailureReleasesTheExistingPendingEnvironmentWithoutConnecting(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	provider := &failingNoiseProvider{}
	pending, err := manager.MaterializePendingNoiseEnvironment("tools", provider)
	if err != nil {
		t.Fatalf("MaterializePendingNoiseEnvironment() error = %v", err)
	}

	failure := "provisioning failed"
	reported, err := manager.ReportProvisioningStatus("tools", nil, &failure, provider)
	if err != nil || reported == nil {
		t.Fatalf("ReportProvisioningStatus(failure) = %#v, %v", reported, err)
	}
	if pending.Provisioning != reported.Provisioning {
		t.Fatal("failure report should apply to the pending environment")
	}
	status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "tools"})
	if err != nil || status.Status != EnvironmentStatusDisconnected || status.Error == nil || !strings.HasSuffix(*status.Error, "provisioning failed") {
		t.Fatalf("Status(failed) = %+v, %v", status, err)
	}
	if got := provider.callCount(); got != 0 {
		t.Fatalf("provider called %d times, want 0", got)
	}
}

func TestRepeatedFailurePreservesTheFirstErrorAndRejectsReady(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	provider := &failingNoiseProvider{}
	first := "first failure"
	failed, err := manager.ReportProvisioningStatus("tools", nil, &first, provider)
	if err != nil || failed == nil {
		t.Fatalf("ReportProvisioningStatus(first failure) = %#v, %v", failed, err)
	}

	different := "different failure"
	repeated, err := manager.ReportProvisioningStatus("tools", nil, &different, provider)
	if err != nil || repeated == nil {
		t.Fatalf("ReportProvisioningStatus(repeated failure) = %#v, %v", repeated, err)
	}
	if _, _, failure := failed.Provisioning.Current(); failure != "first failure" {
		t.Fatalf("failure = %q, want first failure preserved", failure)
	}

	invalidReady := provisionedReadyInfo("selected-root", "other")
	if _, err := manager.ReportProvisioningStatus("tools", &invalidReady, nil, provider); err == nil || !strings.Contains(err.Error(), "first failure") {
		t.Fatalf("ReportProvisioningStatus(ready after failure) error = %v, want first failure", err)
	}
	validReady := provisionedReadyInfo("selected-root", "tools")
	if _, err := manager.ReportProvisioningStatus("tools", &validReady, nil, provider); err == nil || !strings.Contains(err.Error(), "first failure") {
		t.Fatalf("ReportProvisioningStatus(valid ready after failure) error = %v, want first failure", err)
	}
	if got := manager.SelectedCapabilityRoots("tools"); len(got) != 0 {
		t.Fatalf("SelectedCapabilityRoots = %+v, want empty", got)
	}
	status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "tools"})
	if err != nil || status.Status != EnvironmentStatusDisconnected || status.Error == nil || !strings.HasSuffix(*status.Error, "first failure") {
		t.Fatalf("Status = %+v, %v", status, err)
	}
	if got := provider.callCount(); got != 0 {
		t.Fatalf("provider called %d times, want 0", got)
	}
}

func TestReadyEnvironmentRejectsALaterFailure(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	provider := &failingNoiseProvider{}
	readyInfo := provisionedReadyInfo("selected-root", "tools")
	ready, err := manager.ReportProvisioningStatus("tools", &readyInfo, nil, provider)
	if err != nil || ready == nil {
		t.Fatalf("ReportProvisioningStatus(ready) = %#v, %v", ready, err)
	}

	late := "late failure"
	if _, err := manager.ReportProvisioningStatus("tools", nil, &late, provider); err == nil || !strings.Contains(err.Error(), "already ready") {
		t.Fatalf("ReportProvisioningStatus(late failure) error = %v, want already-ready", err)
	}
	if got := manager.SelectedCapabilityRoots("tools"); len(got) != 1 {
		t.Fatalf("SelectedCapabilityRoots = %d roots, want 1", len(got))
	}
	status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "tools"})
	if err != nil || status.Status != EnvironmentStatusDisconnected || status.Error == nil || !strings.Contains(*status.Error, "test Noise provider called") {
		t.Fatalf("Status = %+v, %v", status, err)
	}
	if got := provider.callCount(); got != 1 {
		t.Fatalf("provider called %d times, want 1", got)
	}
}

func TestExistingEnvironmentAcceptsMatchingReadiness(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	provider := &failingNoiseProvider{}
	selected := provisionedReadyInfo("selected-root", "tools")
	ready, err := manager.ReportProvisioningStatus("tools", &selected, nil, provider)
	if err != nil || ready == nil {
		t.Fatalf("ReportProvisioningStatus(ready) = %#v, %v", ready, err)
	}

	updated := provisionedReadyInfo("different-root", "tools")
	ready, err = manager.ReportProvisioningStatus("tools", &updated, nil, provider)
	if err != nil || ready == nil {
		t.Fatalf("ReportProvisioningStatus(updated ready) = %#v, %v", ready, err)
	}
	if got := manager.SelectedCapabilityRoots("tools"); len(got) != 1 || got[0].ID != "different-root" {
		t.Fatalf("SelectedCapabilityRoots = %+v, want different-root", got)
	}
}

func TestReportProvisioningStatusOnOrdinaryEnvironmentIsIgnored(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	provider := &failingNoiseProvider{}
	if err := manager.AddNoise("tools", provider); err != nil {
		t.Fatalf("AddNoise() error = %v", err)
	}
	readyInfo := provisionedReadyInfo("selected-root", "tools")
	record, err := manager.ReportProvisioningStatus("tools", &readyInfo, nil, provider)
	if err != nil || record != nil {
		t.Fatalf("ReportProvisioningStatus(ordinary) = %#v, %v, want nil record and no error", record, err)
	}
	if got := manager.SelectedCapabilityRoots("tools"); len(got) != 0 {
		t.Fatalf("SelectedCapabilityRoots(ordinary) = %+v, want empty", got)
	}
}

func TestInvalidReadyInfoDoesNotCreateAMissingEnvironment(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	invalid := provisionedReadyInfo("selected-root", "other")
	if _, err := manager.ReportProvisioningStatus("tools", &invalid, nil, &failingNoiseProvider{}); err == nil || !strings.Contains(err.Error(), "belong to environment") {
		t.Fatalf("ReportProvisioningStatus(invalid, missing) error = %v, want belong-to-environment", err)
	}
	if _, ok := manager.Record("tools"); ok {
		t.Fatal("invalid ready info must not create a missing environment")
	}
}

func TestReadyInfoValidationBounds(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	roots := make([]SelectedCapabilityRoot, maxSelectedCapabilityRoots+1)
	for i := range roots {
		roots[i] = provisionedRoot(fmt.Sprintf("root-%d", i), "tools")
	}
	tooMany := EnvironmentReadyInfo{SelectedCapabilityRoots: roots}
	if _, err := manager.ReportProvisioningStatus("tools", &tooMany, nil, &failingNoiseProvider{}); err == nil || !strings.Contains(err.Error(), "more than 256") {
		t.Fatalf("ReportProvisioningStatus(too many roots) error = %v, want more-than-256", err)
	}

	duplicate := EnvironmentReadyInfo{SelectedCapabilityRoots: []SelectedCapabilityRoot{
		provisionedRoot("dup", "tools"),
		provisionedRoot("dup", "tools"),
	}}
	if _, err := manager.ReportProvisioningStatus("tools", &duplicate, nil, &failingNoiseProvider{}); err == nil || !strings.Contains(err.Error(), "unique non-empty IDs") {
		t.Fatalf("ReportProvisioningStatus(duplicate roots) error = %v, want unique IDs", err)
	}
}

func TestMaterializePendingRejectsOrdinaryModeConflict(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if err := manager.AddNoise("tools", &failingNoiseProvider{}); err != nil {
		t.Fatalf("AddNoise() error = %v", err)
	}
	_, err := manager.MaterializePendingNoiseEnvironment("tools", &failingNoiseProvider{})
	if err == nil || !errors.Is(err, ErrEnvironmentProvisioningModeConflict) || !strings.Contains(err.Error(), "different provisioning mode") {
		t.Fatalf("MaterializePendingNoiseEnvironment(ordinary) error = %v, want provisioning mode conflict", err)
	}
}

func TestRegisterDeferredValidation(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if _, err := manager.RegisterDeferredNoiseEnvironment("", &failingNoiseProvider{}); !errors.Is(err, ErrInvalidEnvironmentRequest) {
		t.Fatalf("RegisterDeferredNoiseEnvironment(empty) error = %v, want ErrInvalidEnvironmentRequest", err)
	}
	if _, err := manager.RegisterDeferredNoiseEnvironment("tools", nil); !errors.Is(err, ErrInvalidEnvironmentRequest) {
		t.Fatalf("RegisterDeferredNoiseEnvironment(nil provider) error = %v, want ErrInvalidEnvironmentRequest", err)
	}
	if _, err := manager.MaterializePendingNoiseEnvironment("", &failingNoiseProvider{}); !errors.Is(err, ErrInvalidEnvironmentRequest) {
		t.Fatalf("MaterializePendingNoiseEnvironment(empty) error = %v, want ErrInvalidEnvironmentRequest", err)
	}
	if _, err := manager.ReportProvisioningStatus("", nil, nil, &failingNoiseProvider{}); !errors.Is(err, ErrInvalidEnvironmentRequest) {
		t.Fatalf("ReportProvisioningStatus(empty) error = %v, want ErrInvalidEnvironmentRequest", err)
	}
	if _, err := manager.ReportProvisioningStatus("tools", nil, nil, nil); !errors.Is(err, ErrInvalidEnvironmentRequest) {
		t.Fatalf("ReportProvisioningStatus(nil provider) error = %v, want ErrInvalidEnvironmentRequest", err)
	}
}

func TestInfoContextDefersProvisionedEnvironment(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	provider := &failingNoiseProvider{}
	registration, err := manager.RegisterDeferredNoiseEnvironment("tools", provider)
	if err != nil {
		t.Fatalf("RegisterDeferredNoiseEnvironment() error = %v", err)
	}
	if _, err := manager.InfoContext(context.Background(), &EnvironmentInfoParams{EnvironmentID: "tools"}); err == nil || !strings.Contains(err.Error(), "still provisioning") {
		t.Fatalf("InfoContext(pending) error = %v, want still-provisioning", err)
	}

	failure := "provisioning failed"
	if err := registration.CompleteFailed(failure); err != nil {
		t.Fatalf("CompleteFailed() error = %v", err)
	}
	if _, err := manager.InfoContext(context.Background(), &EnvironmentInfoParams{EnvironmentID: "tools"}); err == nil || !strings.Contains(err.Error(), "provisioning failed") {
		t.Fatalf("InfoContext(failed) error = %v, want provisioning-failed", err)
	}
	if got := provider.callCount(); got != 0 {
		t.Fatalf("provider called %d times, want 0", got)
	}
}

func TestDoubleCompletionIsInactive(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	registration, err := manager.RegisterDeferredNoiseEnvironment("tools", &failingNoiseProvider{})
	if err != nil {
		t.Fatalf("RegisterDeferredNoiseEnvironment() error = %v", err)
	}
	if err := registration.CompleteReady(provisionedReadyInfo("selected-root", "tools")); err != nil {
		t.Fatalf("CompleteReady() error = %v", err)
	}
	if err := registration.CompleteReady(provisionedReadyInfo("other-root", "tools")); err == nil || !strings.Contains(err.Error(), "inactive") {
		t.Fatalf("CompleteReady(second) error = %v, want inactive", err)
	}
	if got := manager.SelectedCapabilityRoots("tools"); len(got) != 1 || got[0].ID != "selected-root" {
		t.Fatalf("SelectedCapabilityRoots = %+v, want first completion to win", got)
	}
}

