package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/session"
	agentsoverview "codex_go/tui/agents_overview"
	codextea "codex_go/tui/tea"
	"codex_go/utils"
)

// TestAgentsOverviewRowsMapSourceForSharedPinningLikeRust covers Rust #51500:
// the agent command center decides whether a task may be pinned in the shared
// section from its thread source, so the host must carry that source into the
// rows. Rust counterparts: agents_overview_discovery_tests.rs
// (supports_shared_pinning / list_pinned_threads) and
// agents_overview_threads.rs.
func TestAgentsOverviewRowsMapSourceForSharedPinningLikeRust(t *testing.T) {
	idle := appserver.IdleStatus()
	threads := []*appserver.Thread{
		{ID: "root-cli", Status: idle, Source: appserver.SessionSourceCli},
		{ID: "root-vscode", Status: idle, Source: appserver.SessionSourceVsCode},
		{ID: "root-exec", Status: idle, Source: appserver.SessionSourceExec},
		{ID: "root-appserver", Status: idle, Source: appserver.SessionSourceAppServer},
		{ID: "root-unknown", Status: idle, Source: appserver.SessionSourceUnknown},
		// The app server canonicalises legacy spellings with
		// SessionSourceFromString, which the host reuses.
		{ID: "root-legacy", Status: idle, Source: appserver.SessionSource("app_server")},
		{ID: "root-custom", Status: idle, Source: appserver.SessionSource("atlas")},
		{ID: "root-subagent", Status: idle, Source: appserver.SessionSource("subagent:thread_spawn")},
	}
	rows := agentsOverviewRowsFromThreads(threads, "")
	byID := make(map[string]agentsoverview.Row, len(rows))
	for _, row := range rows {
		byID[row.ThreadID] = row
	}
	wantPinnable := map[string]bool{
		"root-cli":       true,
		"root-vscode":    true,
		"root-exec":      true,
		"root-appserver": true,
		"root-unknown":   false,
		"root-legacy":    true,
		"root-custom":    true,
		"root-subagent":  false,
	}
	for threadID, want := range wantPinnable {
		row, ok := byID[threadID]
		if !ok {
			t.Fatalf("missing row for %s", threadID)
		}
		if got := agentsoverview.SupportsSharedPinning(row.Source); got != want {
			t.Errorf("SupportsSharedPinning(%s -> %q) = %v, want %v", threadID, row.Source, got, want)
		}
	}
	if got := byID["root-cli"].Source; got != "cli" {
		t.Errorf("cli row source = %q, want cli", got)
	}
	if got := byID["root-appserver"].Source; got != "appServer" {
		t.Errorf("app server row source = %q, want appServer", got)
	}
	if got := byID["root-legacy"].Source; got != "appServer" {
		t.Errorf("legacy app_server row source = %q, want appServer", got)
	}
	if got := byID["root-unknown"].Source; got != "unknown" {
		t.Errorf("unknown row source = %q, want unknown", got)
	}

	// The local session store carries the persisted thread source
	// (session.Metadata.Source), the same field the app server maps with
	// SessionSourceFromString, so the no-daemon fallback reports a real source
	// instead of an empty one.
	records := []session.Record{
		{ID: "r-cli", Metadata: session.Metadata{Source: "cli"}},
		{ID: "r-appserver", Metadata: session.Metadata{Source: "app_server"}},
		{ID: "r-empty"},
	}
	recordRows := agentsOverviewRowsFromRecords(records, "")
	recordByID := make(map[string]agentsoverview.Row, len(recordRows))
	for _, row := range recordRows {
		recordByID[row.ThreadID] = row
	}
	if got := recordByID["r-cli"].Source; got != "cli" || !agentsoverview.SupportsSharedPinning(got) {
		t.Errorf("local cli record source = %q, want pinnable cli", got)
	}
	if got := recordByID["r-appserver"].Source; got != "appServer" {
		t.Errorf("local app_server record source = %q, want appServer", got)
	}
	if got := recordByID["r-empty"].Source; agentsoverview.SupportsSharedPinning(got) {
		t.Errorf("record without a source is pinnable (%q), want not pinnable", got)
	}
}

