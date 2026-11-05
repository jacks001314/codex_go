package appserver

import (
	"encoding/json"
	"errors"
	"testing"

	"codex_go/session"
)

func outputDeferralCommandItem(id string, status CommandExecutionStatus, exitCode any, output any) session.Item {
	data := map[string]any{
		"turn_id":         "turn",
		"completed_at_ms": int64(100),
		"status":          string(status),
	}
	if exitCode != nil {
		data["exit_code"] = exitCode
	}
	if output != nil {
		data["aggregatedOutput"] = output
	}
	return session.Item{ID: id, Type: "commandExecution", Status: string(status), Data: data}
}

func TestThreadItemsListDefersSuccessfulCommandOutputLikeRust(t *testing.T) {
	large := "ééé"
	small := "x"
	record := &session.Record{ID: "thread", Items: []session.Item{
		outputDeferralCommandItem("large", CommandExecutionCompleted, int64(0), large),
		outputDeferralCommandItem("small", CommandExecutionCompleted, int64(0), small),
		outputDeferralCommandItem("empty", CommandExecutionCompleted, int64(0), ""),
		outputDeferralCommandItem("missing-output", CommandExecutionCompleted, int64(0), nil),
		outputDeferralCommandItem("missing-exit", CommandExecutionCompleted, nil, large),
		outputDeferralCommandItem("nonzero", CommandExecutionCompleted, int64(1), large),
		outputDeferralCommandItem("failed", CommandExecutionFailed, int64(0), large),
		outputDeferralCommandItem("declined", CommandExecutionDeclined, int64(0), large),
		outputDeferralCommandItem("running", CommandExecutionInProgress, int64(0), large),
	}}
	turnID := "turn"
	full, err := BuildItemsResponse(record, &ThreadItemsListParams{ThreadID: "thread", TurnID: &turnID})
	if err != nil {
		t.Fatalf("full BuildItemsResponse() error = %v", err)
	}
	for index, output := range []string{large, small, "", "", large, large, large, large, large} {
		got := full.Data[index].Item.Data["aggregatedOutput"]
		if output == "" && got == nil {
			continue
		}
		if got != output {
			t.Fatalf("full item %d output = %#v, want %q", index, got, output)
		}
		if full.Data[index].OutputDeferred {
			t.Fatalf("full item %d unexpectedly deferred", index)
		}
	}

	fullWire, err := json.Marshal(full.Data[0])
	if err != nil {
		t.Fatalf("json.Marshal(full entry) error = %v", err)
	}
	var fullMap map[string]any
	if err := json.Unmarshal(fullWire, &fullMap); err != nil {
		t.Fatalf("json.Unmarshal(full entry) error = %v", err)
	}
	if _, present := fullMap["outputDeferred"]; present {
		t.Fatalf("full entry serialized outputDeferred: %s", fullWire)
	}

	deferred, err := BuildItemsResponse(record, &ThreadItemsListParams{
		ThreadID: "thread", TurnID: &turnID, DeferOutput: &ThreadItemOutputDeferral{CommandExecution: true},
	})
	if err != nil {
		t.Fatalf("deferred BuildItemsResponse() error = %v", err)
	}
	for index := 0; index < 2; index++ {
		if !deferred.Data[index].OutputDeferred {
			t.Fatalf("entry %d OutputDeferred = false", index)
		}
	}
	for index := 2; index < len(deferred.Data); index++ {
		if deferred.Data[index].OutputDeferred {
			t.Fatalf("ineligible entry %d OutputDeferred = true", index)
		}
	}

	encoded, err := json.Marshal(deferred.Data[0])
	if err != nil {
		t.Fatalf("json.Marshal(entry) error = %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("json.Unmarshal(entry) error = %v", err)
	}
	item := wire["item"].(map[string]any)
	if wire["outputDeferred"] != true || item["aggregatedOutput"] != nil {
		t.Fatalf("deferred wire entry = %s", encoded)
	}
	if got := record.Items[0].Data["aggregatedOutput"]; got != large {
		t.Fatalf("stored history output mutated to %#v", got)
	}

	if _, err := deferred.Data[0].ResolveThreadItem(); err == nil {
		t.Fatal("ResolveThreadItem(deferred) = nil error")
	} else {
		var deferredErr *DeferredItemOutputError
		if !errors.As(err, &deferredErr) || deferredErr.TurnID != "turn" || deferredErr.ItemID != "large" {
			t.Fatalf("ResolveThreadItem(deferred) error = %#v", err)
		}
	}

	read, err := BuildItemsReadResponse(record, &ThreadItemsReadParams{ThreadID: "thread", TurnID: "turn", ItemIDs: []string{"large", "small"}})
	if err != nil {
		t.Fatalf("BuildItemsReadResponse() error = %v", err)
	}
	if len(read.Data) != 2 || read.Data[0].Item.Data["aggregatedOutput"] != large || read.Data[1].Item.Data["aggregatedOutput"] != small {
		t.Fatalf("thread/items/read output = %#v", read.Data)
	}
}

func TestThreadItemsListOutputDeferralRequiresTurnAndExperimentalFieldLikeRust(t *testing.T) {
	turnID := "turn"
	record := &session.Record{ID: "thread", Items: []session.Item{outputDeferralCommandItem("command", CommandExecutionCompleted, int64(0), "output")}}
	for _, params := range []*ThreadItemsListParams{
		{ThreadID: "thread", DeferOutput: &ThreadItemOutputDeferral{CommandExecution: true}},
		{ThreadID: "thread", TurnID: &turnID, DeferOutput: &ThreadItemOutputDeferral{CommandExecution: false}},
	} {
		response, err := BuildItemsResponse(record, params)
		if err != nil {
			t.Fatalf("BuildItemsResponse() error = %v", err)
		}
		if response.Data[0].OutputDeferred || response.Data[0].Item.Data["aggregatedOutput"] != "output" {
			t.Fatalf("ineligible deferral params %#v returned %#v", params, response.Data[0])
		}
	}

	for raw, want := range map[string]string{
		`{"threadId":"thread","turnId":"turn","deferOutput":{"commandExecution":true,"unknownItemKind":true}}`:  "thread/items/list.deferOutput.commandExecution",
		`{"threadId":"thread","turnId":"turn","deferOutput":{"commandExecution":false,"unknownItemKind":true}}`: "",
		`{"threadId":"thread","turnId":"turn","deferOutput":{"unknownItemKind":true}}`:                          "",
	} {
		request := &Request{Method: MethodThreadItemsList, Params: json.RawMessage(raw)}
		if got := experimentalAPIFieldReasonForRequest(request); got != want {
			t.Fatalf("experimental reason for %s = %q, want %q", raw, got, want)
		}
	}
}
