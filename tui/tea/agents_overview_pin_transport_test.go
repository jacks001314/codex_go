package tea

import (
	"errors"
	"testing"

	"codex_go/appserver"
	"codex_go/session"
	agentsoverview "codex_go/tui/agents_overview"
)

// pinnedTransportCall records one request issued through an
// AgentsOverviewPinRequestFunc so the tests can assert the exact wire shape the
// Go transport sends (Rust #51500 list_pinned_threads / toggle pin).
type pinnedTransportCall struct {
	method string
	list   *appserver.ThreadListParams
	move   *appserver.ThreadSectionMoveParams
}

// TestListPinnedThreadsPagesSharedSectionLikeRust covers Rust #51500's
// `list_pinned_threads` (codex-rs/tui/src/app/agents_overview_discovery.rs):
// two source-kind passes (empty first, then exec + app-server), a
// section_position page size of 100 with sectionId = PINNED_THREAD_SECTION_ID,
// useStateDBOnly and archived = false, cursor paging within a pass, and the
// is_overview_thread + supports_shared_pinning filter. Rust counterpart:
// `check_discovery` (agents_overview_discovery_tests.rs) and `list_pinned_threads`.
func TestListPinnedThreadsPagesSharedSectionLikeRust(t *testing.T) {
	child := "parent-1"
	firstPage := []appserver.Thread{
		{ID: "pin-1", Source: appserver.SessionSourceCli},
		{ID: "drop-ephemeral", Source: appserver.SessionSourceCli, Ephemeral: true},
		{ID: "drop-child", Source: appserver.SessionSourceCli, ParentThreadID: &child},
		{ID: "drop-spawn", Source: appserver.SessionSource(appserver.ThreadSourceKindSubAgentThreadSpawn)},
		{ID: "drop-unknown", Source: appserver.SessionSourceUnknown},
		{ID: "pin-2", Source: appserver.SessionSourceAppServer},
	}
	secondPage := []appserver.Thread{
		{ID: "pin-3", Source: appserver.SessionSourceVsCode},
		{ID: "drop-spawn-2", Source: appserver.SessionSource("subAgentThreadSpawn")},
		// Not a source that can carry a shared pin (Rust supports_shared_pinning).
		{ID: "drop-unsupported", Source: appserver.SessionSource("my-threadspawn-source")},
	}

	var calls []pinnedTransportCall
	request := func(method string, params any, result any) (int, error) {
		call := pinnedTransportCall{method: method}
		switch method {
		case string(appserver.MethodThreadList):
			list := params.(appserver.ThreadListParams)
			call.list = &list
			calls = append(calls, call)
			page := result.(*appserver.ThreadListResponse)
			if len(list.SourceKinds) == 0 {
				// First pass: a single empty page, no further cursor.
				page.Data = nil
				page.NextCursor = nil
				return 0, nil
			}
			if list.Cursor == nil {
				next := "cursor-2"
				page.Data = firstPage
				page.NextCursor = &next
				return 0, nil
			}
			page.Data = secondPage
			page.NextCursor = nil
			return 0, nil
		default:
			t.Fatalf("unexpected method %q", method)
			return 0, nil
		}
	}

	threads, supported, err := ListPinnedThreads(request)
	if err != nil {
		t.Fatalf("ListPinnedThreads error = %v, want nil", err)
	}
	if !supported {
		t.Fatal("ListPinnedThreads supported = false, want true")
	}

	if len(calls) != 3 {
		t.Fatalf("request count = %d, want 3 (one empty pass + two cursor pages)", len(calls))
	}
	wantSourceKinds := [][]appserver.ThreadSourceKind{
		{},
		{appserver.ThreadSourceKindExec, appserver.ThreadSourceKindAppServer},
		{appserver.ThreadSourceKindExec, appserver.ThreadSourceKindAppServer},
	}
	for i, call := range calls {
		if call.method != string(appserver.MethodThreadList) {
			t.Fatalf("call %d method = %q, want %q", i, call.method, appserver.MethodThreadList)
		}
		if call.list == nil {
			t.Fatalf("call %d did not decode ThreadListParams", i)
		}
		params := *call.list
		if len(params.SourceKinds) != len(wantSourceKinds[i]) {
			t.Fatalf("call %d sourceKinds = %v, want %v", i, params.SourceKinds, wantSourceKinds[i])
		}
		for j := range wantSourceKinds[i] {
			if params.SourceKinds[j] != wantSourceKinds[i][j] {
				t.Fatalf("call %d sourceKinds = %v, want %v", i, params.SourceKinds, wantSourceKinds[i])
			}
		}
		if !params.SectionID.Set || params.SectionID.Value == nil || *params.SectionID.Value != session.PinnedThreadSectionID {
			t.Fatalf("call %d sectionId = %#v, want %q", i, params.SectionID, session.PinnedThreadSectionID)
		}
		if params.SortKey != appserver.SortSectionPosition {
			t.Fatalf("call %d sortKey = %q, want %q", i, params.SortKey, appserver.SortSectionPosition)
		}
		if !params.UseStateDBOnly {
			t.Fatalf("call %d useStateDBOnly = false, want true", i)
		}
		if params.Limit == nil || *params.Limit != pinnedThreadPageSize {
			t.Fatalf("call %d limit = %v, want %d", i, params.Limit, pinnedThreadPageSize)
		}
		if params.Archived == nil || *params.Archived {
			t.Fatalf("call %d archived = %v, want false", i, params.Archived)
		}
		if params.ModelProviders == nil || len(params.ModelProviders) != 0 {
			t.Fatalf("call %d modelProviders = %#v, want non-nil empty", i, params.ModelProviders)
		}
	}

	if calls[0].list.Cursor != nil {
		t.Fatalf("first pass cursor = %v, want nil", calls[0].list.Cursor)
	}
	if calls[1].list.Cursor != nil {
		t.Fatalf("first page cursor = %v, want nil", calls[1].list.Cursor)
	}
	if calls[2].list.Cursor == nil || *calls[2].list.Cursor != "cursor-2" {
		t.Fatalf("second page cursor = %v, want cursor-2", calls[2].list.Cursor)
	}

	wantPinned := []string{"pin-1", "pin-2", "pin-3"}
	if len(threads) != len(wantPinned) {
		t.Fatalf("pinned threads = %#v, want %v", threads, wantPinned)
	}
	for i, want := range wantPinned {
		if threads[i].ID != want {
			t.Fatalf("pinned[%d] = %q, want %q (order must be preserved)", i, threads[i].ID, want)
		}
	}

	// The dashboard reader returns the same ids in shared section order.
	reader := PinnedThreadsReader(request)
	if reader == nil {
		t.Fatal("PinnedThreadsReader returned nil for a non-nil request")
	}
	ids, readerSupported, readerErr := reader()
	if readerErr != nil || !readerSupported {
		t.Fatalf("PinnedThreadsReader = (%v, %v, %v), want supported ids", ids, readerSupported, readerErr)
	}
	if len(ids) != len(wantPinned) {
		t.Fatalf("pinned ids = %v, want %v", ids, wantPinned)
	}
	for i, want := range wantPinned {
		if ids[i] != want {
			t.Fatalf("pinned ids[%d] = %q, want %q", i, ids[i], want)
		}
	}
}