// agentsOverviewPinTestServerLikeRust is an in-process app server: the real
// router behind a real websocket, the transport the remote TUI talks to.
type agentsOverviewPinTestServerLikeRust struct {
	store    *session.Store
	endpoint *appserverdaemon.RemoteAppServerEndpoint
	// sectionQueries counts the shared-section queries the server rejected.
	sectionQueries atomic.Int32
}

// agentsOverviewPinTestAppServerLikeRust serves the real app-server router over
// a real websocket. sectionsSupported=false answers a shared-section thread/list
// with the JSON-RPC code Rust's list_pinned_threads reads as "this server has no
// shared sections" (-32602..-32600), like a server that predates them.
func agentsOverviewPinTestAppServerLikeRust(t *testing.T, sectionsSupported bool) *agentsOverviewPinTestServerLikeRust {
	t.Helper()
	store := session.NewStore(t.TempDir())
	router := appserver.NewRouter(store)
	server := &agentsOverviewPinTestServerLikeRust{store: store}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(r.Context(), conn)
			if err != nil {
				return
			}
			envelope := map[string]any{"jsonrpc": "2.0", "id": req.ID}
			switch {
			case req.Method == string(appserver.MethodInitialize):
				envelope["result"] = map[string]any{}
			case !sectionsSupported && agentsOverviewPinTestSectionQueryLikeRust(req.Params):
				// An older server that does not know the sectionId parameter
				// rejects the shared-section query (Rust's -32602..-32600 range).
				server.sectionQueries.Add(1)
				envelope["error"] = map[string]any{"code": -32602, "message": "unknown parameter: sectionId"}
			default:
				response := router.Handle(&appserver.Request{
					JSONRPC: "2.0",
					ID:      agentsOverviewPinTestRequestIDLikeRust(req.ID),
					Method:  appserver.Method(req.Method),
					Params:  req.Params,
				})
				if response.Error != nil {
					envelope["error"] = map[string]any{"code": response.Error.Code, "message": response.Error.Message}
				} else {
					envelope["result"] = response.Result
				}
			}
			remoteTUITestWrite(r.Context(), conn, envelope)
		}
	}))
	t.Cleanup(func() {
		httpServer.Close()
		_ = router.Close()
	})
	server.endpoint = appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	return server
}

// agentsOverviewPinTestSectionQueryLikeRust reports whether a thread/list
// request asks for a shared section.
func agentsOverviewPinTestSectionQueryLikeRust(params json.RawMessage) bool {
	if len(params) == 0 {
		return false
	}
	var fields map[string]any
	if err := json.Unmarshal(params, &fields); err != nil {
		return false
	}
	_, ok := fields["sectionId"]
	return ok
}

// agentsOverviewPinTestRequestIDLikeRust rebuilds the request id the client
// sent so the router accepts the request and the response can address it.
func agentsOverviewPinTestRequestIDLikeRust(id any) appserver.RequestID {
	switch value := id.(type) {
	case string:
		return appserver.StringID(value)
	case float64:
		return appserver.IntID(int64(value))
	default:
		return appserver.IntID(1)
	}
}

// agentsOverviewPinTestRowsLikeRust builds dashboard rows from the app server
// the way the remote dashboard source does: a thread/list page handed to
// agentsOverviewRowsFromThreads.
func agentsOverviewPinTestRowsLikeRust(t *testing.T, request agentsOverviewPinRequestFunc) ([]agentsoverview.Row, error) {
	t.Helper()
	limit := 100
	params := appserver.ThreadListParams{
		Limit:          &limit,
		ModelProviders: []string{},
		UseStateDBOnly: true,
	}
	var response appserver.ThreadListResponse
	if code, err := request(string(appserver.MethodThreadList), params, &response); err != nil {
		t.Fatalf("thread/list (code %d) failed: %v", code, err)
	}
	threads := make([]*appserver.Thread, 0, len(response.Data))
	for i := range response.Data {
		threads = append(threads, &response.Data[i])
	}
	return agentsOverviewRowsFromThreads(threads, ""), nil
}

