package appserver

import (
	"encoding/json"
	"testing"
)

func TestFeedbackThreadIndexPrioritizesFailedReviewsLikeRust(t *testing.T) {
	subtree := []string{"child-01", "child-02", "child-03", "child-04", "child-05", "child-06", "child-07", "child-08", "child-09", "child-10", "failed-new", "failed-old"}
	failures := guardianReviewFailures{
		ThreadIDs:               []string{"failed-new", "failed-old"},
		ProcessDiscardedRecords: 3,
	}
	threads, index := selectFeedbackTreeThreads("reported", subtree, failures)
	if len(threads) != maxFeedbackTreeThreads || threads[0] != "reported" ||
		threads[1] != "failed-new" || threads[2] != "failed-old" {
		t.Fatalf("selected feedback threads = %#v", threads)
	}
	if index.UnlistedOmittedThreadCount != 0 || len(index.OmittedThreadIDs) != 5 {
		t.Fatalf("omission metadata = listed %#v unlisted %d", index.OmittedThreadIDs, index.UnlistedOmittedThreadCount)
	}
	if index.ProcessDiscardedReviewRecords != 3 ||
		len(index.RetainedFailureThreadIDs) != 2 ||
		index.RetainedFailureThreadIDs[0] != "failed-new" {
		t.Fatalf("failure metadata = %#v", index)
	}
	index.setRolloutFilename("failed-new", "C:\\rollouts\\failed-new.jsonl")
	attachment, err := index.attachment()
	if err != nil {
		t.Fatal(err)
	}
	if attachment.Filename != feedbackThreadIndexFilename || attachment.ContentType != "application/json" {
		t.Fatalf("thread index attachment = %#v", attachment)
	}
	var decoded map[string]any
	if err := json.Unmarshal(attachment.Buffer, &decoded); err != nil {
		t.Fatal(err)
	}
	entries, _ := decoded["threads"].([]any)
	second, _ := entries[1].(map[string]any)
	if second["thread_id"] != "failed-new" || second["rollout_filename"] != "failed-new.jsonl" || second["guardian_rollout_filename"] != nil {
		t.Fatalf("thread index entry = %#v", second)
	}
}

func TestGuardianFeedbackDiscardedCountLikeRust(t *testing.T) {
	resetGuardianFeedbackRecordsForTest()
	t.Cleanup(resetGuardianFeedbackRecordsForTest)
	oversized := make([]byte, guardianFeedbackMaxBytes+1)
	recordGuardianReviewFailure("thread", oversized)
	snapshot := guardianReviewFailuresSnapshot([]string{"thread"})
	if snapshot.Attachment != nil || snapshot.ProcessDiscardedRecords != 1 {
		t.Fatalf("discarded snapshot = %#v", snapshot)
	}
}
