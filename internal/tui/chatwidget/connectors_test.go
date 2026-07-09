package chatwidget

import (
	"strings"
	"testing"

	appsapi "codex_go/internal/apps"
)

func TestConnectorsBeginRefreshMatchesRustInFlightForceRefetch(t *testing.T) {
	state := ConnectorsState{}
	if !state.BeginRefresh(true, false) {
		t.Fatal("first refresh should start")
	}
	if state.Cache.Kind != ConnectorsCacheLoading || !state.PrefetchInFlight {
		t.Fatalf("state after first refresh = %#v", state)
	}
	if state.BeginRefresh(true, true) {
		t.Fatal("second refresh while in-flight should not start")
	}
	if !state.ForceRefetchPending {
		t.Fatalf("force refetch should be remembered while in-flight: %#v", state)
	}
	if (&ConnectorsState{}).BeginRefresh(false, true) {
		t.Fatal("disabled connectors should not refresh")
	}
}

func TestConnectorsAddOutputUsesCacheAndQueuesFetchLikeRust(t *testing.T) {
	state := ConnectorsState{}
	disabled := state.AddOutput(false)
	if disabled.Kind != ConnectorsOutputDisabled || !strings.Contains(disabled.InfoMessage, "disabled") {
		t.Fatalf("disabled output = %#v", disabled)
	}

	loading := state.AddOutput(true)
	if loading.Kind != ConnectorsOutputLoading || loading.View == nil || loading.View.ViewID != AppsSelectionViewID || !loading.FetchQueued {
		t.Fatalf("loading output = %#v state=%#v", loading, state)
	}

	state = ConnectorsState{Cache: NewConnectorsCacheReady(ConnectorsSnapshot{Connectors: []appsapi.AppEntry{connectorApp("drive", "Drive", true, true)}})}
	popup := state.AddOutput(true)
	if popup.Kind != ConnectorsOutputPopup || popup.View == nil || popup.View.Items[0].ID != "drive" || !popup.ForceRefetch {
		t.Fatalf("popup output = %#v", popup)
	}

	state = ConnectorsState{Cache: NewConnectorsCacheReady(ConnectorsSnapshot{})}
	empty := state.AddOutput(true)
	if empty.Kind != ConnectorsOutputEmpty || empty.InfoMessage != "No apps available." {
		t.Fatalf("empty output = %#v", empty)
	}
}

func TestConnectorsLoadedPartialFinalPreservesEnabledAndSelection(t *testing.T) {
	state := ConnectorsState{Cache: NewConnectorsCacheReady(ConnectorsSnapshot{Connectors: []appsapi.AppEntry{
		connectorApp("drive", "Drive", true, false),
		connectorApp("calendar", "Calendar", true, true),
	}})}
	state.PrefetchInFlight = true
	state.ForceRefetchPending = true

	partial := state.OnLoaded(ConnectorsLoadResult{
		Snapshot: ConnectorsSnapshot{Connectors: []appsapi.AppEntry{connectorApp("partial", "Partial", true, true)}},
	}, false, 0)
	if state.PartialSnapshot == nil || partial.BottomPaneSnapshot == nil || partial.View != nil {
		t.Fatalf("partial outcome=%#v state=%#v", partial, state)
	}
	mentions, ok := state.ConnectorsForMentions(true)
	if !ok || len(mentions) != 1 || mentions[0].ID != "partial" {
		t.Fatalf("mentions = %#v ok=%v", mentions, ok)
	}

	final := state.OnLoaded(ConnectorsLoadResult{
		Snapshot: ConnectorsSnapshot{Connectors: []appsapi.AppEntry{
			connectorApp("drive", "Drive", true, true),
			connectorApp("calendar", "Calendar", true, true),
		}},
	}, true, 1)
	if !final.TriggerPendingForceRefetch || state.PrefetchInFlight || state.ForceRefetchPending {
		t.Fatalf("final refresh flags outcome=%#v state=%#v", final, state)
	}
	if state.PartialSnapshot != nil || state.Cache.Kind != ConnectorsCacheReady {
		t.Fatalf("final cache state = %#v", state)
	}
	if state.Cache.Snapshot.Connectors[0].IsEnabled {
		t.Fatalf("existing disabled state for drive should be preserved: %#v", state.Cache.Snapshot.Connectors[0])
	}
	if final.View == nil || final.View.InitialSelectedIndex != 1 {
		t.Fatalf("final view selection = %#v", final.View)
	}
}

