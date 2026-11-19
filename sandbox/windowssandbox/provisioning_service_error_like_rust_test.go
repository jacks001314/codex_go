package windowssandbox

import (
	"errors"
	"io"
	"testing"

	"codex_go/sandbox/windowssandbox/provisionservice"
)

// Rust #62192 attaches a distinct setup error code for a registered Core
// exchange that timed out or lost its pipe, and leaves any other error alone.
func TestRegisteredServiceSetupFailureCodesLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		want    SetupErrorCode
		wantErr bool
	}{
		{name: "timeout", err: &provisionservice.ServiceExchangeFailure{Kind: provisionservice.ServiceExchangeTimedOut, Cause: io.EOF}, want: SetupErrorRegisteredServiceTimedOut, wantErr: true},
		{name: "pipe closed", err: &provisionservice.ServiceExchangeFailure{Kind: provisionservice.ServiceExchangePipeClosed, Cause: io.EOF}, want: SetupErrorRegisteredServicePipeClosed, wantErr: true},
		{name: "other error keeps its identity", err: errors.New("boom"), wantErr: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := registeredServiceSetupFailure(test.err)
			var failure *SetupFailure
			if !test.wantErr {
				if errors.As(err, &failure) {
					t.Fatalf("error = %v, want no setup failure", err)
				}
				return
			}
			if !errors.As(err, &failure) {
				t.Fatalf("error = %v, want a SetupFailure", err)
			}
			if failure.Code != test.want {
				t.Fatalf("code = %q, want %q", failure.Code, test.want)
			}
			if err.Error() == "" || failure.Message != provisionservice.RegisteredServiceUnavailable {
				t.Fatalf("failure = %#v, want the registered refusal message", failure)
			}
		})
	}
}
