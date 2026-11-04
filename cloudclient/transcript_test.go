package cloudclient

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

const testAgentItem = "\x1a\x06\x12\x01i\x32\x01d"
const testCommandItem = "\x0a\x00\x12\x01c\x1a\x07\x0a\x05\x22\x03\x0a\x01c\x22\x01w\x28\x00\x30\x00\x3a\x01i\x60\x03"

func protoMessage(tag int, value []byte) []byte {
	out := protowire.AppendTag(nil, protowire.Number(tag), protowire.BytesType)
	out = protowire.AppendBytes(out, value)
	return out
}

func protoVarint(tag int, value int64) []byte {
	out := protowire.AppendTag(nil, protowire.Number(tag), protowire.VarintType)
	return protowire.AppendVarint(out, uint64(value))
}

func testLifecycle(tag int, item string) []byte {
	atMS, emittedAtMS := int64(3), int64(7)
	if tag != 27 {
		atMS = 5
		emittedAtMS = 11
	}
	params := []byte{}
	if tag == 27 {
		params = append(params, protoMessage(1, []byte(item))...)
		params = append(params, protoVarint(2, atMS)...)
	} else {
		params = append(params, protoVarint(1, atMS)...)
		params = append(params, protoMessage(2, []byte(item))...)
	}
	params = append(params, protoMessage(3, []byte("t"))...)
	params = append(params, protoMessage(4, []byte("u"))...)
	envelope := append(protoVarint(1, emittedAtMS), protoMessage(2, params)...)
	return protoMessage(tag, envelope)
}

func TestTranscriptItemLifecycleKeepsIdentityAndTimestamps(t *testing.T) {
	event, ok, err := decodeTranscriptEvent("t", testLifecycle(27, testAgentItem))
	if err != nil || !ok {
		t.Fatalf("started decode = %#v %v %v", event, ok, err)
	}
	if event.ThreadID != "t" || event.EmittedAtMS == nil || *event.EmittedAtMS != 7 || event.Update.Kind != TranscriptItemStarted {
		t.Fatalf("started event = %#v", event)
	}
	if event.Update.Item.TurnID != "u" || event.Update.Item.AtMS != 3 || event.Update.Item.Item.Agent.ID != "i" || event.Update.Item.Item.Agent.Text != "d" {
		t.Fatalf("started item = %#v", event.Update.Item)
	}
	event, ok, err = decodeTranscriptEvent("t", testLifecycle(31, testAgentItem))
	if err != nil || !ok || event.Update.Kind != TranscriptItemCompleted || event.Update.Item.AtMS != 5 || event.EmittedAtMS == nil || *event.EmittedAtMS != 11 {
		t.Fatalf("completed event = %#v ok=%v err=%v", event, ok, err)
	}
}

func TestTranscriptUserMessagePreservesClientIDAndText(t *testing.T) {
	user := "\x0a\x23\x0a\x09client-id\x12\x0b\x0a\x09\x0a\x07\x0a\x05hello\x1a\x09user-item"
	item, ok, err := decodeItem([]byte(user))
	if err != nil || !ok || item.Kind != ItemUserMessage {
		t.Fatalf("decodeItem = %#v ok=%v err=%v", item, ok, err)
	}
	if item.User.ID != "user-item" || item.User.ClientID == nil || *item.User.ClientID != "client-id" || len(item.User.Text) != 1 || item.User.Text[0] != "hello" {
		t.Fatalf("user summary = %#v", item.User)
	}
}

