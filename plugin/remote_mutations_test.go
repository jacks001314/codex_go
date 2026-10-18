package plugin

import (
	"testing"
	"time"
)

func testMutationPlugin() PluginDetail {
	return PluginDetail{Summary: PluginSummary{
		ID:              "sample@debug",
		Name:            "sample",
		MarketplaceName: "debug",
		RemotePluginID:  "remote-sample",
	}}
}

// Mirrors Rust #42114: the shared manager gate covers the install mutation and
// the outcome carries the staged detail for downstream setup.
func TestInstallWithOutcomeHoldsMutationGateLikeRust(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	service.AddPlugin(testMutationPlugin())

	release := AcquireRemoteInstalledPluginCacheMutation(home)
	done := make(chan *PluginInstallOutcome, 1)
	errs := make(chan error, 1)
	go func() {
		outcome, err := service.InstallWithOutcome(&PluginInstallParams{PluginID: "sample@debug"})
		done <- outcome
		errs <- err
	}()
	select {
	case <-done:
		release()
		t.Fatal("InstallWithOutcome bypassed the held mutation gate")
	case <-time.After(25 * time.Millisecond):
	}
	release()
	outcome := <-done
	if err := <-errs; err != nil {
		t.Fatalf("InstallWithOutcome() error = %v", err)
	}
	if outcome == nil || outcome.Response == nil || outcome.Detail == nil {
		t.Fatalf("install outcome = %#v, want response and detail", outcome)
	}
	if !outcome.EffectivePluginsChanged {
		t.Fatal("install outcome did not report effective plugin changes")
	}
	if !outcome.Detail.Summary.Installed || !outcome.Detail.Summary.Enabled {
		t.Fatalf("install outcome detail = %#v, want installed and enabled", outcome.Detail.Summary)
	}
}

// Mirrors Rust #42114 uninstall-gate coverage: the gate spans the cache/config
// mutation and the outcome retains the pre-uninstall detail for analytics.
func TestUninstallWithOutcomeHoldsMutationGateAndRetainsDetailLikeRust(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	service.AddPlugin(testMutationPlugin())
	if _, err := service.Install(&PluginInstallParams{PluginID: "sample@debug"}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}

	release := AcquireRemoteInstalledPluginCacheMutation(home)
	done := make(chan *PluginUninstallOutcome, 1)
	errs := make(chan error, 1)
	go func() {
		outcome, err := service.UninstallWithOutcome(&PluginUninstallParams{PluginID: "sample@debug"})
		done <- outcome
		errs <- err
	}()
	select {
	case <-done:
		release()
		t.Fatal("UninstallWithOutcome bypassed the held mutation gate")
	case <-time.After(25 * time.Millisecond):
	}
	release()
	outcome := <-done
	if err := <-errs; err != nil {
		t.Fatalf("UninstallWithOutcome() error = %v", err)
	}
	if outcome == nil || outcome.Detail == nil {
		t.Fatalf("uninstall outcome = %#v, want pre-uninstall detail", outcome)
	}
	if !outcome.EffectivePluginsChanged {
		t.Fatal("uninstall outcome did not report effective plugin changes")
	}
}
