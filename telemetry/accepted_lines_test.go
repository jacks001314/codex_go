package telemetry

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestAcceptedLineFingerprintsFromUnifiedDiff(t *testing.T) {
	diff := `diff --git a/src/lib.rs b/src/lib.rs
--- a/src/lib.rs
+++ b/src/lib.rs
@@ -1,3 +1,5 @@
-old line
+fn useful() {
+}
+    return user.id;
 context
`
	summary := AcceptedLineFingerprintsFromUnifiedDiff(diff)
	if summary.AcceptedAddedLines != 3 || summary.AcceptedDeletedLines != 1 {
		t.Fatalf("unexpected counts: %#v", summary)
	}
	if len(summary.LineFingerprints) != 2 {
		t.Fatalf("unexpected fingerprints: %#v", summary.LineFingerprints)
	}
	if summary.LineFingerprints[0].PathHash != FingerprintHash("path", "src/lib.rs") {
		t.Fatalf("unexpected path hash")
	}
	if summary.LineFingerprints[0].LineHash != FingerprintHash("line", "fn useful() {") {
		t.Fatalf("unexpected first line hash")
	}
}

func TestAcceptedLineFingerprintsSkipsAddedFileHeaders(t *testing.T) {
	diff := `diff --git a/new.py b/new.py
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/new.py
@@ -0,0 +1 @@
+print('hello')
`
	summary := AcceptedLineFingerprintsFromUnifiedDiff(diff)
	if summary.AcceptedAddedLines != 1 || summary.AcceptedDeletedLines != 0 {
		t.Fatalf("unexpected counts: %#v", summary)
	}
	if len(summary.LineFingerprints) != 1 {
		t.Fatalf("fingerprints = %#v", summary.LineFingerprints)
	}
}

func TestAcceptedLineFingerprintsParsesHunkLinesThatLookLikeHeaders(t *testing.T) {
	diff := `diff --git a/src/lib.rs b/src/lib.rs
index 1111111..2222222
--- a/src/lib.rs
+++ b/src/lib.rs
@@ -1,2 +1,2 @@
--- old value
+++ new value
`
	summary := AcceptedLineFingerprintsFromUnifiedDiff(diff)
	if summary.AcceptedAddedLines != 1 || summary.AcceptedDeletedLines != 1 {
		t.Fatalf("unexpected counts: %#v", summary)
	}
	if len(summary.LineFingerprints) != 1 {
		t.Fatalf("fingerprints = %#v", summary.LineFingerprints)
	}
	if summary.LineFingerprints[0].LineHash != FingerprintHash("line", "++ new value") {
		t.Fatalf("unexpected line hash: %#v", summary.LineFingerprints[0])
	}
}

func TestAcceptedLineFingerprintEventRequestsDropLineFingerprints(t *testing.T) {
	surface := "cli"
	model := "gpt-5"
	repo := FingerprintHash("repo", "git@example.com:repo.git")
	requests := AcceptedLineFingerprintEventRequests(&AcceptedLineFingerprintEventInput{
		EventType:            "accepted_patch",
		TurnID:               "turn-1",
		ThreadID:             "thread-1",
		ProductSurface:       &surface,
		ModelSlug:            &model,
		CompletedAt:          123,
		RepoHash:             &repo,
		AcceptedAddedLines:   2,
		AcceptedDeletedLines: 1,
		LineFingerprints: []AcceptedLineFingerprint{{
			PathHash: "path",
			LineHash: "line",
		}},
	})
	if len(requests) != 1 {
		t.Fatalf("requests = %#v", requests)
	}
	if requests[0].EventType != "codex_accepted_line_fingerprints" {
		t.Fatalf("event_type = %q", requests[0].EventType)
	}
	if len(requests[0].Params.LineFingerprints) != 0 {
		t.Fatalf("line fingerprints should not be uploaded: %#v", requests[0].Params.LineFingerprints)
	}
	if requests[0].Params.AcceptedAddedLines != 2 || requests[0].Params.AcceptedDeletedLines != 1 {
		t.Fatalf("counts = %#v", requests[0].Params)
	}
}

func TestAcceptedLineFingerprintEventSerializesExpectedRustShape(t *testing.T) {
	productSurface := "codex"
	model := "gpt-5.1-codex"
	repo := "repo-hash-1"
	requests := AcceptedLineFingerprintEventRequests(&AcceptedLineFingerprintEventInput{
		TurnID:               "turn-1",
		ThreadID:             "thread-1",
		ProductSurface:       &productSurface,
		ModelSlug:            &model,
		CompletedAt:          1710000000,
		RepoHash:             &repo,
		AcceptedAddedLines:   42,
		AcceptedDeletedLines: 40,
		LineFingerprints: []AcceptedLineFingerprint{{
			PathHash: "path",
			LineHash: "line",
		}},
	})
	var got any
	if err := marshalUnmarshalTelemetry(requests[0], &got); err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	var want any
	if err := json.Unmarshal([]byte(`{
		"event_type": "codex_accepted_line_fingerprints",
		"event_params": {
			"event_type": "codex.accepted_line_fingerprints",
			"turn_id": "turn-1",
			"thread_id": "thread-1",
			"product_surface": "codex",
			"model_slug": "gpt-5.1-codex",
			"completed_at": 1710000000,
			"repo_hash": "repo-hash-1",
			"accepted_added_lines": 42,
			"accepted_deleted_lines": 40,
			"line_fingerprints": []
		}
	}`), &want); err != nil {
		t.Fatalf("unmarshal expected event error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event JSON mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestAcceptedLineFingerprintEventSerializesNullOptionFieldsLikeRust(t *testing.T) {
	requests := AcceptedLineFingerprintEventRequests(&AcceptedLineFingerprintEventInput{
		TurnID:               "turn-1",
		ThreadID:             "thread-1",
		CompletedAt:          1,
		AcceptedAddedLines:   1,
		AcceptedDeletedLines: 0,
	})
	data, err := json.Marshal(requests[0])
	if err != nil {
		t.Fatalf("marshal event error = %v", err)
	}
	for _, key := range []string{`"product_surface":null`, `"model_slug":null`, `"repo_hash":null`} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("payload %s missing %s", data, key)
		}
	}
}

func TestNormalizeDiffPath(t *testing.T) {
	if NormalizeDiffPath("b/foo/bar.go") != "foo/bar.go" {
		t.Fatalf("unexpected normalized path")
	}
	if NormalizeDiffPath("/dev/null") != "" {
		t.Fatalf("/dev/null should be skipped")
	}
}

func TestNormalizeEffectiveLine(t *testing.T) {
	if line, ok := NormalizeEffectiveLine("   return   user.id; "); !ok || line != "return user.id;" {
		t.Fatalf("unexpected normalized line: %q %v", line, ok)
	}
	if _, ok := NormalizeEffectiveLine("}"); ok {
		t.Fatalf("punctuation-only short line should be skipped")
	}
}
