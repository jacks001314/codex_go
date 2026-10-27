package appserver

// Go port of codex-rs/feedback/src/rollout_archive.rs (Rust #50446): selected
// rollouts are bundled into a bounded gzip tar at a caller-provided local path,
// diagnostics stay separate attachments, and any archiving failure falls back to
// the individual rollout attachments.
//
// Difference from Rust: Go's feedback pipeline stops at the prepared attachment
// list (there is no sentry envelope assembly or network upload in this port), so
// the archive is materialized while the upload is prepared instead of lazily
// while the envelope is written. The observable bundle - one `rollouts.tar.gz`
// attachment whose entries are the rollout filenames - is the same.

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"codex_go/rollout"
)

// FeedbackRolloutArchiveFilename is the archive attachment name (Rust
// `rollout_archive` writes `rollouts.tar.gz`).
const FeedbackRolloutArchiveFilename = "rollouts.tar.gz"

// FeedbackRolloutArchiveContentType is the content type of that archive.
const FeedbackRolloutArchiveContentType = "application/gzip"

// FeedbackMaxDecodedUploadBytes mirrors Rust `MAX_DECODED_UPLOAD_BYTES`
// (codex-rs/feedback/src/lib.rs): the bound applied to one decoded upload
// attachment, and to the archive as it is written.
const FeedbackMaxDecodedUploadBytes = 200 * 1024 * 1024

// errFeedbackRolloutArchiveTooLarge mirrors the Rust bounded writer's error.
var errFeedbackRolloutArchiveTooLarge = errors.New("rollout archive exceeds the size limit")

// FeedbackAttachmentIsRollout reports whether an attachment filename names a
// rollout, mirroring Rust `codex_rollout::rollout_id_from_path().is_some()`.
func FeedbackAttachmentIsRollout(filename string) bool {
	_, ok := rollout.ThreadIDFromFilename(filename)
	return ok
}

// FeedbackAttachmentPathFilename is the archive or attachment name for a
// path-backed attachment: the caller override wins, otherwise the file name.
func FeedbackAttachmentPathFilename(attachment FeedbackAttachmentPath) string {
	if attachment.AttachmentFilenameOverride != nil && strings.TrimSpace(*attachment.AttachmentFilenameOverride) != "" {
		return strings.TrimSpace(*attachment.AttachmentFilenameOverride)
	}
	return filepath.Base(attachment.Path)
}

// FeedbackReadAttachmentPath mirrors Rust `FeedbackAttachmentPath::read_attachment`:
// ok is false for a missing, non-regular or oversized source (the Rust reader
// logs and skips those), while a genuine read failure is returned as an error.
//
// Rust #49852: every skip is logged so operators can tell why an attachment was
// dropped; the messages and fields mirror the Rust tracing events.
//
// Rust #44175: a path that names a rollout is read through the plain-or-compressed
// resolver, decoded when only the `.jsonl.zst` representation exists, bounded by
// the decoded byte count, and attached under the canonical `.jsonl` filename
// (a caller override still wins). Non-rollout files keep the plain byte read.
func FeedbackReadAttachmentPath(attachment FeedbackAttachmentPath, maxBytes int64) (FeedbackAttachment, bool, error) {
	if maxBytes < 0 {
		maxBytes = 0
	}
	var buffer []byte
	rolloutPath := ""
	attachmentPath := attachment.Path
	rawRollout := false
	if _, isRollout := rollout.ThreadIDFromFilename(filepath.Base(attachment.Path)); isRollout {
		rolloutPath = rollout.PlainRolloutPath(attachment.Path)
		decoded, ok, err := rollout.ReadRolloutPrefix(attachment.Path, maxBytes+1)
		switch {
		case err == nil && ok:
			buffer = decoded
		case err == nil:
			slog.Error("feedback attachment skipped: rollout is missing or not a regular file")
			return FeedbackAttachment{}, false, nil
		case feedbackAttachmentKeepsRawRollout(attachment.Path, err):
			// Rust #55545: preserve a selected, undecodable archive for
			// diagnosis. A preferred plain rollout is never replaced by its
			// potentially stale compressed sibling.
			rolloutPath = ""
			rawRollout = true
			attachmentPath = rollout.PlainRolloutPath(attachment.Path) + ".zst"
			read, ok, readErr := feedbackReadRegularFile(attachmentPath, maxBytes)
			if readErr != nil {
				return FeedbackAttachment{}, false, readErr
			}
			if !ok {
				return FeedbackAttachment{}, false, nil
			}
			buffer = read
		default:
			return FeedbackAttachment{}, false, fmt.Errorf("read feedback attachment: %w", err)
		}
	} else {
		read, ok, err := feedbackReadRegularFile(attachmentPath, maxBytes)
		if err != nil {
			return FeedbackAttachment{}, false, err
		}
		if !ok {
			return FeedbackAttachment{}, false, nil
		}
		buffer = read
	}
	if int64(len(buffer)) > maxBytes {
		slog.Error("feedback attachment skipped: decoded size limit exceeded", "bytes_read", len(buffer), "max_bytes", maxBytes)
		return FeedbackAttachment{}, false, nil
	}
	filename := FeedbackAttachmentPathFilename(attachment)
	if rolloutPath != "" && (attachment.AttachmentFilenameOverride == nil || strings.TrimSpace(*attachment.AttachmentFilenameOverride) == "") {
		filename = filepath.Base(rolloutPath)
	}
	if rawRollout && !strings.HasSuffix(filename, ".zst") {
		filename += ".zst"
	}
	contentType := "application/octet-stream"
	if strings.EqualFold(filepath.Ext(filename), ".jsonl") {
		contentType = "text/plain"
	}
	return FeedbackAttachment{
		Filename:    filename,
		ContentType: contentType,
		Buffer:      buffer,
	}, true, nil
}

