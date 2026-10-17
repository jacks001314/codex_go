package appserver

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

const daemonFeedbackLogTailBytes = 256 * 1024

var daemonFeedbackLogNames = []string{
	"daemon-updater.stderr.log",
	"app-server-updater.stderr.log",
}

// DaemonLogAttachments returns the bounded current and previous updater log
// tails for a feedback report. New filenames are preferred; the legacy name is
// a per-suffix fallback rather than an additional attachment. Missing, empty,
// symlinked, and nonregular files are skipped.
func DaemonLogAttachments(codexHome string) []FeedbackAttachment {
	codexHome = filepath.Clean(codexHome)
	if codexHome == "" || codexHome == "." {
		return nil
	}
	directory := filepath.Join(codexHome, "app-server-daemon")
	attachments := make([]FeedbackAttachment, 0, 2)
	for _, suffix := range []string{"", ".previous"} {
		for _, name := range daemonFeedbackLogNames {
			filename := name + suffix
			data := readDaemonLogTail(filepath.Join(directory, filename))
			if len(data) == 0 {
				continue
			}
			attachments = append(attachments, FeedbackAttachment{
				Filename:    filename,
				ContentType: "text/plain",
				Buffer:      data,
			})
			break
		}
	}
	return attachments
}

func readDaemonLogTail(path string) []byte {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		slog.Warn("failed to inspect daemon feedback log", "path", path, "error", err)
		return nil
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("failed to open daemon feedback log", "path", path, "error", err)
		}
		return nil
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() == 0 {
		return nil
	}
	offset := opened.Size() - daemonFeedbackLogTailBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		slog.Warn("failed to seek daemon feedback log", "path", path, "error", err)
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(file, daemonFeedbackLogTailBytes))
	if err != nil {
		slog.Warn("failed to read daemon feedback log", "path", path, "error", err)
		return nil
	}
	return data
}