// TestListPinnedThreadsReportsUnsupportedServerLikeRust covers Rust #51500's
// `Err(TypedRequestError::Server { source, .. }) if matches!(source.code,
// -32602..=-32600) => Ok(None)`: an invalid request/params answer means the
// server has no shared thread sections, so pinning is disabled rather than
// reported as a failure, and any other error is surfaced. Rust counterpart:
// `list_pinned_threads` (agents_overview_discovery.rs).
func TestListPinnedThreadsReportsUnsupportedServerLikeRust(t *testing.T) {
	for _, code := range []int{
		appserver.JSONRPCInvalidParamsErrorCode,
		appserver.JSONRPCMethodNotFoundErrorCode,
		appserver.JSONRPCInvalidRequestErrorCode,
	} {
		code := code
		request := func(method string, params any, result any) (int, error) {
			return code, errors.New("shared sections unavailable")
		}
		threads, supported, err := ListPinnedThreads(request)
		if threads != nil || supported || err != nil {
			t.Fatalf("rpcCode %d: ListPinnedThreads = (%v, %v, %v), want (nil, false, nil)", code, threads, supported, err)
		}
		reader := PinnedThreadsReader(request)
		ids, readerSupported, readerErr := reader()
		if ids != nil || readerSupported || readerErr != nil {
			t.Fatalf("rpcCode %d: PinnedThreadsReader = (%v, %v, %v), want (nil, false, nil)", code, ids, readerSupported, readerErr)
		}
	}

	// A non-JSON-RPC failure (code 0) is reported so callers keep their pins.
	transportErr := errors.New("transport down")
	request := func(method string, params any, result any) (int, error) {
		return 0, transportErr
	}
	threads, supported, err := ListPinnedThreads(request)
	if threads != nil || supported || !errors.Is(err, transportErr) {
		t.Fatalf("ListPinnedThreads = (%v, %v, %v), want (nil, false, transport down)", threads, supported, err)
	}

	// A nil request function disables pinning without failing.
	if threads, supported, err := ListPinnedThreads(nil); threads != nil || supported || err != nil {
		t.Fatalf("ListPinnedThreads(nil) = (%v, %v, %v), want (nil, false, nil)", threads, supported, err)
	}
	if reader := PinnedThreadsReader(nil); reader != nil {
		t.Fatal("PinnedThreadsReader(nil) must be nil")
	}
}

