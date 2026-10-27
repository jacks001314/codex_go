package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

var feedbackRolloutJSONL = []byte("{\"message\":\"old diagnostic\"}\n{\"message\":\"more details\"}\n")

const feedbackRolloutThreadID = "0192f0aa-0000-7000-8000-0000000000a0"

// writeCompressedFeedbackRollout writes only the `.jsonl.zst` representation and
// returns both logical paths.
func writeCompressedFeedbackRollout(t *testing.T, dir string, payload []byte) (plain string, compressed string) {
	t.Helper()
	plain = filepath.Join(dir, "rollout-2026-09-09T12-00-00-"+feedbackRolloutThreadID+".jsonl")
	compressed = plain + ".zst"
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("zstd.NewWriter() error = %v", err)
	}
	defer encoder.Close()
	if err := os.WriteFile(compressed, encoder.EncodeAll(payload, nil), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", compressed, err)
	}
	return plain, compressed
}

// Mirrors Rust `compressed_paths_keep_canonical_names_and_filename_overrides`
// (#44175): a `.jsonl.zst` attachment is decoded and uploaded under the logical
// `.jsonl` name, while a caller override still wins.
func TestFeedbackReadAttachmentDecodesCompressedRolloutLikeRust(t *testing.T) {
	dir := t.TempDir()
	plain, compressed := writeCompressedFeedbackRollout(t, dir, feedbackRolloutJSONL)
	for _, override := range []string{"", "reviewer-rollout.jsonl"} {
		attachment := FeedbackAttachmentPath{Path: compressed}
		wantName := filepath.Base(plain)
		if override != "" {
			value := override
			attachment.AttachmentFilenameOverride = &value
			wantName = override
		}
		got, ok, err := FeedbackReadAttachmentPath(attachment, int64(len(feedbackRolloutJSONL)))
		if err != nil || !ok {
			t.Fatalf("FeedbackReadAttachmentPath(override=%q): ok=%v err=%v", override, ok, err)
		}
		if got.Filename != wantName {
			t.Fatalf("filename = %q, want %q", got.Filename, wantName)
		}
		if got.ContentType != "text/plain" {
			t.Fatalf("content type = %q, want text/plain", got.ContentType)
		}
		if string(got.Buffer) != string(feedbackRolloutJSONL) {
			t.Fatalf("buffer = %q, want decoded JSONL", string(got.Buffer))
		}
	}
}

// Mirrors Rust `compressed_attachment_prefers_newer_plain_sibling`: the plain
// representation wins when both exist, and the compressed copy is untouched.
func TestFeedbackReadAttachmentPrefersPlainSiblingLikeRust(t *testing.T) {
	dir := t.TempDir()
	plain, compressed := writeCompressedFeedbackRollout(t, dir, feedbackRolloutJSONL)
	current := []byte("{\"message\":\"new diagnostic\"}\n")
	if err := os.WriteFile(plain, current, 0o600); err != nil {
		t.Fatalf("WriteFile(plain) error = %v", err)
	}
	got, ok, err := FeedbackReadAttachmentPath(FeedbackAttachmentPath{Path: compressed}, 1024)
	if err != nil || !ok {
		t.Fatalf("FeedbackReadAttachmentPath: ok=%v err=%v", ok, err)
	}
	if string(got.Buffer) != string(current) {
		t.Fatalf("buffer = %q, want the plain sibling", string(got.Buffer))
	}
	if _, err := os.Stat(compressed); err != nil {
		t.Fatalf("compressed copy must be untouched: %v", err)
	}
}

// Mirrors Rust `queued_attachments_follow_both_representation_transitions`: the
// representation is resolved at read time, not when the path is queued.
func TestFeedbackReadAttachmentFollowsRepresentationTransitionsLikeRust(t *testing.T) {
	dir := t.TempDir()
	plain, compressed := writeCompressedFeedbackRollout(t, dir, feedbackRolloutJSONL)

	// The compressed path resolves to a plain file materialized after queuing.
	if err := os.WriteFile(plain, feedbackRolloutJSONL, 0o600); err != nil {
		t.Fatalf("WriteFile(plain) error = %v", err)
	}
	if err := os.Remove(compressed); err != nil {
		t.Fatalf("Remove(compressed) error = %v", err)
	}
	got, ok, err := FeedbackReadAttachmentPath(FeedbackAttachmentPath{Path: compressed}, 1024)
	if err != nil || !ok || string(got.Buffer) != string(feedbackRolloutJSONL) {
		t.Fatalf("materialized transition: ok=%v err=%v buffer=%q", ok, err, string(got.Buffer))
	}

	// The plain path resolves to a compressed file written after queuing.
	writeCompressedFeedbackRollout(t, dir, feedbackRolloutJSONL)
	if err := os.Remove(plain); err != nil {
		t.Fatalf("Remove(plain) error = %v", err)
	}
	got, ok, err = FeedbackReadAttachmentPath(FeedbackAttachmentPath{Path: plain}, 1024)
	if err != nil || !ok || string(got.Buffer) != string(feedbackRolloutJSONL) {
		t.Fatalf("compressed transition: ok=%v err=%v buffer=%q", ok, err, string(got.Buffer))
	}
}

