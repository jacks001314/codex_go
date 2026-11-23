package appserver

import (
	"testing"
)

// Mirrors Rust #62609: replay re-checks whether each request is still pending
// before sending it, so a request resolved after the snapshot is skipped.
func TestReplaySkipsRequestsResolvedAfterSnapshotLikeRust(t *testing.T) {
	broker := NewServerRequestBroker()
	pendingApproval := func(id int64) *pendingServerRequest {
		return &pendingServerRequest{
			ch: make(chan *serverRequestResult, 1),
			request: &ServerRequest{
				ID: IntID(id), Method: ServerRequestCommandExecutionApproval,
				Params: CommandExecutionRequestApprovalParams{ThreadID: "thread"},
			},
			threadID: "thread",
		}
	}
	entryA := pendingApproval(1)
	entryB := pendingApproval(2)
	entryBID := entryB.request.ID.String()
	var sent []string
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		sent = append(sent, request.ID.String())
		if len(sent) == 1 {
			// Resolve the second request between its snapshot and its send.
			broker.unregister(entryBID)
		}
	}))
	broker.register(entryA.request.ID.String(), entryA)
	broker.register(entryBID, entryB)

	if got := broker.ReplayThreadToConnection("thread", "conn"); got != 1 {
		t.Fatalf("replayed = %d, want 1", got)
	}
	if len(sent) != 1 || sent[0] != entryA.request.ID.String() {
		t.Fatalf("sent = %v, want only the first still-pending request", sent)
	}
}
