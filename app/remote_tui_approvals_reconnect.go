package app

import (
	"context"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	codextea "codex_go/tui/tea"
)

// interactiveRemoteReconnectThreadApprovalsCommand rebuilds the background
// approval queue after a transport failure (Rust #58720).
//
// Go's remote clients are scoped to one turn, so an approval's answer channel
// dies with the connection that carried it. The app-server still holds the
// request and replays it when a connection resumes the owning thread
// (codex-rs/app-server/src/outgoing_message.rs:525
// replay_requests_to_connection_for_thread; Go counterpart
// appserver.RuntimeRouter.replayPendingServerRequestsForThread ->
// ServerRequestBroker.ReplayThreadToConnection). Resuming each waiting thread on
// a fresh connection therefore re-issues exactly the prompts that are still
// pending: resolved requests are not replayed, and a thread archived in the
// meantime is never resumed.
func interactiveRemoteReconnectThreadApprovalsCommand(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, brokers remoteTUIBrokers, threadIDs []string) bubbletea.Cmd {
	threads := make([]string, 0, len(threadIDs))
	seen := make(map[string]bool, len(threadIDs))
	for _, threadID := range threadIDs {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" || seen[threadID] {
			continue
		}
		seen[threadID] = true
		threads = append(threads, threadID)
	}
	if len(threads) == 0 {
		return nil
	}
	return func() bubbletea.Msg {
		messages := make(chan bubbletea.Msg, 64)
		go runRemoteThreadApprovalReconnect(ctx, root, endpoint, brokers, threads, messages)
		return codextea.StreamStartedMsg{Messages: messages}
	}
}

// runRemoteThreadApprovalReconnect resumes the waiting threads and keeps the
// connection alive while the replayed approvals are answered: the response for a
// replayed request has to travel back over the connection it was sent on, to the
// turn that asked for it.
func runRemoteThreadApprovalReconnect(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, brokers remoteTUIBrokers, threadIDs []string, messages chan<- bubbletea.Msg) {
	defer close(messages)
	if ctx == nil {
		ctx = context.Background()
	}
	client := &remoteAppServerTUIClient{
		endpoint:         endpoint,
		root:             root,
		threadParamsMode: threadParamsModeForLaunch(root),
		messages:         messages,
		brokers:          brokers,
		dial:             websocket.Dial,
	}
	if err := client.connect(ctx); err != nil {
		return
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		return
	}
	resumed := 0
	for _, threadID := range threadIDs {
		params, err := remoteTUIResumeParams(root, threadID, "")
		if err != nil {
			continue
		}
		if _, err := client.sendRequest(ctx, appserver.MethodThreadResume, params); err != nil {
			return
		}
		resumed++
	}
	if resumed == 0 {
		return
	}
	// readOne answers the resume responses and hands every replayed server
	// request to remoteApproval on its own goroutine, which publishes the normal
	// ApprovalRequestMsg and blocks until the operator decides.
	for {
		if err := client.readOne(ctx); err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}