func agentsOverviewPinCreateThreadLikeRust(t *testing.T, store *session.Store, threadID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		Title:     "alpha",
		Preview:   "fix parser",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           t.TempDir(),
			ModelProvider: "openai",
			Source:        string(appserver.SessionSourceCli),
		},
		Items: []session.Item{{ID: "item-1", Type: "message", Role: "user", Text: "hello", CreatedAt: now}},
	}); err != nil {
		t.Fatalf("create record %s: %v", threadID, err)
	}
}

func agentsOverviewPinKeyLikeRust(r rune) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{r}}
}

func agentsOverviewPinUpdateLikeRust(t *testing.T, model *codextea.Model, message bubbletea.Msg) (*codextea.Model, bubbletea.Cmd) {
	t.Helper()
	updated, command := model.Update(message)
	next, ok := updated.(*codextea.Model)
	if !ok {
		t.Fatalf("Update returned %T, want *codextea.Model", updated)
	}
	return next, command
}

// agentsOverviewPinRunCmdLikeRust drains a command chain the way the tea
// runtime does, with a bounded depth so a self-refreshing dashboard cannot loop
// forever inside a test.
func agentsOverviewPinRunCmdLikeRust(t *testing.T, model *codextea.Model, command bubbletea.Cmd) *codextea.Model {
	t.Helper()
	for depth := 0; command != nil && depth < 8; depth++ {
		message := command()
		if message == nil {
			return model
		}
		if batch, ok := message.(bubbletea.BatchMsg); ok {
			for _, sub := range batch {
				model = agentsOverviewPinRunCmdLikeRust(t, model, sub)
			}
			return model
		}
		model, command = agentsOverviewPinUpdateLikeRust(t, model, message)
	}
	return model
}

// agentsOverviewPinOpenDashboardLikeRust opens the in-session command center the
// way a user does: type `/agents` and press enter. tui/tea's openAgentsDashboard
// helper is package-private, so the same steps are repeated here.
func agentsOverviewPinOpenDashboardLikeRust(t *testing.T, model *codextea.Model) *codextea.Model {
	t.Helper()
	for _, r := range "/agents" {
		model, _ = agentsOverviewPinUpdateLikeRust(t, model, agentsOverviewPinKeyLikeRust(r))
	}
	model, command := agentsOverviewPinUpdateLikeRust(t, model, bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if command == nil {
		t.Fatal("/agents returned no refresh command")
	}
	return agentsOverviewPinRunCmdLikeRust(t, model, command)
}

// TestAgentsOverviewPinRequestDrivesRealDashboardLikeRust covers Rust #51500 end
// to end for the app layer: rows carry the thread source, the `p` shortcut in
// the command center reaches a real app server (websocket + real router),
// thread/section/move puts the task in the shared pinned section, the shared
// listing reflects it, and the dashboard renders the Pinned group. Rust
// counterparts: pin_action_updates_shared_section_state
// (agents_overview_actions_tests.rs) and list_pinned_threads
// (agents_overview_discovery_tests.rs).
func TestAgentsOverviewPinRequestDrivesRealDashboardLikeRust(t *testing.T) {
	server := agentsOverviewPinTestAppServerLikeRust(t, true)
	agentsOverviewPinCreateThreadLikeRust(t, server.store, "root-1")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The production app-layer wiring: one short-lived client per call.
	request := interactiveRemoteAgentsOverviewPinRequest(ctx, server.endpoint)
	pinnedThreads := interactiveRemoteAgentsOverviewPinnedThreads(request)
	togglePin := interactiveRemoteAgentsOverviewTogglePin(request)

	// The server supports shared sections, and the pinned section starts empty.
	pinned, supported, err := pinnedThreads()
	if err != nil || !supported || len(pinned) != 0 {
		t.Fatalf("initial pinned listing = %#v supported=%v err=%v", pinned, supported, err)
	}

	model := codextea.NewModel(nil, codextea.Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewPinTestRowsLikeRust(t, request)
		},
		OnAgentsOverviewPinnedThreads: pinnedThreads,
		OnAgentsOverviewTogglePin:     togglePin,
	})
	model = agentsOverviewPinOpenDashboardLikeRust(t, model)
	rendered := utils.StripANSI(model.View())
	if !strings.Contains(rendered, "Agent command center") {
		t.Fatalf("command center did not open:\n%s", rendered)
	}
	// The rows the real server served carried a pinnable source, so the
	// dashboard advertises the shortcut (Rust agent_center/hints.rs).
	if !strings.Contains(rendered, "pin/unpin") {
		t.Fatalf("command center did not advertise shared pinning:\n%s", rendered)
	}

	// `p` sends thread/section/move through the app adapter.
	model, pinCommand := agentsOverviewPinUpdateLikeRust(t, model, agentsOverviewPinKeyLikeRust('p'))
	if pinCommand == nil {
		t.Fatal("p returned no pin command")
	}
	model = agentsOverviewPinRunCmdLikeRust(t, model, pinCommand)
	record, err := server.store.Load(session.ThreadID("root-1"))
	if err != nil {
		t.Fatalf("load record: %v", err)
	}
	if record.Section == nil || record.Section.ID != session.PinnedThreadSectionID {
		t.Fatalf("thread section after pin = %#v, want %s", record.Section, session.PinnedThreadSectionID)
	}
	pinned, supported, err = pinnedThreads()
	if err != nil || !supported {
		t.Fatalf("pinned listing supported=%v err=%v, want supported", supported, err)
	}
	if len(pinned) != 1 || pinned[0] != "root-1" {
		t.Fatalf("pinned listing after pin = %#v, want [root-1]", pinned)
	}
	if rendered = utils.StripANSI(model.View()); !strings.Contains(rendered, "Pinned") {
		t.Fatalf("command center does not render the Pinned group:\n%s", rendered)
	}

	// A second `p` unpins the task again.
	model, unpinCommand := agentsOverviewPinUpdateLikeRust(t, model, agentsOverviewPinKeyLikeRust('p'))
	if unpinCommand == nil {
		t.Fatal("p returned no unpin command")
	}
	model = agentsOverviewPinRunCmdLikeRust(t, model, unpinCommand)
	record, err = server.store.Load(session.ThreadID("root-1"))
	if err != nil {
		t.Fatalf("load record after unpin: %v", err)
	}
	if record.Section != nil {
		t.Fatalf("thread section after unpin = %#v, want cleared", record.Section)
	}
	pinned, supported, err = pinnedThreads()
	if err != nil || !supported || len(pinned) != 0 {
		t.Fatalf("pinned listing after unpin = %#v supported=%v err=%v", pinned, supported, err)
	}
}

