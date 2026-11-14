package tea

import (
	"codex_go/protocol"
	codextui "codex_go/tui"
	"testing"
)

func TestAgentSwitchReconcilesHistoryAndBufferedReceiptsLikeRust(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		for _, buffered := range []bool{false, true} {
			model := newPendingSteerModel(t, "thread-a", nil)
			first := submitPendingSteer(t, model, "same")
			second := submitPendingSteer(t, model, "same")
			model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "already sent", ClientID: "queued-id"}}, {Request: SubmitRequest{Prompt: "waiting", ClientID: "waiting-id"}}}
			model.rejectedSteers = []queuedSubmission{{Request: SubmitRequest{Prompt: "retry", ClientID: "retry-id"}}}
			model.applyResumeResponse("thread-b", SessionResumeResponse{Status: "idle"})
			receipts := []codextui.Message{{Role: codextui.RoleUser, ClientID: second}, {Role: codextui.RoleUser, ClientID: "queued-id"}, {Role: codextui.RoleUser, ClientID: "retry-id"}, {Role: codextui.RoleAssistant, ClientID: first}}
			response := AgentThreadSwitchResponse{Entry: codextui.AgentThreadEntry{ThreadID: "thread-a"}, Status: "idle", ReadOnly: readOnly}
			if buffered {
				for _, receipt := range receipts {
					kind := "user_message"
					if receipt.Role == codextui.RoleAssistant {
						kind = "agent_message"
					}
					event := protocol.ItemCompleted(protocol.ThreadItem{Type: kind, ClientID: receipt.ClientID})
					model.backgroundThreadEvents["thread-a"] = append(model.backgroundThreadEvents["thread-a"], event)
				}
				// A nonempty historical snapshot must not suppress receipt reconciliation.
				response.Messages = []codextui.Message{{Role: codextui.RoleAssistant, Text: "older history"}}
			} else {
				response.Messages = receipts
			}
			model.applyAgentSwitchResult(AgentSwitchResultMsg{ThreadID: "thread-a", Response: response})
			if len(model.pendingSteers) != 1 || model.pendingSteers[0].ID != first {
				t.Fatalf("readOnly=%v buffered=%v pending=%#v", readOnly, buffered, model.pendingSteers)
			}
			if len(model.queued) != 1 || model.queued[0].Request.ClientID != "waiting-id" || len(model.rejectedSteers) != 0 {
				t.Fatalf("readOnly=%v buffered=%v queues=%#v retries=%#v", readOnly, buffered, model.queued, model.rejectedSteers)
			}
		}
	}
}

func TestBufferedReceiptRejectsWrongThreadAndUncommittedItemLikeRust(t *testing.T) {
	model := newPendingSteerModel(t, "thread-a", nil)
	id := submitPendingSteer(t, model, "uncertain")
	wrong := protocol.ItemCompleted(protocol.ThreadItem{Type: "user_message", ClientID: id})
	wrong.ThreadID = "thread-b"
	started := protocol.ItemCompleted(protocol.ThreadItem{Type: "user_message", ClientID: id})
	started.Type = "item.started"
	model.reconcileBufferedSessionInput("thread-a", []protocol.ThreadEvent{wrong, started})
	if len(model.pendingSteers) != 1 {
		t.Fatal("nonmatching event confirmed delivery")
	}
	right := protocol.ItemCompleted(protocol.ThreadItem{Type: "user_message", ClientID: id})
	right.ThreadID = "thread-a"
	model.reconcileBufferedSessionInput("thread-a", []protocol.ThreadEvent{right})
	if len(model.pendingSteers) != 0 {
		t.Fatal("matching receipt did not confirm delivery")
	}
}
