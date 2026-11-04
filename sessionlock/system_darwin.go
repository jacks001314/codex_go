//go:build darwin && cgo

package sessionlock

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>

// codex_session_locked mirrors Rust #59330 macos::requires_authentication: the
// screen flag alone is not enough, the per-session Unix password grace period
// must have elapsed.
static int codex_session_locked(long long now) {
	CFDictionaryRef session = CGSessionCopyCurrentDictionary();
	if (session == NULL) {
		return -1;
	}
	int locked = 0;
	CFBooleanRef flag = (CFBooleanRef)CFDictionaryGetValue(session, CFSTR("CGSSessionScreenIsLocked"));
	if (flag != NULL && CFGetTypeID(flag) == CFBooleanGetTypeID() && CFBooleanGetValue(flag)) {
		CFNumberRef stamp = (CFNumberRef)CFDictionaryGetValue(session, CFSTR("CGSSessionScreenLockedTime"));
		if (stamp != NULL && CFGetTypeID(stamp) == CFNumberGetTypeID()) {
			long long value = 0;
			if (CFNumberGetValue(stamp, kCFNumberLongLongType, &value) && value >= 1 && value <= now) {
				locked = 1;
			}
		}
	}
	CFRelease(session);
	return locked;
}
*/
import "C"

import (
	"errors"
	"time"
)

// SystemDetector returns the macOS login-session lock detector.
func SystemDetector() Detector { return darwinDetector{} }

type darwinDetector struct{}

func (darwinDetector) Locked() (bool, error) {
	switch C.codex_session_locked(C.longlong(time.Now().Unix())) {
	case 1:
		return true, nil
	case 0:
		return false, nil
	default:
		return false, errors.New("macOS login session unavailable")
	}
}
