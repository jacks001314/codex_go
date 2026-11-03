package app

// TUI diagnostics staging for remote feedback uploads.
//
// Rust #47889 (codex-rs/tui/src/app/feedback_upload.rs::fetch_feedback_upload)
// stages the TUI process's own log tail on the app-server host before
// `feedback/upload`, because the app-server reads `extraLogFiles` from its own
// filesystem rather than from the TUI's. The staging directory is removed as
// soon as the upload finishes, on success or failure.

import (
	"log/slog"
	"path"
	"strings"

	"github.com/google/uuid"

	"codex_go/appserver"
)

// remoteFeedbackClientLogFilename is Rust's staged attachment name.
const remoteFeedbackClientLogFilename = "client-logs.txt"

// remoteFeedbackLogOmittedReason is appended to the upload reason when staging
// fails, so the report still explains the missing attachment.
const remoteFeedbackLogOmittedReason = "\n\nTUI client logs were omitted because they could not be staged on the app-server."

// feedbackStagingFS is the app-server filesystem surface used to stage TUI
// diagnostics. It is the adapter the goal-file materializer already uses
// (appServerGoalFS over fs/createDirectory, fs/writeFile and fs/remove).
type feedbackStagingFS = appServerGoalFS

// stageRemoteFeedbackClientLogs writes the captured TUI log tail under
// `$CODEX_HOME/tmp/tui-feedback-<uuid>/client-logs.txt` on the app-server host
// and registers it with the upload. The returned cleanup removes the staging
// directory and must run after `feedback/upload` returns; nil means nothing was
// staged.
func stageRemoteFeedbackClientLogs(fs feedbackStagingFS, codexHome string, logs []byte, params *appserver.FeedbackUploadParams) (cleanup func()) {
	if params == nil || !params.IncludeLogs {
		return nil
	}
	home := strings.TrimSpace(codexHome)
	if home == "" {
		// Rust errors with "App server did not report $CODEX_HOME; cannot stage
		// feedback logs" and demotes it to a warning on the upload.
		omitRemoteFeedbackClientLogs(params, "app server did not report CODEX_HOME")
		return nil
	}
	directory := path.Join(home, "tmp", "tui-feedback-"+uuid.NewString())
	if err := fs.createDirectoryAll(directory); err != nil {
		omitRemoteFeedbackClientLogs(params, err.Error())
		return nil
	}
	stagedPath := path.Join(directory, remoteFeedbackClientLogFilename)
	tail := logs
	if len(tail) > TUIClientLogMaxBytes {
		tail = tail[len(tail)-TUIClientLogMaxBytes:]
	}
	if err := fs.writeFile(stagedPath, append([]byte(nil), tail...)); err != nil {
		omitRemoteFeedbackClientLogs(params, err.Error())
		if removeErr := fs.remove(directory, true); removeErr != nil {
			slog.Warn("failed to remove staged feedback logs", "path", directory, "error", removeErr)
		}
		return nil
	}
	params.ExtraLogFiles = append(append([]string(nil), params.ExtraLogFiles...), stagedPath)
	return func() {
		if err := fs.remove(directory, true); err != nil {
			slog.Warn("failed to remove staged feedback logs", "path", directory, "error", err)
		}
	}
}

func omitRemoteFeedbackClientLogs(params *appserver.FeedbackUploadParams, detail string) {
	slog.Warn("failed to stage TUI feedback logs", "error", detail)
	reason := ""
	if params.Reason != nil {
		reason = *params.Reason
	}
	reason += remoteFeedbackLogOmittedReason
	params.Reason = &reason
}
