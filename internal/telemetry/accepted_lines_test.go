package telemetry

import "testing"

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
	if requests[0].Type != "codex_accepted_line_fingerprints" {
		t.Fatalf("type = %q", requests[0].Type)
	}
	if len(requests[0].Params.LineFingerprints) != 0 {
		t.Fatalf("line fingerprints should not be uploaded: %#v", requests[0].Params.LineFingerprints)
	}
	if requests[0].Params.AcceptedAddedLines != 2 || requests[0].Params.AcceptedDeletedLines != 1 {
		t.Fatalf("counts = %#v", requests[0].Params)
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
