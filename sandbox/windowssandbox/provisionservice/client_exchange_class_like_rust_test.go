package provisionservice

import (
	"errors"
	"io"
	"os"
	"syscall"
	"testing"
	"time"
)

// Rust #62192: a registered Core exchange that times out or loses its pipe keeps
// the refusal text but gains a distinct setup error class; other errors and
// non-registered requests stay unchanged.
func TestRegisteredCoreExchangeClassificationLikeRust(t *testing.T) {
	cases := []struct {
		name           string
		err            error
		registeredCore bool
		wantKind       ServiceExchangeFailureKind
		wantErr        bool
	}{
		{name: "timeout", err: os.ErrDeadlineExceeded, registeredCore: true, wantKind: ServiceExchangeTimedOut, wantErr: true},
		{name: "syscall timeout", err: syscall.ETIMEDOUT, registeredCore: true, wantKind: ServiceExchangeTimedOut, wantErr: true},
		{name: "unexpected eof", err: io.ErrUnexpectedEOF, registeredCore: true, wantKind: ServiceExchangePipeClosed, wantErr: true},
		{name: "broken pipe", err: syscall.EPIPE, registeredCore: true, wantKind: ServiceExchangePipeClosed, wantErr: true},
		{name: "unregistered timeout falls back", err: os.ErrDeadlineExceeded, registeredCore: false},
		{name: "permission error keeps no code", err: os.ErrPermission, registeredCore: true, wantErr: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			conn := &scriptedConn{pid: 10, exchange: func(*Frame) (Response, error) { return Response{}, test.err }}
			client, _ := newTestClient(&scriptedTransport{connect: func(time.Time) (Conn, error) { return conn, nil }}, test.registeredCore)
			_, err := client.ProvisionWindowsSandbox("/home", Settings{}, Listeners{})
			var failure *ServiceExchangeFailure
			if !test.wantErr {
				if err != nil {
					t.Fatalf("error = %v, want the fallback path", err)
				}
				return
			}
			if err == nil {
				t.Fatal("error = nil, want a failure")
			}
			if test.wantKind == "" {
				if errors.As(err, &failure) {
					t.Fatalf("error = %v, want no exchange class", err)
				}
				return
			}
			if !errors.As(err, &failure) {
				t.Fatalf("error = %v, want a ServiceExchangeFailure", err)
			}
			if failure.Kind != test.wantKind {
				t.Fatalf("kind = %q, want %q", failure.Kind, test.wantKind)
			}
			if err.Error() != RegisteredServiceUnavailable {
				t.Fatalf("message = %q, want the registered refusal text", err.Error())
			}
			if !errors.Is(err, test.err) {
				t.Fatalf("error %v does not preserve cause %v", err, test.err)
			}
		})
	}
}
