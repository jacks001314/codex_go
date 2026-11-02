package app

import (
	"errors"
	"path"
	"strings"
	"testing"

	"codex_go/appserver"
)

type recordingFeedbackFS struct {
	created     []string
	written     map[string][]byte
	removed     []string
	failCreaate bool
	failWrite   bool
}

func newRecordingFeedbackFS() *recordingFeedbackFS {
	return &recordingFeedbackFS{written: map[string][]byte{}}
}

func (f *recordingFeedbackFS) functions() feedbackStagingFS {
	return feedbackStagingFS{
		createDirectoryAll: func(dir string) error {
			if f.failCreaate {
				return errors.New("createDirectory failed")
			}
			f.created = append(f.created, dir)
			return nil
		},
		writeFile: func(file string, data []byte) error {
			if f.failWrite {
				return errors.New("writeFile failed")
			}
			f.written[file] = append([]byte(nil), data...)
			return nil
		},
		remove: func(target string, recursive bool) error {
			f.removed = append(f.removed, target)
			return nil
		},
	}
}

func TestStageRemoteFeedbackClientLogsStagesAndCleansUp(t *testing.T) {
	fs := newRecordingFeedbackFS()
	params := appserver.FeedbackUploadParams{IncludeLogs: true}
	logs := []byte("client log line\n")

	cleanup := stageRemoteFeedbackClientLogs(fs.functions(), "/remote/home", logs, &params)
	if cleanup == nil {
		t.Fatal("expected a cleanup for staged logs")
	}
	if len(fs.created) != 1 || !strings.HasPrefix(fs.created[0], "/remote/home/tmp/tui-feedback-") {
		t.Fatalf("staging directory = %#v", fs.created)
	}
	staged := path.Join(fs.created[0], remoteFeedbackClientLogFilename)
	if got := string(fs.written[staged]); got != string(logs) {
		t.Fatalf("staged bytes = %q", got)
	}
	if len(params.ExtraLogFiles) != 1 || params.ExtraLogFiles[0] != staged {
		t.Fatalf("extra log files = %#v", params.ExtraLogFiles)
	}
	cleanup()
	if len(fs.removed) != 1 || fs.removed[0] != fs.created[0] {
		t.Fatalf("cleanup removals = %#v", fs.removed)
	}
}

func TestStageRemoteFeedbackClientLogsSkipsWithoutLogConsent(t *testing.T) {
	fs := newRecordingFeedbackFS()
	params := appserver.FeedbackUploadParams{IncludeLogs: false}
	if cleanup := stageRemoteFeedbackClientLogs(fs.functions(), "/remote/home", []byte("x"), &params); cleanup != nil {
		t.Fatal("no staging expected without log consent")
	}
	if len(fs.created) != 0 || params.ExtraLogFiles != nil || params.Reason != nil {
		t.Fatalf("staged without consent: created=%#v params=%#v", fs.created, params)
	}
}

func TestStageRemoteFeedbackClientLogsOmitsWhenCodexHomeMissing(t *testing.T) {
	fs := newRecordingFeedbackFS()
	reason := "existing reason"
	params := appserver.FeedbackUploadParams{IncludeLogs: true, Reason: &reason}
	if cleanup := stageRemoteFeedbackClientLogs(fs.functions(), "  ", []byte("x"), &params); cleanup != nil {
		t.Fatal("no staging expected without CODEX_HOME")
	}
	if params.Reason == nil || !strings.HasPrefix(*params.Reason, "existing reason") ||
		!strings.Contains(*params.Reason, "TUI client logs were omitted") {
		t.Fatalf("reason = %#v", params.Reason)
	}
	if params.ExtraLogFiles != nil {
		t.Fatalf("extra log files = %#v", params.ExtraLogFiles)
	}
}

func TestStageRemoteFeedbackClientLogsRemovesDirectoryOnWriteFailure(t *testing.T) {
	fs := newRecordingFeedbackFS()
	fs.failWrite = true
	params := appserver.FeedbackUploadParams{IncludeLogs: true}
	if cleanup := stageRemoteFeedbackClientLogs(fs.functions(), "/remote/home", []byte("x"), &params); cleanup != nil {
		t.Fatal("no cleanup expected when staging failed")
	}
	if len(fs.removed) != 1 || params.Reason == nil {
		t.Fatalf("removed=%#v reason=%#v", fs.removed, params.Reason)
	}
}

func TestStageRemoteFeedbackClientLogsBoundsTail(t *testing.T) {
	fs := newRecordingFeedbackFS()
	params := appserver.FeedbackUploadParams{IncludeLogs: true}
	logs := make([]byte, TUIClientLogMaxBytes+64)
	for i := range logs {
		logs[i] = byte('a' + i%26)
	}
	cleanup := stageRemoteFeedbackClientLogs(fs.functions(), "/remote/home", logs, &params)
	if cleanup == nil {
		t.Fatal("expected staging")
	}
	staged := path.Join(fs.created[0], remoteFeedbackClientLogFilename)
	if got := len(fs.written[staged]); got != TUIClientLogMaxBytes {
		t.Fatalf("staged size = %d, want %d", got, TUIClientLogMaxBytes)
	}
	if string(fs.written[staged]) != string(logs[len(logs)-TUIClientLogMaxBytes:]) {
		t.Fatal("staged bytes are not the newest window")
	}
}