// TestPinTogglerMovesThreadToSharedSectionLikeRust covers Rust #51500's
// `toggle_agents_overview_pin`: pinning sends thread/section/move into the
// shared pinned section and unpinning sends a set-but-null sectionId, both with
// no before_thread_id, and an empty thread id is a no-op. Rust counterpart:
// `pin_action_updates_shared_section_state` (agents_overview_actions_tests.rs).
func TestPinTogglerMovesThreadToSharedSectionLikeRust(t *testing.T) {
	var calls []pinnedTransportCall
	request := func(method string, params any, result any) (int, error) {
		call := pinnedTransportCall{method: method}
		move := params.(appserver.ThreadSectionMoveParams)
		call.move = &move
		calls = append(calls, call)
		return 0, nil
	}

	toggle := PinToggler(request)
	if toggle == nil {
		t.Fatal("PinToggler returned nil for a non-nil request")
	}

	if err := toggle("thread-1", true); err != nil {
		t.Fatalf("pin error = %v, want nil", err)
	}
	if len(calls) != 1 {
		t.Fatalf("pin issued %d requests, want 1", len(calls))
	}
	if calls[0].method != string(appserver.MethodThreadSectionMove) {
		t.Fatalf("pin method = %q, want %q", calls[0].method, appserver.MethodThreadSectionMove)
	}
	pin := calls[0].move
	if pin == nil || pin.ThreadID != "thread-1" {
		t.Fatalf("pin params = %#v, want thread-1", pin)
	}
	if !pin.SectionID.Set || pin.SectionID.Value == nil || *pin.SectionID.Value != session.PinnedThreadSectionID {
		t.Fatalf("pin sectionId = %#v, want %q", pin.SectionID, session.PinnedThreadSectionID)
	}
	if pin.BeforeThreadID != nil {
		t.Fatalf("pin beforeThreadId = %v, want nil", pin.BeforeThreadID)
	}

	if err := toggle("thread-1", false); err != nil {
		t.Fatalf("unpin error = %v, want nil", err)
	}
	if len(calls) != 2 {
		t.Fatalf("unpin issued %d requests total, want 2", len(calls))
	}
	if calls[1].method != string(appserver.MethodThreadSectionMove) {
		t.Fatalf("unpin method = %q, want %q", calls[1].method, appserver.MethodThreadSectionMove)
	}
	unpin := calls[1].move
	if unpin == nil || unpin.ThreadID != "thread-1" {
		t.Fatalf("unpin params = %#v, want thread-1", unpin)
	}
	if !unpin.SectionID.Set || unpin.SectionID.Value != nil {
		t.Fatalf("unpin sectionId = %#v, want set-but-null", unpin.SectionID)
	}
	if unpin.BeforeThreadID != nil {
		t.Fatalf("unpin beforeThreadId = %v, want nil", unpin.BeforeThreadID)
	}

	// An empty thread id never reaches the transport.
	if err := toggle("   ", true); err != nil {
		t.Fatalf("empty thread id error = %v, want nil", err)
	}
	if len(calls) != 2 {
		t.Fatalf("empty thread id issued %d requests total, want 2", len(calls))
	}

	// A failing request is returned to the caller.
	moveErr := errors.New("move rejected")
	failing := PinToggler(func(method string, params any, result any) (int, error) {
		return appserver.JSONRPCInvalidRequestErrorCode, moveErr
	})
	if err := failing("thread-1", true); !errors.Is(err, moveErr) {
		t.Fatalf("failing pin error = %v, want %v", err, moveErr)
	}

	if PinToggler(nil) != nil {
		t.Fatal("PinToggler(nil) must be nil")
	}
}

