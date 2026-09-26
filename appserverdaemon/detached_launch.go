package appserverdaemon

import "errors"

// detachedLaunchRestrictedMessage mirrors Rust's `DetachedLaunchRestricted`
// Display: the automatic CLI startup may use its embedded server under this
// restriction, while explicit lifecycle operations must still fail with this
// guidance.
const detachedLaunchRestrictedMessage = "this Windows launcher prevents background processes from outliving it (for example, cargo run); build and run codex.exe directly to use the background server"

// DetachedLaunchRestrictedError classifies a launch that only fails when asked
// to leave the launcher's Windows job (Rust's `DetachedLaunchRestricted`). It is
// reported only when the launch succeeds with the breakaway flag removed, so an
// inaccessible executable keeps its ordinary failure.
type DetachedLaunchRestrictedError struct {
	err error
}

func (e *DetachedLaunchRestrictedError) Error() string {
	return detachedLaunchRestrictedMessage
}

func (e *DetachedLaunchRestrictedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// IsDetachedLaunchRestricted reports whether a launch failed only because the
// launcher forbids detaching a background process.
func IsDetachedLaunchRestricted(err error) bool {
	var target *DetachedLaunchRestrictedError
	return errors.As(err, &target)
}
