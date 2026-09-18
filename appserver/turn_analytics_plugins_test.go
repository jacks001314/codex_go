package appserver

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/plugin"
	"codex_go/session"
	"codex_go/turn"
)

// TestTurnAnalyticsPluginInventoryKeepsTheFirstObservation mirrors Rust's
// turn_event_preserves_first_received_plugin_inventory: the inventory observed
// when the turn was admitted survives a later plugin reconciliation (the
// post-compaction run-config rebuild), while a new turn sees the new set.
func TestTurnAnalyticsPluginInventoryKeepsTheFirstObservation(t *testing.T) {
	home := t.TempDir()
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		Name: "first", MarketplaceName: "local", Installed: true, Enabled: true,
	}})
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions"))),
		Config:       config.NewConfigService(home),
		Plugins:      plugins,
	})
	const threadID = "thread-inventory"
	const turnID = "turn-inventory"
	if err := router.threads.RegisterTurn(threadID, turnID, nil, 0, nil); err != nil {
		t.Fatalf("RegisterTurn error = %v", err)
	}
	first := router.turnAnalyticsPluginInventory(threadID, turnID)
	if first == nil || !reflect.DeepEqual(*first, []string{"first@local"}) {
		t.Fatalf("first inventory = %v", formatPluginIDs(first))
	}
	// A plugin reconciled mid-turn must not change this turn's observation.
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		Name: "later", MarketplaceName: "local", Installed: true, Enabled: true,
	}})
	second := router.turnAnalyticsPluginInventory(threadID, turnID)
	if !reflect.DeepEqual(derefPluginIDs(first), derefPluginIDs(second)) {
		t.Fatalf("later observation = %v, want the first inventory %v", formatPluginIDs(second), formatPluginIDs(first))
	}
	// A new turn observes the reconciled inventory.
	next := router.activePluginIDsForTurnAnalytics(threadID)
	if next == nil || !reflect.DeepEqual(*next, []string{"first@local", "later@local"}) {
		t.Fatalf("new-turn inventory = %v", formatPluginIDs(next))
	}
}

// TestRuntimeRouterTurnAnalyticsPluginInventoryLikeRust mirrors Rust #46323's
// app-server coverage: a completed turn reports the active plugin inventory
// (preferring the remote identity), an observed empty inventory is [], and a
// turn whose plugins are not observed reports null.
func TestRuntimeRouterTurnAnalyticsPluginInventoryLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		withPlugin *plugin.PluginSummary
		want       *[]string
	}{
		{
			name: "remote identity wins",
			withPlugin: &plugin.PluginSummary{
				Name: "sample", MarketplaceName: "local", Installed: true, Enabled: true,
				RemotePluginID: "plugins~Plugin_sample",
			},
			want: &[]string{"plugins~Plugin_sample"},
		},
		{
			name: "package key fallback",
			withPlugin: &plugin.PluginSummary{
				Name: "sample", MarketplaceName: "local", Installed: true, Enabled: true,
			},
			want: &[]string{"sample@local"},
		},
		{
			name: "no plugin service",
			want: nil,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
				t.Fatalf("write config error = %v", err)
			}
			store := session.NewStore(filepath.Join(home, "sessions"))
			sink := NewNotificationBuffer()
			analyticsSink := newRecordingTurnEventSink()
			agent := newRecordingRuntimeAgent("done")
			services := RuntimeServices{
				ThreadRouter: NewRouter(store),
				Config:       config.NewConfigService(home),
				Turns:        turn.NewTurnService(),
				Agent:        agent,
				ThreadStatus: NewThreadStatusManager(),
				Analytics:    analyticsSink,
				DefaultCWD:   t.TempDir(),
			}
			if testCase.withPlugin != nil {
				plugins := plugin.NewPluginService()
				plugins.AddPlugin(plugin.PluginDetail{Summary: *testCase.withPlugin})
				services.Plugins = plugins
			}
			router := NewRuntimeRouter(services)
			router.SetNotificationSink(sink)

			initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
				ClientInfo: ClientInfo{Name: "codex-tui", Version: "1.2.3"},
			})
			initialize.ConnectionID = "conn-plugin-inventory"
			if response := router.Handle(initialize); response.Error != nil {
				t.Fatalf("initialize error: %+v", response.Error)
			}
			threadStart := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
				CWD: t.TempDir(),
			}))
			if threadStart.Error != nil {
				t.Fatalf("thread start error: %+v", threadStart.Error)
			}
			threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
			turnStart := requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
				ThreadID: threadID,
				Prompt:   "plugin inventory",
			})
			turnStart.ConnectionID = "conn-plugin-inventory"
			response := router.Handle(turnStart)
			if response.Error != nil {
				t.Fatalf("turn start error: %+v", response.Error)
			}
			turnID := response.Result.(*turn.TurnStartResponse).Turn.ID
			waitForRuntimeAgentRequest(t, agent)
			waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

			event := waitForCodexTurnEvent(t, analyticsSink, turnID)
			if got := event.EventParams.ActivePluginIDsAtTurnStart; !samePluginIDPointer(got, testCase.want) {
				t.Fatalf("active_plugin_ids_at_turn_start = %v, want %v", formatPluginIDs(got), formatPluginIDs(testCase.want))
			}
		})
	}
}