// TestAgentsOverviewPinRequestDerivesCallbacksLikeRust covers Rust #51500's host
// wiring: one injected app-server request drives both the pinned-section
// discovery (the dashboard learns the shared section is supported) and the
// `p` shortcut's thread/section/move. Rust counterpart:
// `pin_action_updates_shared_section_state` (agents_overview_actions_tests.rs).
func TestAgentsOverviewPinRequestDerivesCallbacksLikeRust(t *testing.T) {
	type moveCall struct {
		threadID string
		pinned   bool
	}
	var listCalls int
	var moves []moveCall
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return []agentsoverview.Row{
				{ThreadID: "root-1", Name: "alpha", Preview: "fix parser", CWD: "/work/a", Group: agentsoverview.GroupWorking, Source: "cli"},
				{ThreadID: "root-2", Name: "beta", Preview: "review pr", CWD: "/work/b", Group: agentsoverview.GroupReady, Source: "cli"},
			}, nil
		},
		OnAgentsOverviewPinRequest: func(method string, params any, result any) (int, error) {
			switch method {
			case string(appserver.MethodThreadList):
				listCalls++
				page := result.(*appserver.ThreadListResponse)
				page.Data = nil
				page.NextCursor = nil
				return 0, nil
			case string(appserver.MethodThreadSectionMove):
				move := params.(appserver.ThreadSectionMoveParams)
				pinned := move.SectionID.Value != nil && *move.SectionID.Value == session.PinnedThreadSectionID
				moves = append(moves, moveCall{threadID: move.ThreadID, pinned: pinned})
				return 0, nil
			default:
				t.Fatalf("unexpected method %q", method)
				return 0, nil
			}
		},
	})

	openAgentsDashboard(t, model)
	if !model.agentsOverview.PinsSupported() {
		t.Fatal("a supported thread/list response should enable pinning")
	}
	if listCalls == 0 {
		t.Fatal("the derived reader never issued thread/list")
	}

	updated, pinCommand := model.Update(agentsKeyEvent('p'))
	model = updated.(*Model)
	if pinCommand == nil {
		t.Fatal("p returned no pin command")
	}
	updated, _ = model.Update(pinCommand())
	model = updated.(*Model)
	if len(moves) != 1 {
		t.Fatalf("p issued %d thread/section/move requests, want 1", len(moves))
	}
	if moves[0].threadID != "root-1" || !moves[0].pinned {
		t.Fatalf("pin move = %#v, want root-1 pinned", moves[0])
	}
	if !model.agentsOverview.IsPinned("root-1") {
		t.Fatal("successful pin did not update the local pin order")
	}
}
