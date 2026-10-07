// Shared task pinning transport: the Go port of Rust #51500's pinned-section
// read/write flow ("Add shared task pinning to the agent command center").
//
// Rust keeps this flow in the TUI's app layer:
//
//   - agents_overview_discovery::list_pinned_threads pages `thread/list` for the
//     shared pinned section (sortKey section_position, sectionId
//     PINNED_THREAD_SECTION_ID, useStateDBOnly, two source-kind passes), keeps
//     the overview threads whose source supports shared pinning, and reports
//     `Ok(None)` when the server rejects the request with an invalid
//     request/params error — that server has no shared thread sections.
//   - App::toggle_agents_overview_pin issues `thread/section/move` with the
//     pinned section id (or null to unpin) and no before_thread_id, then
//     refreshes the dashboard.
//
// The Go TUI keeps its transport in the host (app/), so this file carries the
// same flow behind a single injected request function: the host adapts its
// app-server client once (app's remoteSessionRequest plus the JSON-RPC code from
// its *remoteRPCError) and the TUI drives the paging, filtering and section move.
package tea

import (
	"strings"

	"codex_go/appserver"
	"codex_go/session"
	agentsoverview "codex_go/tui/agents_overview"
)

// AgentsOverviewPinRequestFunc issues one app-server request and decodes its
// result into result, reporting the JSON-RPC error code when the request failed
// (0 when the failure is not a JSON-RPC error). It is the Go shape of Rust's
// `AppServerRequestHandle::request_typed` (#51500), whose error carries
// `source.code`.
type AgentsOverviewPinRequestFunc func(method string, params any, result any) (rpcCode int, err error)

const pinnedThreadPageSize = 100

// pinnedThreadSourceKindPasses mirrors the two passes Rust list_pinned_threads
// runs: every source kind first, then the interactive (exec / app-server) kinds.
var pinnedThreadSourceKindPasses = [][]appserver.ThreadSourceKind{
	{},
	{appserver.ThreadSourceKindExec, appserver.ThreadSourceKindAppServer},
}

// ListPinnedThreads lists the tasks in the shared pinned section, in shared
// section order (Rust #51500 list_pinned_threads). It is the thread-level form
// of the discovery: the dashboard also needs the ids (PinnedThreadsReader), and
// a host that wants pinned tasks to render even when they fall outside its
// loaded/recent window builds rows from these threads and merges them (Rust
// agents_overview_threads inserts every pinned thread into the row map).
// supported=false reports a server without shared thread sections; any other
// failure is returned so callers keep the pins they already know about.
func ListPinnedThreads(request AgentsOverviewPinRequestFunc) (threads []appserver.Thread, supported bool, err error) {
	if request == nil {
		return nil, false, nil
	}
	var pinned []appserver.Thread
	for _, sourceKinds := range pinnedThreadSourceKindPasses {
		cursor := (*string)(nil)
		for {
			limit := pinnedThreadPageSize
			sectionID := session.PinnedThreadSectionID
			archived := false
			params := appserver.ThreadListParams{
				Cursor:         cursor,
				Limit:          &limit,
				SortKey:        appserver.SortSectionPosition,
				ModelProviders: []string{},
				SourceKinds:    sourceKinds,
				Archived:       &archived,
				SectionID:      appserver.OptionalString{Set: true, Value: &sectionID},
				UseStateDBOnly: true,
			}
			var page appserver.ThreadListResponse
			rpcCode, err := request(string(appserver.MethodThreadList), params, &page)
			if err != nil {
				if pinnedSectionUnsupported(rpcCode) {
					// Rust: Ok(None) — no shared thread sections on this server.
					return nil, false, nil
				}
				return nil, false, err
			}
			for _, thread := range page.Data {
				if !overviewPinnedThread(thread) {
					continue
				}
				if strings.TrimSpace(thread.ID) != "" {
					pinned = append(pinned, thread)
				}
			}
			if page.NextCursor == nil {
				break
			}
			cursor = page.NextCursor
		}
	}
	return pinned, true, nil
}

// PinnedThreadsReader returns the dashboard's shared pinned-section discovery
// callback (Rust #51500 list_pinned_threads), as the pinned task ids in shared
// section order. It returns supported=false when the server rejects the
// pinned-section query as an invalid request/params, which disables pinning in
// the dashboard; any other failure is returned so the dashboard keeps the pins
// it already knows about.
func PinnedThreadsReader(request AgentsOverviewPinRequestFunc) AgentsOverviewPinnedThreadsFunc {
	if request == nil {
		return nil
	}
	return func() ([]string, bool, error) {
		threads, supported, err := ListPinnedThreads(request)
		if err != nil || !supported {
			return nil, supported, err
		}
		pinned := make([]string, 0, len(threads))
		for _, thread := range threads {
			if id := strings.TrimSpace(thread.ID); id != "" {
				pinned = append(pinned, id)
			}
		}
		return pinned, true, nil
	}
}

// PinToggler returns the dashboard's pin/unpin callback (Rust #51500
// toggle_agents_overview_pin): the task is moved into the shared pinned section
// when pinning and out of every section when unpinning. `before_thread_id` stays
// unset in both directions, so the task lands at the end of the section exactly
// like the Rust request.
func PinToggler(request AgentsOverviewPinRequestFunc) AgentsOverviewTogglePinFunc {
	if request == nil {
		return nil
	}
	return func(threadID string, pinned bool) error {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return nil
		}
		params := appserver.ThreadSectionMoveParams{
			ThreadID: threadID,
			// A set-but-nil sectionId removes the thread from its section.
			SectionID: appserver.OptionalString{Set: true},
		}
		if pinned {
			sectionID := session.PinnedThreadSectionID
			params.SectionID.Value = &sectionID
		}
		if err := params.Validate(); err != nil {
			return err
		}
		var response appserver.ThreadSectionMoveResponse
		if _, err := request(string(appserver.MethodThreadSectionMove), params, &response); err != nil {
			return err
		}
		return nil
	}
}

// pinnedSectionUnsupported mirrors Rust's
// `matches!(source.code, -32602..=-32600)` check: an invalid request or invalid
// params answer means this server does not implement shared thread sections.
func pinnedSectionUnsupported(rpcCode int) bool {
	return rpcCode >= appserver.JSONRPCInvalidParamsErrorCode && rpcCode <= appserver.JSONRPCInvalidRequestErrorCode
}

// overviewPinnedThread mirrors Rust's is_overview_thread + supports_shared_pinning
// filter applied to every pinned-section page: ephemeral, child and
// thread-spawned subagent tasks are not dashboard tasks, and only sources that
// can live in a shared section are pinnable. The thread-spawn comparison is the
// exact `ThreadSourceKindSubAgentThreadSpawn` source, matching Rust's
// `SessionSource::SubAgent(SubAgentSource::ThreadSpawn)` arm.
func overviewPinnedThread(thread appserver.Thread) bool {
	if thread.Ephemeral || thread.ParentThreadID != nil {
		return false
	}
	source := strings.TrimSpace(string(thread.Source))
	if strings.EqualFold(source, string(appserver.ThreadSourceKindSubAgentThreadSpawn)) {
		return false
	}
	return agentsoverview.SupportsSharedPinning(source)
}