// TestAgentsOverviewPinUnsupportedServerDisablesPinningLikeRust covers Rust
// #51500: a server that rejects shared thread sections (-32602..-32600, as an
// older server would) disables pinning instead of failing, so the command
// center never advertises the shortcut. Rust counterpart: list_pinned_threads
// returning None for the unsupported error range.
func TestAgentsOverviewPinUnsupportedServerDisablesPinningLikeRust(t *testing.T) {
	server := agentsOverviewPinTestAppServerLikeRust(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request := interactiveRemoteAgentsOverviewPinRequest(ctx, server.endpoint)

	// Control: the same endpoint serves an ordinary listing, so the rejected
	// section query below is really about shared sections.
	limit := 100
	var control appserver.ThreadListResponse
	if code, err := request(string(appserver.MethodThreadList), appserver.ThreadListParams{Limit: &limit, UseStateDBOnly: true}, &control); err != nil {
		t.Fatalf("plain thread/list (code %d) failed: %v", code, err)
	}

	pinnedThreads := interactiveRemoteAgentsOverviewPinnedThreads(request)
	pinned, supported, err := pinnedThreads()
	if err != nil {
		t.Fatalf("unsupported server surfaced an error: %v", err)
	}
	if supported || len(pinned) != 0 {
		t.Fatalf("unsupported server reported supported=%v pinned=%#v, want unavailable", supported, pinned)
	}
	// The rejection really was the shared-section query (not some other error
	// swallowed by the unsupported-code range), and pinning stopped there.
	if got := server.sectionQueries.Load(); got != 1 {
		t.Fatalf("server rejected %d shared-section queries, want 1", got)
	}

	// The dashboard therefore never advertises the pin shortcut, even though the
	// task itself could be pinned.
	rows := agentsOverviewRowsFromThreads([]*appserver.Thread{
		{ID: "root-1", Status: appserver.IdleStatus(), Source: appserver.SessionSourceCli},
	}, "")
	model := codextea.NewModel(nil, codextea.Options{
		Width:                         120,
		Height:                        24,
		OnAgentsOverviewRefresh:       func(string) ([]agentsoverview.Row, error) { return rows, nil },
		OnAgentsOverviewPinnedThreads: pinnedThreads,
		OnAgentsOverviewTogglePin:     interactiveRemoteAgentsOverviewTogglePin(request),
	})
	model = agentsOverviewPinOpenDashboardLikeRust(t, model)
	if rendered := utils.StripANSI(model.View()); strings.Contains(rendered, "pin/unpin") {
		t.Fatalf("command center advertises pinning against an unsupported server:\n%s", rendered)
	}
}

// agentsOverviewPinCreateThreadAtLikeRust persists one root task with an explicit
// recency, so a test can place a task outside the dashboard's recent-task window.
func agentsOverviewPinCreateThreadAtLikeRust(t *testing.T, store *session.Store, threadID string, title string, preview string, recency time.Time) {
	t.Helper()
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		Title:     title,
		Preview:   preview,
		CreatedAt: recency,
		UpdatedAt: recency,
		RecencyAt: recency,
		Metadata: session.Metadata{
			CWD:           t.TempDir(),
			ModelProvider: "openai",
			Source:        string(appserver.SessionSourceCli),
		},
		Items: []session.Item{{ID: "item-1", Type: "message", Role: "user", Text: "hello", CreatedAt: recency}},
	}); err != nil {
		t.Fatalf("create record %s: %v", threadID, err)
	}
}