func TestConnectorsLoadedFailureFallsBackLikeRust(t *testing.T) {
	state := ConnectorsState{PartialSnapshot: &ConnectorsSnapshot{Connectors: []appsapi.AppEntry{connectorApp("partial", "Partial", true, true)}}}
	outcome := state.OnLoaded(ConnectorsLoadResult{Error: "network down"}, true, 0)
	if !outcome.Failed || state.Cache.Kind != ConnectorsCacheReady || state.Cache.Snapshot.Connectors[0].ID != "partial" {
		t.Fatalf("partial fallback outcome=%#v state=%#v", outcome, state)
	}

	state = ConnectorsState{Cache: NewConnectorsCacheReady(ConnectorsSnapshot{Connectors: []appsapi.AppEntry{connectorApp("cached", "Cached", true, true)}})}
	outcome = state.OnLoaded(ConnectorsLoadResult{Error: "network down"}, true, 0)
	if !outcome.Failed || state.Cache.Kind != ConnectorsCacheReady || outcome.BottomPaneSnapshot == nil || outcome.BottomPaneSnapshot.Connectors[0].ID != "cached" {
		t.Fatalf("ready fallback outcome=%#v state=%#v", outcome, state)
	}

	state = ConnectorsState{}
	outcome = state.OnLoaded(ConnectorsLoadResult{Error: "network down"}, true, 0)
	if !outcome.Failed || state.Cache.Kind != ConnectorsCacheFailed || state.Cache.Error != "network down" || outcome.BottomPaneSnapshot != nil {
		t.Fatalf("failed fallback outcome=%#v state=%#v", outcome, state)
	}
}

func TestConnectorsUpdateEnabledAndCatalogTextMatchRust(t *testing.T) {
	desc := "Search files"
	state := ConnectorsState{Cache: NewConnectorsCacheReady(ConnectorsSnapshot{Connectors: []appsapi.AppEntry{{
		ID:           "drive",
		Name:         "Drive",
		Description:  &desc,
		IsAccessible: true,
		IsEnabled:    true,
	}}})}

	if !state.UpdateConnectorEnabled("drive", false) {
		t.Fatal("expected enabled state to change")
	}
	if state.Cache.Snapshot.Connectors[0].IsEnabled {
		t.Fatalf("connector should be disabled: %#v", state.Cache.Snapshot.Connectors[0])
	}
	if state.LastView == nil || state.LastView.Items[0].Description != "Installed \u2022 Disabled \u2022 Search files" {
		t.Fatalf("last view = %#v", state.LastView)
	}
	if state.UpdateConnectorEnabled("drive", false) {
		t.Fatal("unchanged enabled state should be ignored")
	}
}

func TestConnectorsExactStringStateMatchesRust(t *testing.T) {
	state := ConnectorsState{Cache: NewConnectorsCacheFailed("  failed to load  ")}
	failed := state.AddOutput(true)
	if failed.Kind != ConnectorsOutputFailed || failed.ErrorMessage != "  failed to load  " {
		t.Fatalf("failed output should preserve error text: %#v", failed)
	}

	state = ConnectorsState{}
	outcome := state.OnLoaded(ConnectorsLoadResult{Error: "   "}, true, 0)
	if !outcome.Failed || state.Cache.Kind != ConnectorsCacheFailed || state.Cache.Error != "   " {
		t.Fatalf("whitespace error should still be an error: outcome=%#v state=%#v", outcome, state)
	}

	state = ConnectorsState{Cache: NewConnectorsCacheReady(ConnectorsSnapshot{Connectors: []appsapi.AppEntry{
		connectorApp(" drive ", "Drive", true, true),
		connectorApp("drive", "Drive Plain", true, true),
	}})}
	if !state.UpdateConnectorEnabled("drive", false) || !state.Cache.Snapshot.Connectors[0].IsEnabled || state.Cache.Snapshot.Connectors[1].IsEnabled {
		t.Fatalf("exact id update should not touch spaced id: %#v", state.Cache.Snapshot.Connectors)
	}
	if !state.UpdateConnectorEnabled(" drive ", false) || state.Cache.Snapshot.Connectors[0].IsEnabled {
		t.Fatalf("spaced id should update only on exact match: %#v", state.Cache.Snapshot.Connectors)
	}

	final := state.OnLoaded(ConnectorsLoadResult{Snapshot: ConnectorsSnapshot{Connectors: []appsapi.AppEntry{
		connectorApp(" drive ", "Drive", true, true),
		connectorApp("drive", "Drive Plain", true, true),
	}}}, true, 0)
	if state.Cache.Snapshot.Connectors[0].IsEnabled {
		t.Fatalf("enabled state should be preserved by exact id: %#v", state.Cache.Snapshot.Connectors)
	}
	if final.View == nil || final.View.InitialSelectedIndex != 0 {
		t.Fatalf("selection should preserve exact spaced id: %#v", final.View)
	}
}

func connectorApp(id string, name string, accessible bool, enabled bool) appsapi.AppEntry {
	return appsapi.AppEntry{
		ID:           id,
		Name:         name,
		IsAccessible: accessible,
		IsEnabled:    enabled,
	}
}