// feedbackAttachmentKeepsRawRollout reports whether a failed rollout decode
// should fall back to attaching the raw compressed archive (Rust #55545): the
// plain representation must be absent, and OS-level read failures keep failing
// instead of being mistaken for corrupt archives.
func feedbackAttachmentKeepsRawRollout(path string, err error) bool {
	if err == nil {
		return false
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return false
	}
	plain := rollout.PlainRolloutPath(path)
	if plain == "" {
		return false
	}
	if _, statErr := os.Stat(plain); statErr == nil {
		return false
	}
	if _, statErr := os.Stat(plain + ".zst"); statErr != nil {
		return false
	}
	return true
}

// feedbackReadRegularFile reads a whole regular file within maxBytes, mirroring
// Rust's metadata check plus one extra byte to detect growth after the check.
// ok is false for a missing, non-regular, or oversized source.
func feedbackReadRegularFile(path string, maxBytes int64) ([]byte, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Error("feedback attachment skipped: rollout is missing or not a regular file")
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("stat feedback attachment: %w", err)
	}
	if !info.Mode().IsRegular() {
		slog.Error("feedback attachment skipped: not a regular file")
		return nil, false, nil
	}
	if info.Size() > maxBytes {
		slog.Error("feedback attachment skipped: size limit exceeded", "bytes", info.Size(), "max_bytes", maxBytes)
		return nil, false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("read feedback attachment: %w", err)
	}
	read, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, false, fmt.Errorf("read feedback attachment: %w", readErr)
	}
	if closeErr != nil {
		return nil, false, fmt.Errorf("read feedback attachment: %w", closeErr)
	}
	return read, true, nil
}

// FeedbackArchiveRollouts mirrors Rust `rollout_archive::archive_rollouts`: the
// archive is written to outputPath and only published when it is complete. ok is
// false when no rollout could be added (Rust returns `Ok(None)`).
func FeedbackArchiveRollouts(outputPath string, rolloutPaths []FeedbackAttachmentPath, rollouts []FeedbackAttachment, maxBytes int64) (bool, error) {
	directory := filepath.Dir(outputPath)
	if directory != "" && directory != "." {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return false, fmt.Errorf("failed to create rollout archive directory: %w", err)
		}
	}
	file, err := os.CreateTemp(directory, "rollouts-*.tar.gz")
	if err != nil {
		return false, fmt.Errorf("create rollout archive: %w", err)
	}
	tempPath := file.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(tempPath)
		}
	}()

	bounded := &boundedArchiveWriter{inner: file, maxBytes: maxBytes}
	gzipWriter := gzip.NewWriter(bounded)
	archive := tar.NewWriter(gzipWriter)
	filenames := map[string]bool{}
	for _, rolloutPath := range rolloutPaths {
		attachment, ok, err := FeedbackReadAttachmentPath(rolloutPath, maxBytes)
		if err != nil {
			continue // Rust logs the read failure and skips the attachment.
		}
		if !ok {
			continue
		}
		if err := appendRolloutToArchive(archive, filenames, attachment, maxBytes); err != nil {
			return false, err
		}
	}
	for _, rollout := range rollouts {
		if err := appendRolloutToArchive(archive, filenames, rollout, maxBytes); err != nil {
			return false, err
		}
	}
	if len(filenames) == 0 {
		_ = gzipWriter.Close()
		_ = archive.Close()
		_ = file.Close()
		return false, nil // Rust `Ok(None)`: nothing to publish.
	}
	if err := archive.Close(); err != nil {
		_ = file.Close()
		return false, fmt.Errorf("failed to finish rollout archive: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		_ = file.Close()
		return false, fmt.Errorf("failed to finish rollout archive: %w", err)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("failed to finish rollout archive: %w", err)
	}
	if err := os.Rename(tempPath, outputPath); err != nil {
		return false, fmt.Errorf("failed to publish rollout archive: %w", err)
	}
	published = true
	return true, nil
}

