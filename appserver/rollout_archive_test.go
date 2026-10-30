package appserver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func readRolloutArchiveLikeRust(t *testing.T, buffer []byte) map[string]string {
	t.Helper()
	gzipReader, err := gzip.NewReader(bytes.NewReader(buffer))
	if err != nil {
		t.Fatalf("open rollout archive: %v", err)
	}
	defer gzipReader.Close()
	entries := map[string]string{}
	archive := tar.NewReader(gzipReader)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read rollout archive: %v", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		content, err := io.ReadAll(archive)
		if err != nil {
			t.Fatalf("read rollout archive entry %s: %v", header.Name, err)
		}
		if perm := header.Mode & 0o777; perm != 0o600 {
			t.Fatalf("entry %s mode = %o", header.Name, perm)
		}
		entries[header.Name] = string(content)
	}
	return entries
}

// TestFeedbackArchiveRolloutsBundlesFileAndBufferedRolloutsLikeRust mirrors the
// Rust #50446 upload test
// `feedback_upload_archives_two_rollouts_that_can_be_extracted_and_read`:
// a file-backed rollout and a buffered rollout both land in `rollouts.tar.gz`
// under their own filenames.
func TestFeedbackArchiveRolloutsBundlesFileAndBufferedRolloutsLikeRust(t *testing.T) {
	directory := t.TempDir()
	fileRollout := "rollout-2026-10-07T10-00-00-11111111-1111-7111-8111-111111111111.jsonl"
	fileRolloutPath := filepath.Join(directory, fileRollout)
	fileContent := "{\"type\":\"session_meta\"}\nfile-backed rollout\n"
	if err := os.WriteFile(fileRolloutPath, []byte(fileContent), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	bufferedRollout := "rollout-2026-10-07T11-00-00-22222222-2222-7222-8222-222222222222.jsonl"
	bufferedContent := "{\"type\":\"session_meta\"}\nbuffered prefix\n"

	attachments, paths := FeedbackRolloutArchive(filepath.Join(directory, "archive"),
		[]FeedbackAttachmentPath{{Path: fileRolloutPath}},
		[]FeedbackAttachment{{Filename: bufferedRollout, ContentType: "application/jsonl", Buffer: []byte(bufferedContent)}},
		FeedbackMaxDecodedUploadBytes)
	if len(paths) != 0 {
		t.Fatalf("fallback paths = %+v", paths)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments = %+v", attachments)
	}
	if attachments[0].Filename != FeedbackRolloutArchiveFilename {
		t.Fatalf("archive filename = %q", attachments[0].Filename)
	}
	if attachments[0].ContentType != FeedbackRolloutArchiveContentType {
		t.Fatalf("archive content type = %q", attachments[0].ContentType)
	}
	entries := readRolloutArchiveLikeRust(t, attachments[0].Buffer)
	if len(entries) != 2 {
		t.Fatalf("archive entries = %+v", entries)
	}
	if entries[fileRollout] != fileContent {
		t.Fatalf("file-backed rollout = %q", entries[fileRollout])
	}
	if entries[bufferedRollout] != bufferedContent {
		t.Fatalf("buffered rollout = %q", entries[bufferedRollout])
	}
}

func writeArchiveRolloutZstd(t *testing.T, dir string, threadID string, payload []byte) (string, string) {
	t.Helper()
	plain := filepath.Join(dir, "rollout-2026-09-09T12-00-00-"+threadID+".jsonl")
	compressed := plain + ".zst"
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

// TestFeedbackArchiveRolloutsIncludesCompressedSourcesLikeRust mirrors the
// Rust #50446 upload test: a compressed file-backed rollout is archived under
// its canonical `.jsonl` name with decoded bytes, while an undecodable
// compressed source is preserved verbatim under its `.jsonl.zst` name.
func TestFeedbackArchiveRolloutsIncludesCompressedSourcesLikeRust(t *testing.T) {
	livePlain, liveCompressed := writeArchiveRolloutZstd(t, t.TempDir(),
		"11111111-1111-7111-8111-111111111111", feedbackRolloutJSONL)
	damagedPlain, damagedCompressed := writeArchiveRolloutZstd(t, t.TempDir(),
		"22222222-2222-7222-8222-222222222222", feedbackRolloutJSONL)
	damaged, err := os.ReadFile(damagedCompressed)
	if err != nil {
		t.Fatalf("ReadFile(damaged) error = %v", err)
	}
	damaged = damaged[:10]
	if err := os.WriteFile(damagedCompressed, damaged, 0o600); err != nil {
		t.Fatalf("WriteFile(damaged) error = %v", err)
	}
	bufferedName := "rollout-2026-09-09T12-00-00-33333333-3333-7333-8333-333333333333.jsonl"
	buffered := []byte("{\"message\":\"second rollout\"}\n")

	attachments, paths := FeedbackRolloutArchive(t.TempDir(),
		[]FeedbackAttachmentPath{{Path: liveCompressed}, {Path: damagedCompressed}},
		[]FeedbackAttachment{{Filename: bufferedName, Buffer: buffered}},
		FeedbackMaxDecodedUploadBytes)
	if len(paths) != 0 || len(attachments) != 1 || attachments[0].Filename != FeedbackRolloutArchiveFilename {
		t.Fatalf("attachments = %+v paths = %+v", attachments, paths)
	}
	entries := readRolloutArchiveLikeRust(t, attachments[0].Buffer)
	want := map[string]string{
		filepath.Base(livePlain):             string(feedbackRolloutJSONL),
		filepath.Base(damagedPlain) + ".zst": string(damaged),
		bufferedName:                         string(buffered),
	}
	if len(entries) != len(want) {
		t.Fatalf("archive entries = %+v", entries)
	}
	for name, content := range want {
		if entries[name] != content {
			t.Fatalf("archive entry %q = %q, want %q", name, entries[name], content)
		}
	}
}

// TestFeedbackArchiveRolloutsSkipsInvalidAndDuplicateFilenamesLikeRust covers the
// Rust #50446 `append_rollout` guards: empty or traversal names, names carrying a
// separator or drive colon, and duplicate archive filenames are skipped.
func TestFeedbackArchiveRolloutsSkipsInvalidAndDuplicateFilenamesLikeRust(t *testing.T) {
	directory := t.TempDir()
	rollouts := []FeedbackAttachment{
		{Filename: "", Buffer: []byte("empty")},
		{Filename: ".", Buffer: []byte("dot")},
		{Filename: "..", Buffer: []byte("dotdot")},
		{Filename: "nested/rollout.jsonl", Buffer: []byte("slash")},
		{Filename: `nested\rollout.jsonl`, Buffer: []byte("backslash")},
		{Filename: "c:rollout.jsonl", Buffer: []byte("colon")},
	}
	if ok, err := FeedbackArchiveRollouts(filepath.Join(directory, "empty.tar.gz"), nil, rollouts, FeedbackMaxDecodedUploadBytes); err != nil || ok {
		t.Fatalf("archive of invalid names = ok %v err %v", ok, err)
	}

	valid := "rollout-2026-10-07T10-00-00-33333333-3333-7333-8333-333333333333.jsonl"
	duplicated := append(append([]FeedbackAttachment{}, rollouts...),
		FeedbackAttachment{Filename: valid, Buffer: []byte("first")},
		FeedbackAttachment{Filename: valid, Buffer: []byte("second")},
	)
	outputPath := filepath.Join(directory, "dedup.tar.gz")
	ok, err := FeedbackArchiveRollouts(outputPath, nil, duplicated, FeedbackMaxDecodedUploadBytes)
	if err != nil || !ok {
		t.Fatalf("archive = ok %v err %v", ok, err)
	}
	buffer, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	entries := readRolloutArchiveLikeRust(t, buffer)
	if len(entries) != 1 || entries[valid] != "first" {
		t.Fatalf("archive entries = %+v", entries)
	}
}

// TestFeedbackRolloutArchiveFallsBackToIndividualAttachmentsLikeRust covers the
// Rust #50446 `with_individual_fallback`: when the archive cannot be written the
// original attachments and paths are carried through.
func TestFeedbackRolloutArchiveFallsBackToIndividualAttachmentsLikeRust(t *testing.T) {
	directory := t.TempDir()
	fileRollout := "rollout-2026-10-07T10-00-00-44444444-4444-7444-8444-444444444444.jsonl"
	fileRolloutPath := filepath.Join(directory, fileRollout)
	if err := os.WriteFile(fileRolloutPath, []byte("file-backed rollout content padded to fifty bytes\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	buffered := FeedbackAttachment{Filename: "rollout-2026-10-07T11-00-00-55555555-5555-7555-8555-555555555555.jsonl", Buffer: []byte("buffered prefix content padded to fifty bytes.\n")}

	// The rollouts fit the limit individually, but the tar/gzip framing does not:
	// the bounded writer fails and Rust falls back to individual attachments.
	attachments, paths := FeedbackRolloutArchive(filepath.Join(directory, "archive"),
		[]FeedbackAttachmentPath{{Path: fileRolloutPath}},
		[]FeedbackAttachment{buffered},
		64)
	if len(attachments) != 1 || attachments[0].Filename != buffered.Filename {
		t.Fatalf("fallback attachments = %+v", attachments)
	}
	if len(paths) != 1 || paths[0].Path != fileRolloutPath {
		t.Fatalf("fallback paths = %+v", paths)
	}
}

// TestFeedbackRolloutArchiveSkipsUnarchivableRolloutsLikeRust covers the
// `Ok(None)` branch: when every rollout is skipped (invalid archive name or
// oversized buffer) Rust uploads neither an archive nor the individual
// rollouts, because `with_individual_fallback` only falls back on errors.
func TestFeedbackRolloutArchiveSkipsUnarchivableRolloutsLikeRust(t *testing.T) {
	directory := t.TempDir()
	invalidName := FeedbackAttachment{Filename: "..", Buffer: []byte("dotdot")}
	attachments, paths := FeedbackRolloutArchive(filepath.Join(directory, "invalid"),
		nil, []FeedbackAttachment{invalidName}, FeedbackMaxDecodedUploadBytes)
	if len(attachments) != 0 || len(paths) != 0 {
		t.Fatalf("invalid filename attachments = %+v paths = %+v, want nothing", attachments, paths)
	}

	oversized := FeedbackAttachment{
		Filename: "rollout-2026-10-07T11-00-00-77777777-7777-7777-8777-777777777777.jsonl",
		Buffer:   []byte("larger than the limit"),
	}
	attachments, paths = FeedbackRolloutArchive(filepath.Join(directory, "oversized"),
		nil, []FeedbackAttachment{oversized}, 1)
	if len(attachments) != 0 || len(paths) != 0 {
		t.Fatalf("oversized rollout attachments = %+v paths = %+v, want nothing", attachments, paths)
	}
}

// TestFeedbackPrepareUploadArchivesRolloutsLikeRust checks the wiring: a prepared
// upload carries a single `rollouts.tar.gz` attachment instead of the rollout
// paths, while diagnostics stay separate.
func TestFeedbackPrepareUploadArchivesRolloutsLikeRust(t *testing.T) {
	directory := t.TempDir()
	rolloutName := "rollout-2026-10-07T10-00-00-66666666-6666-7666-8666-666666666666.jsonl"
	rolloutPath := filepath.Join(directory, rolloutName)
	if err := os.WriteFile(rolloutPath, []byte("rollout body\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	diagnosticPath := filepath.Join(directory, "extra.log")
	if err := os.WriteFile(diagnosticPath, []byte("diagnostic body\n"), 0o600); err != nil {
		t.Fatalf("write diagnostic: %v", err)
	}
	snapshot := &FeedbackSnapshot{ThreadID: "thread-1", Logs: []byte("logs\n")}
	prepared := snapshot.PrepareUpload(&FeedbackUploadOptions{
		Classification:     "bug",
		IncludeLogs:        true,
		AttachmentPaths:    []FeedbackAttachmentPath{{Path: rolloutPath}, {Path: diagnosticPath}},
		FeedbackArchiveDir: directory,
	})
	var archive *FeedbackAttachment
	for index := range prepared.Attachments {
		if prepared.Attachments[index].Filename == FeedbackRolloutArchiveFilename {
			archive = &prepared.Attachments[index]
		}
	}
	if archive == nil {
		t.Fatalf("attachments = %+v", prepared.Attachments)
	}
	entries := readRolloutArchiveLikeRust(t, archive.Buffer)
	if len(entries) != 1 || strings.TrimSpace(entries[rolloutName]) != "rollout body" {
		t.Fatalf("archive entries = %+v", entries)
	}
	if len(prepared.AttachmentPaths) != 1 || prepared.AttachmentPaths[0].Path != diagnosticPath {
		t.Fatalf("attachment paths = %+v", prepared.AttachmentPaths)
	}
	if len(prepared.Attachments) != 2 || prepared.Attachments[0].Filename != "codex-logs.log" {
		t.Fatalf("attachments = %+v", prepared.Attachments)
	}
}