// TestPinnedTaskRendersInPinnedGroupThroughRealListingLikeRust covers Rust
// #51500's "Display pinned tasks first in a Pinned group" through the production
// app-layer refresh path: the command center lists the shared pinned section and
// renders a task pinned outside the recent-task window in the Pinned group ahead
// of the recent tasks. Rust counterparts: list_pinned_threads
// (agents_overview_discovery_tests.rs) and pin_action_updates_shared_section_state
// (agents_overview_actions_tests.rs). The row seeding the pinned section adds on
// top of the listing is covered directly by
// TestAgentsOverviewSeedPinnedThreadsLikeRust.
func TestPinnedTaskRendersInPinnedGroupThroughRealListingLikeRust(t *testing.T) {
	server := agentsOverviewPinTestAppServerLikeRust(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// More recent tasks than the dashboard's recent-task window holds, so the
	// pinned task below cannot be part of it.
	now := time.Now().UTC()
	for index := 0; index < recentSessionLimit+1; index++ {
		agentsOverviewPinCreateThreadAtLikeRust(t, server.store,
			fmt.Sprintf("recent-%02d", index),
			fmt.Sprintf("recent task %02d", index),
			fmt.Sprintf("recent preview %02d", index),
			now.Add(-time.Duration(index)*time.Minute))
	}
	agentsOverviewPinCreateThreadAtLikeRust(t, server.store, "pinned-old", "old pinned task", "older pinned preview", now.Add(-24*time.Hour))
	section := session.PinnedThreadSectionID
	if _, err := server.store.MoveThreadToSection(session.ThreadID("pinned-old"), &section, nil); err != nil {
		t.Fatalf("pin thread: %v", err)
	}

	client, err := openRemoteSessionClient(ctx, server.endpoint)
	if err != nil {
		t.Fatalf("open client: %v", err)
	}
	defer client.close()
	source := newRemoteAgentsDashboardSource(client, "")

	// Control: the pinned task really is outside the window the dashboard loads
	// on its own, so the assertions below cannot pass through the recent seed.
	window, err := source.listRecentThreads(ctx)
	if err != nil {
		t.Fatalf("recent listing: %v", err)
	}
	if len(window) == 0 {
		t.Fatal("recent listing returned no tasks to compare against")
	}
	for _, thread := range window {
		if appserverThreadID(thread) == "pinned-old" {
			t.Fatal("test setup: the pinned task is inside the recent-task window")
		}
	}

	rows, err := source.List(ctx)
	if err != nil {
		t.Fatalf("dashboard listing: %v", err)
	}
	byID := make(map[string]agentsoverview.Row, len(rows))
	for _, row := range rows {
		byID[row.ThreadID] = row
	}
	pinnedRow, ok := byID["pinned-old"]
	if !ok {
		t.Fatalf("a task pinned outside the recent window was not seeded into the dashboard rows")
	}
	if !agentsoverview.SupportsSharedPinning(pinnedRow.Source) {
		t.Fatalf("seeded pinned row source = %q, want a pinnable source", pinnedRow.Source)
	}

	// The real TUI path: the command center refreshes through the production
	// listing and renders the pinned task in the Pinned group ahead of the
	// recent tasks.
	request := agentsOverviewPinRequestOnClient(ctx, client)
	model := codextea.NewModel(nil, codextea.Options{
		Width:  120,
		Height: 30,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return source.List(ctx)
		},
		OnAgentsOverviewPinnedThreads: interactiveRemoteAgentsOverviewPinnedThreads(request),
		OnAgentsOverviewTogglePin:     interactiveRemoteAgentsOverviewTogglePin(request),
	})
	model = agentsOverviewPinOpenDashboardLikeRust(t, model)
	rendered := utils.StripANSI(model.View())
	pinnedIndex := strings.Index(rendered, "old pinned task")
	if pinnedIndex < 0 {
		t.Fatalf("the pinned task outside the recent window did not render:\n%s", rendered)
	}
	if recentIndex := strings.Index(rendered, "recent task 00"); recentIndex >= 0 && recentIndex < pinnedIndex {
		t.Fatalf("pinned task rendered after a recent task:\n%s", rendered)
	}
	if !strings.Contains(rendered, agentsoverview.PinnedGroupHeading) {
		t.Fatalf("command center did not render the Pinned group:\n%s", rendered)
	}
	if got := strings.Count(rendered, "old pinned task"); got != 1 {
		t.Fatalf("pinned task rendered %d times, want once:\n%s", got, rendered)
	}
}