func appendRolloutToArchive(archive *tar.Writer, filenames map[string]bool, rollout FeedbackAttachment, maxBytes int64) error {
	if int64(len(rollout.Buffer)) > maxBytes {
		return nil // Rust skips oversized rollouts.
	}
	if !validRolloutArchiveFilename(rollout.Filename, filenames) {
		return nil // Rust skips invalid or duplicate archive filenames.
	}
	header := &tar.Header{
		Name:     rollout.Filename,
		Mode:     0o600,
		Size:     int64(len(rollout.Buffer)),
		ModTime:  epochTime(),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatGNU,
	}
	if err := archive.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to write rollout archive: %w", err)
	}
	if _, err := archive.Write(rollout.Buffer); err != nil {
		return fmt.Errorf("failed to write rollout archive: %w", err)
	}
	filenames[rollout.Filename] = true
	return nil
}

func validRolloutArchiveFilename(filename string, filenames map[string]bool) bool {
	if filename == "" || filename == "." || filename == ".." {
		return false
	}
	if strings.ContainsAny(filename, `/\:`) {
		return false
	}
	return !filenames[filename]
}

// boundedArchiveWriter mirrors the Rust BoundedWriter: the archive never grows
// past maxBytes.
type boundedArchiveWriter struct {
	inner    io.Writer
	written  int64
	maxBytes int64
}

func (w *boundedArchiveWriter) Write(buffer []byte) (int, error) {
	if int64(len(buffer)) > w.maxBytes-w.written {
		return 0, errFeedbackRolloutArchiveTooLarge
	}
	written, err := w.inner.Write(buffer)
	w.written += int64(written)
	return written, err
}

// FeedbackRolloutArchive mirrors the rollout branch of Rust
// `FeedbackSnapshot::feedback_attachments`: rollouts are bundled into
// `rollouts.tar.gz`; when archiving fails (or nothing can be archived) the
// individual attachments are carried through unchanged.
//
// archiveDir is where the archive is written; an empty value uses the system
// temporary directory.
func FeedbackRolloutArchive(archiveDir string, rolloutPaths []FeedbackAttachmentPath, rollouts []FeedbackAttachment, maxBytes int64) ([]FeedbackAttachment, []FeedbackAttachmentPath) {
	if len(rolloutPaths) == 0 && len(rollouts) == 0 {
		return nil, nil
	}
	if archiveDir == "" {
		archiveDir = os.TempDir()
	}
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		// Rust `archive_rollouts` creates the parent directory first; when that
		// fails the individual attachments are the fallback.
		return cloneAttachments(rollouts), cloneAttachmentPaths(rolloutPaths)
	}
	directory, err := os.MkdirTemp(archiveDir, "codex-feedback-rollouts-")
	if err == nil {
		outputPath := filepath.Join(directory, FeedbackRolloutArchiveFilename)
		ok, archiveErr := FeedbackArchiveRollouts(outputPath, rolloutPaths, rollouts, maxBytes)
		if archiveErr == nil && ok {
			attachment, read, readErr := FeedbackReadAttachmentPath(FeedbackAttachmentPath{Path: outputPath}, maxBytes)
			if readErr == nil && read && int64(len(attachment.Buffer)) <= maxBytes {
				attachment.ContentType = FeedbackRolloutArchiveContentType
				// Rust keeps the archive in a temp dir until the envelope is written;
				// the bytes are already in memory here, so the directory is released.
				_ = os.RemoveAll(directory)
				return []FeedbackAttachment{attachment}, nil
			}
		}
		_ = os.RemoveAll(directory)
	}
	// Rust `with_individual_fallback`: buffered rollouts first, then the
	// path-backed rollouts are left for the uploader to read.
	return cloneAttachments(rollouts), cloneAttachmentPaths(rolloutPaths)
}

// feedbackPartitionRollouts splits buffered attachments and path-backed
// attachments into rollouts and diagnostics, mirroring the partition performed
// by Rust `feedback_attachments` (Rust #50446).
func feedbackPartitionRollouts(extra []FeedbackAttachment, paths []FeedbackAttachmentPath) ([]FeedbackAttachment, []FeedbackAttachment, []FeedbackAttachmentPath, []FeedbackAttachmentPath) {
	var rolloutAttachments, diagnosticAttachments []FeedbackAttachment
	for _, attachment := range extra {
		if FeedbackAttachmentIsRollout(attachment.Filename) {
			rolloutAttachments = append(rolloutAttachments, attachment)
			continue
		}
		diagnosticAttachments = append(diagnosticAttachments, attachment)
	}
	var rolloutPaths, diagnosticPaths []FeedbackAttachmentPath
	for _, attachmentPath := range paths {
		if FeedbackAttachmentIsRollout(path.Base(attachmentPath.Path)) {
			rolloutPaths = append(rolloutPaths, attachmentPath)
			continue
		}
		diagnosticPaths = append(diagnosticPaths, attachmentPath)
	}
	return rolloutAttachments, diagnosticAttachments, rolloutPaths, diagnosticPaths
}

// epochTime matches the Rust archive's `set_mtime(0)`.
func epochTime() time.Time { return time.Unix(0, 0).UTC() }
