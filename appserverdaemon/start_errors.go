package appserverdaemon

import "errors"

// StartFailureReason classifies a failed foreground auto-start for telemetry
// (Rust #59058 StartError::failure_reason). The tag stays bounded so the
// original diagnostic can travel separately.
type StartFailureReason string

const (
	StartFailureInstall          StartFailureReason = "install"
	StartFailureSpawn            StartFailureReason = "spawn"
	StartFailureEarlyChildExit   StartFailureReason = "early_child_exit"
	StartFailureReadinessTimeout StartFailureReason = "readiness_timeout"
	StartFailureOther            StartFailureReason = "other"
)

// StartError keeps the original diagnostic and its telemetry classification.
type StartError struct {
	Reason StartFailureReason
	Err    error
}

func (e *StartError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *StartError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// wrapStartError attaches a classification unless one is already present.
func wrapStartError(reason StartFailureReason, err error) error {
	if err == nil {
		return nil
	}
	var existing *StartError
	if errors.As(err, &existing) {
		return err
	}
	return &StartError{Reason: reason, Err: err}
}

// StartFailureReasonOf reports the bounded failure reason for a failed
// auto-start; unclassified errors are "other".
func StartFailureReasonOf(err error) StartFailureReason {
	var startErr *StartError
	if errors.As(err, &startErr) && startErr.Reason != "" {
		return startErr.Reason
	}
	return StartFailureOther
}