func TestTranscriptCommandAndPatchSummariesPreserveStatus(t *testing.T) {
	for status := int64(1); status <= 4; status++ {
		command := protoMessage(7, append([]byte(testCommandItem), protoVarint(12, status)...))
		item, ok, err := decodeItem(command)
		if err != nil || !ok || item.Kind != ItemCommandExecution {
			t.Fatalf("command %d = %#v ok=%v err=%v", status, item, ok, err)
		}
		if item.Command.Status.Value != int(status) || !item.Command.Status.Known || item.Command.Source != CommandSourceAgent {
			t.Fatalf("command summary = %#v", item.Command)
		}
		if item.Command.AggregatedOutput == nil || item.Command.DurationMS == nil || item.Command.ExitCode == nil || len(item.Command.Actions) != 1 || item.Command.Actions[0].Kind != CommandActionUnknown {
			t.Fatalf("command optional fields = %#v", item.Command)
		}

		change := []byte("\x0a\x01d\x12\x02\x0a\x00\x1a\x01p")
		patchParams := append(protoMessage(1, protoMessage(1, change)), protoMessage(2, []byte("i"))...)
		patchParams = append(patchParams, protoVarint(3, status)...)
		patch, ok, err := decodeItem(protoMessage(8, patchParams))
		if err != nil || !ok || patch.Kind != ItemFileChange {
			t.Fatalf("patch %d = %#v ok=%v err=%v", status, patch, ok, err)
		}
		if patch.File.ID != "i" || patch.File.Status.Value != int(status) || len(patch.File.Changes) != 1 || patch.File.Changes[0].Kind.Kind != PatchAdd {
			t.Fatalf("patch summary = %#v", patch.File)
		}
	}
	command := protoMessage(7, append([]byte(testCommandItem), protoVarint(11, 2)...))
	item, ok, err := decodeItem(command)
	if err != nil || !ok || item.Command.Source != CommandSourceUserShell {
		t.Fatalf("user-shell command = %#v ok=%v err=%v", item, ok, err)
	}
	futureSource := protoMessage(7, append([]byte(testCommandItem), protoVarint(11, 99)...))
	if item, ok, err := decodeItem(futureSource); err != nil || ok {
		t.Fatalf("future command source = %#v ok=%v err=%v, want opaque", item, ok, err)
	}
}

func TestTranscriptReasoningAndQuestions(t *testing.T) {
	reasoning := []byte{}
	reasoning = append(reasoning, protoMessage(1, protoMessage(1, []byte("detail")))...)
	reasoning = append(reasoning, protoMessage(2, []byte("i"))...)
	reasoning = append(reasoning, protoMessage(3, protoMessage(1, []byte("summary")))...)
	item, ok, err := decodeItem(protoMessage(6, reasoning))
	if err != nil || !ok || item.Kind != ItemReasoning || item.Reasoning.ID != "i" || len(item.Reasoning.Summary) != 1 || len(item.Reasoning.Content) != 1 {
		t.Fatalf("reasoning = %#v ok=%v err=%v", item, ok, err)
	}

	choices := protoMessage(1, []byte("Continue"))
	questions := []byte{}
	questions = append(questions, protoMessage(1, protoMessage(2, []byte("Freeform")))...)
	questions = append(questions, protoMessage(1, []byte("\x0a\x00\x12\x05Empty"))...)
	question := append(protoMessage(1, choices), protoMessage(2, []byte("Proceed?"))...)
	questions = append(questions, protoMessage(1, question)...)
	agentParams := append(protoMessage(2, []byte("i")), protoMessage(6, []byte{})...)
	agentParams = append(agentParams, protoMessage(5, questions)...)
	item, ok, err = decodeItem(protoMessage(3, agentParams))
	if err != nil || !ok || item.Kind != ItemAgentMessage || !item.Agent.HasQuestions || len(item.Agent.Questions) != 3 {
		t.Fatalf("questions = %#v ok=%v err=%v", item, ok, err)
	}
	if item.Agent.Questions[0].Options != nil || item.Agent.Questions[1].Options == nil || len(*item.Agent.Questions[1].Options) != 0 || len(*item.Agent.Questions[2].Options) != 1 {
		t.Fatalf("question options = %#v", item.Agent.Questions)
	}
}