// TestAgentsOverviewSeedPinnedThreadsLikeRust covers Rust #51500's row seeding
// (agents_overview_threads.rs: list_pinned_threads' threads are inserted into the
// row map, so a pinned task the recent/loaded listing does not carry still has a
// row to rank). Go's app server currently lists every persisted record from
// thread/loaded/list (appserver/router.go handleThreadLoadedList uses PageSize 0)
// and the dashboard caps that listing at maxThreads, so this seeding is what
// keeps a pinned task beyond the cap renderable. Rust counterpart:
// list_pinned_threads / agents_overview_threads row seeding.
func TestAgentsOverviewSeedPinnedThreadsLikeRust(t *testing.T) {
	listed := &appserver.Thread{ID: "recent-00"}
	// thread/loaded/list hands back ids without records, so the dashboard's order
	// can name a task the record map does not know yet.
	byID := map[string]*appserver.Thread{"recent-00": listed}
	order := []string{"recent-00", "loaded-01"}
	pinned := []appserver.Thread{
		{ID: "recent-00"},  // already a row: the listing's record is kept
		{ID: "loaded-01"},  // listed without a record: the pinned one fills it in
		{ID: "pinned-out"}, // outside the listing: seeded as a new row
		{ID: "   "},        // no task: ignored
	}
	got := agentsOverviewSeedPinnedThreads(order, byID, pinned)
	want := []string{"recent-00", "loaded-01", "pinned-out"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("seeded order = %#v, want %#v", got, want)
	}
	if byID["recent-00"] != listed {
		t.Fatal("seeding replaced the record the listing already had")
	}
	if byID["loaded-01"] == nil || byID["loaded-01"].ID != "loaded-01" {
		t.Fatalf("seeding did not fill in the listed task's record: %#v", byID["loaded-01"])
	}
	if byID["pinned-out"] == nil || byID["pinned-out"].ID != "pinned-out" {
		t.Fatalf("seeding did not insert the pinned task outside the listing: %#v", byID["pinned-out"])
	}
	// A refresh seeds the same pinned section again: no duplicate rows, and the
	// pin order keeps ranking one row per task.
	if again := agentsOverviewSeedPinnedThreads(got, byID, pinned); !reflect.DeepEqual(again, want) {
		t.Fatalf("second seeding = %#v, want %#v", again, want)
	}
}
