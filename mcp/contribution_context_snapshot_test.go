package mcp

import "testing"

func TestContributionContextDisabledPluginsSnapshot(t *testing.T) {
	base := NewMCPServerContributionContext("thread", nil)
	if _, explicit := base.DisabledPluginIDs(); explicit {
		t.Fatal("unspecified snapshot became explicit")
	}
	ids := []string{"a@m"}
	context := base.WithDisabledPluginIDs(ids)
	ids[0] = "changed"
	got, explicit := context.DisabledPluginIDs()
	if !explicit || len(got) != 1 || got[0] != "a@m" {
		t.Fatalf("snapshot=%v explicit=%v", got, explicit)
	}
	got[0] = "mutated"
	again, _ := context.DisabledPluginIDs()
	if again[0] != "a@m" {
		t.Fatal("getter exposed snapshot")
	}
	cleared := context.WithDisabledPluginIDs(nil)
	if got, explicit := cleared.DisabledPluginIDs(); !explicit || len(got) != 0 {
		t.Fatalf("clear=%v explicit=%v", got, explicit)
	}
}

func TestHostContributionContextPreservesIdentityAcrossOverrides(t *testing.T) {
	context := NewHostMCPServerContributionContext(" parent ", " desktop ", " exec ").WithSelectedEnvironments(NewSelectedEnvironments(nil)).WithDisabledPluginIDs([]string{"a@m"})
	if !context.HostOnly() || context.ThreadID() != "parent" || context.Originator() != "desktop" || context.SessionSource() != "exec" {
		t.Fatalf("host identity lost: %#v", context)
	}
	if context.SelectedEnvironments() == nil {
		t.Fatal("explicit empty selection lost")
	}
}
