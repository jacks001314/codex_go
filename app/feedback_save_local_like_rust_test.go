package app

import (
	"archive/zip"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/appserver"
)

// TestInteractiveFeedbackSaveWritesLocalZIPLikeRust covers Rust #60542: the
// report editor local save produces a diagnostic ZIP under the app server Codex
// home, with and without log consent, and never leaves the machine.
func TestInteractiveFeedbackSaveWritesLocalZIPLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("GCODE_HOME", "")
	save := interactiveFeedbackSaveHandler()
	for _, includeLogs := range []bool{true, false} {
		reason := "the build broke"
		response, err := save(appserver.FeedbackSaveParams{
			FeedbackUploadParams: appserver.FeedbackUploadParams{
				Classification: "bug",
				Reason:         &reason,
				IncludeLogs:    includeLogs,
			},
		})
		if err != nil {
			t.Fatalf("save(includeLogs=%v) error = %v", includeLogs, err)
		}
		if !strings.HasPrefix(response.SavedPath, filepath.Join(home, "feedback")+string(filepath.Separator)) {
			t.Fatalf("save path = %q, want it under the feedback directory", response.SavedPath)
		}
		base := filepath.Base(response.SavedPath)
		if !strings.HasPrefix(base, "codex-diagnostics-") || !strings.HasSuffix(base, ".zip") {
			t.Fatalf("save name = %q, want the diagnostic archive name", base)
		}
		reader, err := zip.OpenReader(response.SavedPath)
		if err != nil {
			t.Fatalf("saved archive is not a readable ZIP: %v", err)
		}
		if len(reader.File) == 0 {
			reader.Close()
			t.Fatalf("saved archive %q is empty", response.SavedPath)
		}
		names := make([]string, 0, len(reader.File))
		for _, file := range reader.File {
			names = append(names, file.Name)
		}
		reader.Close()
		joined := strings.Join(names, ",")
		if !strings.Contains(joined, "report.json") || !strings.Contains(joined, "manifest.json") {
			t.Fatalf("archive entries = %v, want the feedback event", names)
		}
		if includeLogs && !strings.Contains(joined, "codex-logs.log") {
			t.Fatalf("archive with consent = %v, want the log attachment", names)
		}
		if !includeLogs && strings.Contains(joined, "codex-logs.log") {
			t.Fatalf("archive without consent = %v, want no log attachment", names)
		}
	}
}

// TestInteractiveFeedbackSaveRejectsIncompleteParams keeps an invalid report
// from writing a half-formed archive.
func TestInteractiveFeedbackSaveRejectsIncompleteParams(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GCODE_HOME", "")
	if _, err := interactiveFeedbackSaveHandler()(appserver.FeedbackSaveParams{}); err == nil {
		t.Fatal("save without a classification succeeded")
	}
}
