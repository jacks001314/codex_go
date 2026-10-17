package codemode

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRemoteSessionNotFoundRetiresBindingLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		err        error
		wantRetire bool
	}{
		{name: "missing session", err: status.Error(codes.NotFound, "unknown session"), wantRetire: true},
		{name: "invalid argument", err: status.Error(codes.InvalidArgument, "bad request")},
		{name: "ordinary error", err: errors.New("transport failed")},
		{name: "success"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			connection := &remoteConnection{}
			session := &remoteSession{openedOn: connection}
			session.invalidateSessionOnNotFound(connection, testCase.err)
			if retired := session.openedOn == nil; retired != testCase.wantRetire {
				t.Fatalf("retired = %t, want %t", retired, testCase.wantRetire)
			}
		})
	}
}