func TestTranscriptUnsupportedItemsStayOpaque(t *testing.T) {
	opaque := [][]byte{
		protoMessage(9, nil),
		protoMessage(20, nil),
		append([]byte(testAgentItem), protoMessage(19, nil)...),
		protoMessage(3, protoMessage(2, []byte("i"))),
		protoMessage(7, []byte("\x12\x01c\x22\x01w\x3a\x01i")),
		protoMessage(8, []byte("\x12\x01i\x18\x01")),
	}
	for _, item := range opaque {
		if event, ok, err := decodeTranscriptEvent("t", testLifecycle(27, string(item))); err != nil || ok {
			t.Fatalf("opaque item %x = %#v ok=%v err=%v", item, event, ok, err)
		}
	}
	if _, ok, err := decodeTranscriptEvent("t", protoMessage(27, protoMessage(2, protoMessage(1, []byte(testAgentItem))))); err != nil || ok {
		t.Fatalf("missing lifecycle time ok=%v err=%v", ok, err)
	}
	if _, _, err := decodeTranscriptEvent("other", testLifecycle(31, testAgentItem)); err == nil {
		t.Fatal("foreign item owner was accepted")
	}
}

func testNotification(tag int, emittedAtMS int64, params []byte) []byte {
	envelope := append(protoVarint(1, emittedAtMS), protoMessage(2, params)...)
	return protoMessage(tag, envelope)
}

func TestTranscriptDeltaPatchAndTurnUpdates(t *testing.T) {
	deltaParams := append(protoMessage(1, []byte("partial")), protoMessage(2, []byte("item"))...)
	deltaParams = append(deltaParams, protoMessage(3, []byte("thread"))...)
	deltaParams = append(deltaParams, protoMessage(4, []byte("turn"))...)
	event, ok, err := decodeTranscriptEvent("thread", testNotification(32, 9, deltaParams))
	if err != nil || !ok || event.Update.Kind != TranscriptAgentMessageDelta || event.Update.Delta.Delta != "partial" || event.Update.Delta.ItemID != "item" || event.EmittedAtMS == nil || *event.EmittedAtMS != 9 {
		t.Fatalf("delta = %#v ok=%v err=%v", event, ok, err)
	}

	change := []byte("\x0a\x01d\x12\x02\x0a\x00\x1a\x01p")
	changes := protoMessage(1, change)
	patchParams := append(protoMessage(1, changes), protoMessage(2, []byte("item"))...)
	patchParams = append(patchParams, protoMessage(3, []byte("thread"))...)
	patchParams = append(patchParams, protoMessage(4, []byte("turn"))...)
	event, ok, err = decodeTranscriptEvent("thread", testNotification(40, 10, patchParams))
	if err != nil || !ok || event.Update.Kind != TranscriptFileChangePatchUpdated || event.Update.Patch.ItemID != "item" || len(event.Update.Patch.Changes) != 1 || event.Update.Patch.Changes[0].Kind.Kind != PatchAdd {
		t.Fatalf("patch = %#v ok=%v err=%v", event, ok, err)
	}

	turn := []byte{}
	turn = append(turn, protoVarint(1, 120)...)
	turn = append(turn, protoVarint(2, 5000)...)
	turn = append(turn, protoMessage(4, []byte("turn-1"))...)
	turn = append(turn, protoVarint(7, 100)...)
	turn = append(turn, protoVarint(8, 1)...)
	turnParams := append(protoMessage(1, []byte("thread")), protoMessage(2, turn)...)
	event, ok, err = decodeTranscriptEvent("thread", testNotification(21, 11, turnParams))
	if err != nil || !ok || event.Update.Kind != TranscriptTurnStarted || event.Update.Turn.ID != "turn-1" || event.Update.Turn.Status != TurnCompleted || event.Update.Turn.StartedAt == nil || *event.Update.Turn.StartedAt != 100 || event.Update.Turn.DurationMS == nil || *event.Update.Turn.DurationMS != 5000 {
		t.Fatalf("turn = %#v ok=%v err=%v", event, ok, err)
	}
	if _, ok, err := decodeTranscriptEvent("other", testNotification(23, 12, turnParams)); err != nil || ok {
		t.Fatalf("foreign turn ok=%v err=%v, want skipped", ok, err)
	}
}