// TestActivePluginTelemetryIDsSelectAndValidateBySource mirrors Rust's
// session::active_plugin_inventory_tests::active_plugin_ids_select_and_validate_by_source.
func TestActivePluginTelemetryIDsSelectAndValidateBySource(t *testing.T) {
	type hostIdentity struct {
		pluginID       string
		remotePluginID *string
	}
	remote := func(value string) *string { return &value }
	for _, testCase := range []struct {
		name     string
		host     []hostIdentity
		selected []string
		want     *[]string
	}{
		{
			name: "remote wins even when the package fallback is invalid",
			host: []hostIdentity{
				{pluginID: "box@local", remotePluginID: remote("plugins~Plugin_box")},
				{pluginID: "selected-root", remotePluginID: remote("plugin_box-1")},
			},
			want: &[]string{"plugin_box-1", "plugins~Plugin_box"},
		},
		{
			name:     "package fallback",
			host:     []hostIdentity{{pluginID: "my.tool@123"}},
			selected: []string{"executor-demo@1"},
			want:     &[]string{"executor-demo@1", "my.tool@123"},
		},
		{
			name: "blank present remote",
			host: []hostIdentity{{pluginID: "box@local", remotePluginID: remote("")}},
			want: nil,
		},
		{
			name: "remote is not trimmed",
			host: []hostIdentity{{pluginID: "box@local", remotePluginID: remote(" plugin_box ")}},
			want: nil,
		},
		{
			name: "package key is not a remote ID",
			host: []hostIdentity{{pluginID: "box@local", remotePluginID: remote("box@local")}},
			want: nil,
		},
		{
			name:     "remote-looking selected root is still a fallback",
			selected: []string{"plugins~Plugin_box"},
			want:     nil,
		},
		{
			name: "one invalid identity makes the inventory unknown",
			host: []hostIdentity{
				{pluginID: "box@local", remotePluginID: remote("plugin_box")},
				{pluginID: "bad..name@1"},
			},
			want: nil,
		},
		{
			name: "exact deduplication retains aliases and distinct remote objects",
			host: []hostIdentity{
				{pluginID: "box@local", remotePluginID: remote("plugins~Plugin_b")},
				{pluginID: "box@local"},
				{pluginID: "box@local", remotePluginID: remote("plugins~Plugin_a")},
				{pluginID: "box@other", remotePluginID: remote("plugins~Plugin_a")},
			},
			selected: []string{"box@local", "box@local"},
			want:     &[]string{"box@local", "plugins~Plugin_a", "plugins~Plugin_b"},
		},
		{
			name: "empty observation",
			want: &[]string{},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			identities := make([]analyticsPluginIdentity, 0, len(testCase.host)+len(testCase.selected))
			for _, identity := range testCase.host {
				identities = append(identities, analyticsPluginIdentity{
					pluginID:       identity.pluginID,
					remotePluginID: identity.remotePluginID,
				})
			}
			for _, pluginID := range testCase.selected {
				identities = append(identities, analyticsPluginIdentity{pluginID: pluginID})
			}
			got := activePluginTelemetryIDs(identities)
			if !samePluginIDPointer(got, testCase.want) {
				t.Fatalf("activePluginTelemetryIDs() = %v, want %v", formatPluginIDs(got), formatPluginIDs(testCase.want))
			}
		})
	}
}

// TestActivePluginTelemetryIDsEnforceBounds mirrors Rust's
// active_plugin_ids_enforce_bounds_after_selection_and_deduplication.
func TestActivePluginTelemetryIDsEnforceBounds(t *testing.T) {
	remote := func(value string) *string { return &value }
	for _, length := range []int{128, 129} {
		for _, isRemote := range []bool{false, true} {
			id := strings.Repeat("p", length-6) + "@local"
			pluginID := id
			var remoteID *string
			if isRemote {
				id = strings.Repeat("r", length)
				// A remote ID also wins over an overlong package key.
				pluginID = strings.Repeat("p", 128) + "@local"
				remoteID = remote(id)
			}
			identities := []analyticsPluginIdentity{{pluginID: pluginID, remotePluginID: remoteID}}
			var want *[]string
			if length == 128 {
				want = &[]string{id}
			}
			if got := activePluginTelemetryIDs(identities); !samePluginIDPointer(got, want) {
				t.Fatalf("length=%d remote=%v: got %v, want %v", length, isRemote, formatPluginIDs(got), formatPluginIDs(want))
			}
		}

		id := strings.Repeat("p", length-6) + "@local"
		var want *[]string
		if length == 128 {
			want = &[]string{id}
		}
		got := activePluginTelemetryIDs([]analyticsPluginIdentity{{pluginID: id}})
		if !samePluginIDPointer(got, want) {
			t.Fatalf("selected length=%d: got %v, want %v", length, formatPluginIDs(got), formatPluginIDs(want))
		}
	}

	for _, count := range []int{512, 513} {
		ids := make([]string, 0, count)
		for index := 0; index < count; index++ {
			ids = append(ids, fmt.Sprintf("plugin_%03d@local", index))
		}
		identities := make([]analyticsPluginIdentity, 0, count)
		for _, id := range ids[:count/2] {
			identities = append(identities, analyticsPluginIdentity{pluginID: id})
		}
		for _, id := range ids[count/2-1:] {
			identities = append(identities, analyticsPluginIdentity{pluginID: id})
		}
		var want *[]string
		if count == 512 {
			want = &ids
		}
		if got := activePluginTelemetryIDs(identities); !samePluginIDPointer(got, want) {
			t.Fatalf("%d distinct IDs across host and selected inputs: got %d ids, want %v", count, len(derefPluginIDs(got)), formatPluginIDs(want))
		}
	}
}

func samePluginIDPointer(got *[]string, want *[]string) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return reflect.DeepEqual(*got, *want)
}

func derefPluginIDs(ids *[]string) []string {
	if ids == nil {
		return nil
	}
	return *ids
}

func formatPluginIDs(ids *[]string) string {
	if ids == nil {
		return "null"
	}
	return fmt.Sprintf("%v", *ids)
}
