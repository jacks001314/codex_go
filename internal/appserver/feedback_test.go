package appserver

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestFeedbackUploadParamsValidateRequiresClassification(t *testing.T) {
	if err := (&FeedbackUploadParams{Classification: "bug"}).Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := (&FeedbackUploadParams{}).Validate(); !errors.Is(err, ErrInvalidFeedbackRequest) {
		t.Fatalf("Validate(empty classification) error = %v, want ErrInvalidFeedbackRequest", err)
	}
	if err := (*FeedbackUploadParams)(nil).Validate(); !errors.Is(err, ErrInvalidFeedbackRequest) {
		t.Fatalf("Validate(nil) error = %v, want ErrInvalidFeedbackRequest", err)
	}
}

func TestFeedbackUploadParamsJSONMatchesRustSchema(t *testing.T) {
	reason := "broken"
	threadID := "thread-1"
	data, err := json.Marshal(&FeedbackUploadParams{
		Classification: "bug",
		Reason:         &reason,
		ThreadID:       &threadID,
		IncludeLogs:    true,
		ExtraLogFiles:  []string{"/tmp/codex.log"},
		Tags:           map[string]string{"area": "app-server"},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	output := string(data)
	for _, want := range []string{
		`"classification":"bug"`,
		`"reason":"broken"`,
		`"threadId":"thread-1"`,
		`"includeLogs":true`,
		`"extraLogFiles":["/tmp/codex.log"]`,
		`"tags":{"area":"app-server"}`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("JSON missing %s: %s", want, output)
		}
	}

	data, err = json.Marshal(&FeedbackUploadParams{Classification: "bug"})
	if err != nil {
		t.Fatalf("Marshal(nil optionals) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	for _, key := range []string{"reason", "threadId", "extraLogFiles", "tags"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("optional nullable key %q omitted in %s", key, data)
		}
		if payload[key] != nil {
			t.Fatalf("key %q = %#v, want nil", key, payload[key])
		}
	}
	if _, ok := payload["includeLogs"]; ok {
		t.Fatalf("includeLogs=false should be omitted: %s", data)
	}
}

func TestDiagnosticsFromPairsAndAttachment(t *testing.T) {
	diagnostics := CollectFeedbackDiagnosticsFromPairs(map[string]string{
		"HTTPS_PROXY": "https://example.test",
		"all_proxy":   "socks5://example.test",
	})
	text := diagnostics.AttachmentText()
	if text == nil {
		t.Fatalf("AttachmentText() = nil")
	}
	want := "Connectivity diagnostics\n\n- Proxy environment variables are set and may affect connectivity.\n  - HTTPS_PROXY = https://example.test\n  - all_proxy = socks5://example.test"
	if *text != want {
		t.Fatalf("AttachmentText() = %q, want %q", *text, want)
	}
}

func TestDiagnosticsEmpty(t *testing.T) {
	diagnostics := CollectFeedbackDiagnosticsFromPairs(map[string]string{})
	if !diagnostics.IsEmpty() {
		t.Fatalf("IsEmpty() = false, want true")
	}
	if diagnostics.AttachmentText() != nil {
		t.Fatalf("AttachmentText() != nil for empty diagnostics")
	}
}

func TestUploadTagsPreserveReservedFields(t *testing.T) {
	reason := "actual"
	source := "cli"
	snapshot := &FeedbackSnapshot{
		ThreadID: "thread-1",
		Tags: map[string]string{
			"thread_id": "wrong",
			"model":     "gpt-5",
		},
	}
	tags := snapshot.UploadTags("bug", &reason, map[string]string{
		"reason":     "wrong",
		"client_tag": "yes",
	}, &source)
	if tags["thread_id"] != "thread-1" || tags["reason"] != "actual" || tags["session_source"] != "cli" {
		t.Fatalf("reserved tags = %+v", tags)
	}
	if tags["model"] != "gpt-5" || tags["client_tag"] != "yes" {
		t.Fatalf("merged tags = %+v", tags)
	}
}

func TestAttachmentsGateDiagnosticsOnIncludeLogs(t *testing.T) {
	snapshot := &FeedbackSnapshot{
		Logs:        []byte("logs"),
		ThreadID:    "thread-1",
		Diagnostics: CollectFeedbackDiagnosticsFromPairs(map[string]string{"HTTP_PROXY": "proxy"}),
	}
	attachments := snapshot.Attachments(true, []FeedbackAttachment{{Filename: "extra.txt", Buffer: []byte("extra")}}, []byte("override"))
	if len(attachments) != 3 {
		t.Fatalf("attachments = %+v, want 3", attachments)
	}
	if string(attachments[0].Buffer) != "override" || attachments[2].Filename != FeedbackDiagnosticsAttachmentFilename {
		t.Fatalf("attachments = %+v", attachments)
	}
	attachments = snapshot.Attachments(false, nil, nil)
	if len(attachments) != 0 {
		t.Fatalf("attachments without logs = %+v, want none", attachments)
	}
}

func TestPrepareUploadStoresLastPreparedClone(t *testing.T) {
	snapshot := &FeedbackSnapshot{
		Logs:        []byte("logs"),
		ThreadID:    "thread-1",
		Diagnostics: &FeedbackDiagnostics{},
	}
	prepared := snapshot.PrepareUpload(&FeedbackUploadOptions{
		Classification: "bug",
		IncludeLogs:    true,
		ExtraAttachmentPath: []FeedbackAttachmentPath{
			{Path: "/tmp/extra.log"},
		},
	})
	if snapshot.LastPrepared == nil || snapshot.LastPrepared.Tags["classification"] != "bug" {
		t.Fatalf("LastPrepared = %+v", snapshot.LastPrepared)
	}
	prepared.Tags["classification"] = "mutated"
	prepared.Attachments[0].Buffer[0] = 'X'
	if snapshot.LastPrepared.Tags["classification"] != "bug" || string(snapshot.LastPrepared.Attachments[0].Buffer) != "logs" {
		t.Fatalf("LastPrepared clone leaked mutation: %+v", snapshot.LastPrepared)
	}
}

func TestRingBufferKeepsTail(t *testing.T) {
	buffer := NewFeedbackRingBuffer(5)
	buffer.Write([]byte("abc"))
	buffer.Write([]byte("def"))
	if got := string(buffer.FeedbackSnapshot()); got != "bcdef" {
		t.Fatalf("FeedbackSnapshot() = %q, want bcdef", got)
	}
	buffer.Write([]byte("1234567"))
	if got := string(buffer.FeedbackSnapshot()); got != "34567" {
		t.Fatalf("FeedbackSnapshot() after large write = %q, want 34567", got)
	}
}
