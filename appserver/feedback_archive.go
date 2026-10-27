package appserver

// Go port of codex-rs/feedback/src/archive.rs (Rust #55940): a local feedback
// export is an atomically published ZIP whose uncompressed contents and
// attachment count are bounded, whose entry names cannot escape
// `attachments/<index>/`, and whose publication never replaces an existing
// file. Any failure leaves no published archive.

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// FeedbackArchiveMaxBytes mirrors Rust `Limits::default().max_bytes`: the
	// total uncompressed entry contents, including report and manifest
	// serialization.
	FeedbackArchiveMaxBytes = 200 * 1024 * 1024
	// FeedbackArchiveMaxAttachments mirrors Rust's attachment count bound.
	FeedbackArchiveMaxAttachments = 128
)

// FeedbackArchiveLimits bounds one local export.
type FeedbackArchiveLimits struct {
	MaxBytes       int
	MaxAttachments int
}

// DefaultFeedbackArchiveLimits returns the Rust defaults: 200 MiB and 128
// attachments.
func DefaultFeedbackArchiveLimits() FeedbackArchiveLimits {
	return FeedbackArchiveLimits{
		MaxBytes:       FeedbackArchiveMaxBytes,
		MaxAttachments: FeedbackArchiveMaxAttachments,
	}
}

// feedbackArchiveEntry is one selected attachment: either already in memory or
// a host path that must still be read strictly.
type feedbackArchiveEntry struct {
	attachment *FeedbackAttachment
	path       *FeedbackAttachmentPath
}

type feedbackArchiveManifestEntry struct {
	Entry       string  `json:"entry"`
	Filename    string  `json:"filename"`
	ContentType *string `json:"contentType"`
}

// feedbackArchiveBudget counts uncompressed entry bytes across report,
// attachments, and manifest.
type feedbackArchiveBudget struct {
	remaining int
}

func (b *feedbackArchiveBudget) write(writer io.Writer, data []byte) error {
	if len(data) > b.remaining {
		return errors.New("feedback archive exceeds the uncompressed size limit")
	}
	b.remaining -= len(data)
	_, err := writer.Write(data)
	return err
}

// SaveFeedbackArchive writes report.json, one entry per selected attachment,
// and manifest.json to path. It mirrors Rust `archive::save`.
func SaveFeedbackArchive(path string, report []byte, entries []feedbackArchiveEntry, limits FeedbackArchiveLimits) error {
	if !filepath.IsAbs(path) {
		return errors.New("feedback save path must be absolute")
	}
	if _, err := os.Stat(path); err == nil {
		return errors.New("feedback save path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to inspect feedback save path: %w", err)
	}
	parent := filepath.Dir(path)
	temporary, err := os.CreateTemp(parent, ".feedback-*.zip")
	if err != nil {
		return fmt.Errorf("failed to create feedback archive: %w", err)
	}
	temporaryName := temporary.Name()
	published := false
	defer func() {
		_ = temporary.Close()
		if !published {
			_ = os.Remove(temporaryName)
		}
	}()

	writer := zip.NewWriter(temporary)
	budget := &feedbackArchiveBudget{remaining: limits.MaxBytes}
	options := &zip.FileHeader{Name: "report.json", Method: zip.Deflate}
	header, err := writer.CreateHeader(options)
	if err != nil {
		return err
	}
	if err := budget.write(header, report); err != nil {
		return err
	}
	manifest := make([]feedbackArchiveManifestEntry, 0, len(entries))
	for index, entry := range entries {
		// Inspect metadata before reading the next file or compressing its
		// contents.
		if index >= limits.MaxAttachments {
			return errors.New("feedback archive exceeds the attachment count limit")
		}
		attachment, err := feedbackArchiveRead(entry, budget.remaining)
		if err != nil {
			return err
		}
		basename := feedbackArchiveBasename(attachment.Filename)
		// Numbered directories retain duplicate names without unsafe ZIP paths.
		entryName := fmt.Sprintf("attachments/%04d/%s", index, basename)
		header, err := writer.CreateHeader(&zip.FileHeader{Name: entryName, Method: zip.Deflate})
		if err != nil {
			return err
		}
		if err := budget.write(header, attachment.Buffer); err != nil {
			return err
		}
		var contentType *string
		if attachment.ContentType != "" {
			value := attachment.ContentType
			contentType = &value
		}
		manifest = append(manifest, feedbackArchiveManifestEntry{
			Entry:       entryName,
			Filename:    attachment.Filename,
			ContentType: contentType,
		})
	}
	header, err = writer.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Deflate})
	if err != nil {
		return err
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := budget.write(header, manifestJSON); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	// Same-directory publication is atomic and must not replace an existing
	// file: a hard link fails when the destination appears or exists, and the
	// exclusive-create fallback keeps the guarantee on filesystems without
	// hard links.
	if err := os.Link(temporaryName, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("feedback save path already exists")
		}
		if publishErr := feedbackArchivePublishExclusive(temporaryName, path); publishErr != nil {
			return publishErr
		}
	}
	published = true
	// The published file is a separate directory entry; the temporary link
	// source is no longer needed.
	_ = os.Remove(temporaryName)
	return nil
}

func feedbackArchiveRead(entry feedbackArchiveEntry, remaining int) (*FeedbackAttachment, error) {
	if entry.attachment != nil {
		return entry.attachment, nil
	}
	if entry.path == nil {
		return nil, errors.New("feedback archive entry is empty")
	}
	path := entry.path.Path
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(remaining) {
		return nil, fmt.Errorf("selected attachment %s is missing, nonregular, or exceeds the remaining decoded size limit", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read selected attachment %s: %w", path, err)
	}
	filename := feedbackArchiveBasename(path)
	if entry.path.AttachmentFilenameOverride != nil && strings.TrimSpace(*entry.path.AttachmentFilenameOverride) != "" {
		filename = *entry.path.AttachmentFilenameOverride
	}
	return &FeedbackAttachment{Filename: filename, Buffer: data}, nil
}

// feedbackArchiveBasename mirrors Rust's `rsplit(['/', '\\'])` basename rule so
// a Windows-style path stays a safe leaf even on Unix hosts.
func feedbackArchiveBasename(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return "attachment"
	}
	basename := parts[len(parts)-1]
	if basename == "" || basename == "." || basename == ".." {
		return "attachment"
	}
	return basename
}

// feedbackArchivePublishExclusive copies the temporary archive to path with
// exclusive creation, used when the filesystem cannot hard-link.
func feedbackArchivePublishExclusive(temporaryName string, path string) error {
	source, err := os.Open(temporaryName)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("feedback save path already exists")
		}
		return err
	}
	copied := false
	defer func() {
		_ = target.Close()
		if !copied {
			_ = os.Remove(path)
		}
	}()
	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	if err := target.Sync(); err != nil {
		return err
	}
	if err := target.Close(); err != nil {
		return err
	}
	copied = true
	return nil
}
