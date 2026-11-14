//go:build windows

package windowsandboxservice

import (
	"golang.org/x/sys/windows"
	"strings"
	"testing"
)

func TestAppxEventIDsSortedDeduplicated(t *testing.T) {
	if got := formatAppxEventIDs([]uint16{404, 100, 404, 65535}); got != "100,404,65535" {
		t.Fatalf("IDs=%q", got)
	}
	if got := formatAppxEventIDs(nil); got != "" {
		t.Fatalf("empty IDs=%q", got)
	}
	if got := appxFailureEventIDs(windows.GUID{}); got != "" {
		t.Fatalf("zero activity=%q", got)
	}
}

func TestRegistrationFailureAddsOnlyAvailableAppxIDs(t *testing.T) {
	for _, mode := range []string{"failure", "success", "missing_activity", "zero_activity", "no_events"} {
		t.Run(mode, func(t *testing.T) {
			result := &registrationResult{status: asyncStatusError, details: &deploymentDetails{extendedError: 0x80073CF3, activityID: packageManagerIID}}
			switch mode {
			case "success":
				result.details.extendedError = 0
			case "missing_activity":
				result.details.activityIDErr = hresultError(0x80004003)
			case "zero_activity":
				result.details.activityID = windows.GUID{}
			}
			called := false
			err := result.checkWithEventIDs(nil, func(id windows.GUID) string {
				called = true
				if id != packageManagerIID {
					t.Fatalf("activity=%v", id)
				}
				if mode == "no_events" {
					return ""
				}
				return "100,404"
			})
			wantCall := mode == "failure" || mode == "no_events"
			if called != wantCall {
				t.Fatalf("lookup=%v want %v", called, wantCall)
			}
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "extended_error=0x80073CF3") {
				t.Fatalf("lost deployment error: %v", err)
			}
			if strings.Contains(err.Error(), "appx_event_ids=") != (mode == "failure") {
				t.Fatalf("event summary=%v", err)
			}
		})
	}
}
