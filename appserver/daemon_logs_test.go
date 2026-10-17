package appserver

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDaemonLogAttachmentsKeepCurrentAndPreviousTailsLikeRust(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "app-server-daemon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	large := bytes.Repeat([]byte("d"), daemonFeedbackLogTailBytes+1024)
	copy(large[len(large)-4:], "tail")
	if err := os.WriteFile(filepath.Join(dir, "daemon-updater.stderr.log"), large, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daemon-updater.stderr.log.previous"), []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app-server-updater.stderr.log"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}

	attachments := DaemonLogAttachments(home)
	if len(attachments) != 2 {
		t.Fatalf("attachments = %#v, want current and previous", attachments)
	}
	if attachments[0].Filename != "daemon-updater.stderr.log" ||
		len(attachments[0].Buffer) != daemonFeedbackLogTailBytes ||
		!bytes.HasSuffix(attachments[0].Buffer, []byte("tail")) {
		t.Fatalf("current attachment = %s / %d bytes", attachments[0].Filename, len(attachments[0].Buffer))
	}
	if attachments[1].Filename != "daemon-updater.stderr.log.previous" ||
		string(attachments[1].Buffer) != "previous" {
		t.Fatalf("previous attachment = %#v", attachments[1])
	}
}

func TestDaemonLogAttachmentsFallBackToLegacyNameLikeRust(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "app-server-daemon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app-server-updater.stderr.log"), []byte("legacy current"), 0o600); err != nil {
		t.Fatal(err)
	}
	attachments := DaemonLogAttachments(home)
	if len(attachments) != 1 || attachments[0].Filename != "app-server-updater.stderr.log" ||
		string(attachments[0].Buffer) != "legacy current" {
		t.Fatalf("legacy attachments = %#v", attachments)
	}
}