// Mirrors Rust `compressed_attachment_bounds_decoded_bytes`: the limit applies to
// decoded bytes, and reading must not materialize the plain file.
func TestFeedbackReadAttachmentBoundsDecodedBytesLikeRust(t *testing.T) {
	dir := t.TempDir()
	plain, compressed := writeCompressedFeedbackRollout(t, dir, feedbackRolloutJSONL)
	if _, ok, err := FeedbackReadAttachmentPath(FeedbackAttachmentPath{Path: compressed}, int64(len(feedbackRolloutJSONL))-1); err != nil || ok {
		t.Fatalf("undersized limit: ok=%v err=%v", ok, err)
	}
	got, ok, err := FeedbackReadAttachmentPath(FeedbackAttachmentPath{Path: compressed}, int64(len(feedbackRolloutJSONL)))
	if err != nil || !ok {
		t.Fatalf("exact limit: ok=%v err=%v", ok, err)
	}
	if string(got.Buffer) != string(feedbackRolloutJSONL) {
		t.Fatalf("buffer = %q, want the full decoded JSONL", string(got.Buffer))
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatalf("reading must not materialize the plain rollout, stat err = %v", err)
	}
}

// Mirrors Rust `nonregular_rollout_is_omitted_and_unrelated_zstd_attachment_stays_opaque`:
// a rollout path that resolves to a directory is skipped, while a `.zst` file
// that is not a rollout is attached verbatim.
func TestFeedbackReadAttachmentKeepsUnrelatedZstdOpaqueLikeRust(t *testing.T) {
	dir := t.TempDir()
	plain, compressed := writeCompressedFeedbackRollout(t, dir, feedbackRolloutJSONL)
	if err := os.Remove(compressed); err != nil {
		t.Fatalf("Remove(compressed) error = %v", err)
	}
	if err := os.Mkdir(plain, 0o755); err != nil {
		t.Fatalf("Mkdir(plain) error = %v", err)
	}
	if _, ok, err := FeedbackReadAttachmentPath(FeedbackAttachmentPath{Path: plain}, 1024); err != nil || ok {
		t.Fatalf("non-regular rollout: ok=%v err=%v", ok, err)
	}

	opaque := filepath.Join(dir, "diagnostics.zst")
	_, compressed = writeCompressedFeedbackRollout(t, t.TempDir(), feedbackRolloutJSONL)
	data, err := os.ReadFile(compressed)
	if err != nil {
		t.Fatalf("ReadFile(compressed) error = %v", err)
	}
	if err := os.WriteFile(opaque, data, 0o600); err != nil {
		t.Fatalf("WriteFile(opaque) error = %v", err)
	}
	got, ok, err := FeedbackReadAttachmentPath(FeedbackAttachmentPath{Path: opaque}, 1024)
	if err != nil || !ok {
		t.Fatalf("opaque zstd: ok=%v err=%v", ok, err)
	}
	if got.Filename != "diagnostics.zst" || string(got.Buffer) != string(data) {
		t.Fatalf("opaque attachment = (%q, %d bytes), want (diagnostics.zst, %d bytes)", got.Filename, len(got.Buffer), len(data))
	}
}

// Mirrors Rust #55545: an undecodable compressed rollout is attached as its raw
// `.zst` archive when no preferred plain representation exists, under the
// bounded raw size and with a `.zst` suffix on filename overrides.
func TestFeedbackReadAttachmentPreservesUndecodableCompressedRolloutLikeRust(t *testing.T) {
	dir := t.TempDir()
	plain, compressed := writeCompressedFeedbackRollout(t, dir, feedbackRolloutJSONL)
	damaged := []byte("not zstd")
	if err := os.WriteFile(compressed, damaged, 0o600); err != nil {
		t.Fatalf("WriteFile(compressed) error = %v", err)
	}
	for _, path := range []string{plain, compressed} {
		attachment := FeedbackAttachmentPath{Path: path}
		if _, ok, err := FeedbackReadAttachmentPath(attachment, int64(len(damaged))-1); err != nil || ok {
			t.Fatalf("undersized raw fallback(%s): ok=%v err=%v", path, ok, err)
		}
		got, ok, err := FeedbackReadAttachmentPath(attachment, int64(len(damaged)))
		if err != nil || !ok {
			t.Fatalf("raw fallback(%s): ok=%v err=%v", path, ok, err)
		}
		if got.Filename != filepath.Base(compressed) || got.ContentType != "application/octet-stream" {
			t.Fatalf("raw fallback(%s) = %+v", path, got)
		}
		if string(got.Buffer) != string(damaged) {
			t.Fatalf("raw fallback(%s) buffer = %q", path, string(got.Buffer))
		}
		override := "diagnostic.jsonl"
		attachment.AttachmentFilenameOverride = &override
		got, ok, err = FeedbackReadAttachmentPath(attachment, int64(len(damaged)))
		if err != nil || !ok || got.Filename != "diagnostic.jsonl.zst" {
			t.Fatalf("override raw fallback(%s) = %+v ok=%v err=%v", path, got, ok, err)
		}
	}
	// A preferred plain representation is never replaced by the archive.
	if err := os.WriteFile(plain, feedbackRolloutJSONL, 0o600); err != nil {
		t.Fatalf("WriteFile(plain) error = %v", err)
	}
	got, ok, err := FeedbackReadAttachmentPath(FeedbackAttachmentPath{Path: compressed}, 1024)
	if err != nil || !ok || string(got.Buffer) != string(feedbackRolloutJSONL) {
		t.Fatalf("plain preferred: ok=%v err=%v buffer=%q", ok, err, string(got.Buffer))
	}
}
