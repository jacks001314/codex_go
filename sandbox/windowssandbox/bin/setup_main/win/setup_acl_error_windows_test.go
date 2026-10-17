package win

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRecordSetupACLFailurePreservesNativeErrorChain(t *testing.T) {
	const path = `project\.git`
	native := errors.New("SetSecurityInfo failed: 87")
	cause := fmt.Errorf("open ACL target (READ_CONTROL | WRITE_DAC): %w", native)
	cause = fmt.Errorf("grant write ACL: %w", cause)

	var refreshErrors []string
	var log bytes.Buffer
	if err := recordSetupACLFailure(&refreshErrors, setupACLRefreshGrant, path, cause, &log); err != nil {
		t.Fatalf("recordSetupACLFailure() error = %v", err)
	}

	wantDetail := path + ": grant write ACL: open ACL target (READ_CONTROL | WRITE_DAC): SetSecurityInfo failed: 87"
	if len(refreshErrors) != 1 || refreshErrors[0] != "write ACE failed on "+wantDetail {
		t.Fatalf("refresh errors = %#v", refreshErrors)
	}
	if got := log.String(); !strings.Contains(got, "write ACE grant failed on "+wantDetail) {
		t.Fatalf("log = %q", got)
	}
}

func TestRecordSetupACLFailureOperationMessages(t *testing.T) {
	cause := fmt.Errorf("open ACL target: %w", errors.New("Access is denied."))
	for _, testCase := range []struct {
		name        string
		operation   setupACLRefreshOperation
		wantSummary string
		wantLog     string
	}{
		{
			name:        "check",
			operation:   setupACLRefreshCheck,
			wantSummary: "write ACE check failed on project: open ACL target: Access is denied.",
			wantLog:     "write ACE check failed on project: open ACL target: Access is denied.; continuing",
		},
		{
			name:        "grant",
			operation:   setupACLRefreshGrant,
			wantSummary: "write ACE failed on project: open ACL target: Access is denied.",
			wantLog:     "write ACE grant failed on project: open ACL target: Access is denied.",
		},
		{
			name:        "deny",
			operation:   setupACLRefreshDeny,
			wantSummary: "deny ACE failed on project: open ACL target: Access is denied.",
			wantLog:     "deny ACE failed on project: open ACL target: Access is denied.",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var refreshErrors []string
			var log bytes.Buffer
			if err := recordSetupACLFailure(&refreshErrors, testCase.operation, "project", cause, &log); err != nil {
				t.Fatalf("recordSetupACLFailure() error = %v", err)
			}
			if len(refreshErrors) != 1 || refreshErrors[0] != testCase.wantSummary {
				t.Fatalf("refresh errors = %#v, want %q", refreshErrors, testCase.wantSummary)
			}
			if !strings.Contains(log.String(), testCase.wantLog) {
				t.Fatalf("log = %q, want %q", log.String(), testCase.wantLog)
			}
		})
	}
}

func TestRecordSetupACLFailureRetainsSummaryWhenLogWriteFails(t *testing.T) {
	refreshErrors := []string{"previous root failed"}
	cause := fmt.Errorf("grant write ACL: %w", errors.New("SetSecurityInfo failed: 87"))

	err := recordSetupACLFailure(&refreshErrors, setupACLRefreshGrant, `virtual-drive\project`, cause, failingSetupLogWriter{})
	if err == nil || !strings.Contains(err.Error(), "log unavailable") {
		t.Fatalf("recordSetupACLFailure() error = %v", err)
	}
	if len(refreshErrors) != 2 || refreshErrors[0] != "previous root failed" ||
		!strings.Contains(refreshErrors[1], "SetSecurityInfo failed: 87") {
		t.Fatalf("refresh errors = %#v", refreshErrors)
	}
}

type failingSetupLogWriter struct{}

func (failingSetupLogWriter) Write([]byte) (int, error) {
	return 0, errors.New("log unavailable")
}
