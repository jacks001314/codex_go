package appserver

import (
	"strings"
	"sync"

	"codex_go/state"
)

const (
	logWriteWarningWithFeedback = "Codex couldn't save diagnostic logs to its local database. Use /feedback with logs included before closing Codex, or run `codex doctor` for diagnostics."
	logWriteWarningNoFeedback   = "Codex couldn't save diagnostic logs to its local database. Run `codex doctor` for diagnostics."
)

type logDBWriteWarningReporter struct {
	router  *RuntimeRouter
	message string
	once    sync.Once
}

func (r *logDBWriteWarningReporter) ReportLogWriteFailure(diagnostic string) {
	if r == nil || r.router == nil || strings.TrimSpace(diagnostic) == "" {
		return
	}
	r.once.Do(func() {
		r.router.notify(NotificationWarning, &WarningNotification{Message: r.message})
	})
}

func (r *RuntimeRouter) logDBHandler() *state.LogDBHandler {
	if r == nil {
		return nil
	}
	if r.services.LogDBHandler != nil {
		return r.services.LogDBHandler
	}
	if r.services.LogDBInstallation != nil {
		return r.services.LogDBInstallation.Handler
	}
	return nil
}

func (r *RuntimeRouter) logDBWarningFeedbackEnabled() bool {
	if r == nil || r.services.Config == nil {
		return true
	}
	response, err := r.services.Config.Read(nil)
	if err != nil || response == nil {
		return true
	}
	feedback, _ := response.Config["feedback"].(map[string]any)
	if feedback == nil {
		return true
	}
	enabled, ok := feedback["enabled"].(bool)
	if !ok {
		return true
	}
	return enabled
}

func (r *RuntimeRouter) installLogDBWriteWarningReporter() {
	if r == nil {
		return
	}
	r.logDBWriteWarningOnce.Do(func() {
		handler := r.logDBHandler()
		if handler == nil {
			return
		}
		message := logWriteWarningNoFeedback
		if r.logDBWarningFeedbackEnabled() {
			message = logWriteWarningWithFeedback
		}
		reporter := &logDBWriteWarningReporter{router: r, message: message}
		handler.SetFailureReporter(reporter)
		if handler.HasWriteFailure() {
			reporter.ReportLogWriteFailure(handler.LastWriteFailure())
		}
	})
}
