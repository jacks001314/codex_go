package chatwidget

import "testing"

func TestInterruptManagerResolvedPromptMatchingMatchesRust(t *testing.T) {
	manager := NewInterruptManager()
	manager.PushExecApproval("call", "approval", nil)

	if manager.RemoveResolvedPrompt(ResolvedPrompt{Kind: QueuedInterruptExecApproval, ID: "call"}) {
		t.Fatalf("exec approval with approval id must not resolve by call id")
	}
	if manager.Len() != 1 {
		t.Fatalf("queue len = %d, want 1", manager.Len())
	}
	if !manager.RemoveResolvedPrompt(ResolvedPrompt{Kind: QueuedInterruptExecApproval, ID: "approval"}) {
		t.Fatalf("exec approval should resolve by effective approval id")
	}

	manager.PushExecApproval("call-only", "", nil)
	if !manager.RemoveResolvedPrompt(ResolvedPrompt{Kind: QueuedInterruptExecApproval, ID: "call-only"}) {
		t.Fatalf("exec approval without approval id should resolve by call id")
	}
}

func TestInterruptManagerElicitationAndLifecycleMatchingMatchesRust(t *testing.T) {
	manager := NewInterruptManager()
	manager.PushElicitation("server-a", "request-a", nil)
	manager.PushItemStarted("request-a", nil)

	if manager.RemoveResolvedPrompt(ResolvedPrompt{Kind: QueuedInterruptElicitation, ServerName: "server-b", RequestID: "request-a"}) {
		t.Fatalf("elicitation should require matching server and request id")
	}
	if !manager.RemoveResolvedPrompt(ResolvedPrompt{Kind: QueuedInterruptElicitation, ServerName: "server-a", RequestID: "request-a"}) {
		t.Fatalf("elicitation should resolve by server and request id")
	}
	if manager.RemoveResolvedPrompt(ResolvedPrompt{Kind: QueuedInterruptItemStarted, ID: "request-a"}) {
		t.Fatalf("lifecycle events should never match resolved prompts")
	}
	if manager.Len() != 1 {
		t.Fatalf("queue len = %d, want lifecycle item only", manager.Len())
	}
}

func TestInterruptManagerFlushAllPreservesFIFOOrderMatchRust(t *testing.T) {
	manager := NewInterruptManager()
	manager.PushRequestPermissions("perm", nil)
	manager.PushUserInput("input", nil)
	manager.PushItemCompleted("done", nil)

	flushed := manager.FlushAll()

	if len(flushed) != 3 ||
		flushed[0].Kind != QueuedInterruptRequestPermissions ||
		flushed[1].Kind != QueuedInterruptRequestUserInput ||
		flushed[2].Kind != QueuedInterruptItemCompleted {
		t.Fatalf("flush order = %#v", flushed)
	}
	if !manager.IsEmpty() {
		t.Fatalf("manager should be empty after flush, len=%d", manager.Len())
	}
}
