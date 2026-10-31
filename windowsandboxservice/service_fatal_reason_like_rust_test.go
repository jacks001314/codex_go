//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"testing"

	"codex_go/sandbox/windowssandbox/provisionservice"
)

func TestFatalStopReasonSeparatesInterruptedRegistration(t *testing.T) {
	startPending := &ServiceState{}
	startPending.currentStatus.Store(ServiceStartPending)
	brokerFailure := &ServiceState{}
	brokerFailure.currentStatus.Store(ServiceRunning)

	cases := []struct {
		name  string
		state *ServiceState
		err   error
		want  provisionservice.ServiceStopReason
	}{
		{
			name:  "interrupted registration during startup",
			state: startPending,
			err:   fmt.Errorf("provision: %w", ErrRuntimeSetupInterrupted),
			want:  provisionservice.ServiceStopReasonRegistrationInterrupted,
		},
		{
			name:  "interrupted registration while running",
			state: brokerFailure,
			err:   ErrRuntimeSetupInterrupted,
			want:  provisionservice.ServiceStopReasonRegistrationInterrupted,
		},
		{
			name:  "failure before the service reported running",
			state: startPending,
			err:   errors.New("initialize the sandbox service"),
			want:  provisionservice.ServiceStopReasonStartupFailed,
		},
		{
			name:  "failure while the broker was serving",
			state: brokerFailure,
			err:   errors.New("run the sandbox provisioning broker"),
			want:  provisionservice.ServiceStopReasonBrokerFailed,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := fatalStopReason(testCase.state, testCase.err); got != testCase.want {
				t.Fatalf("fatalStopReason() = %d, want %d", got, testCase.want)
			}
		})
	}
}
