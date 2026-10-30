package model

// Request preparation bound to the credential revision it started with.
//
// Rust parity: codex-rs/model-provider/src/request_preparation.rs (#58121).
// Go resolves provider auth from the request's auth snapshot at send time and
// the app-server rebuilds auth-owned caches when the owner revision advances,
// so there is no cached two-phase client setup here. The helper is still the
// single place that encodes the retry/abort policy for callers that prepare a
// request before dispatching it.

import (
	"errors"

	"codex_go/auth"
)

// ErrAccountChangedDuringPreparation mirrors Rust's
// "account changed while preparing model request".
var ErrAccountChangedDuringPreparation = errors.New("account changed while preparing model request")

// PrepareProviderRequest prepares a request without dispatching it. When the
// request uses managed auth, preparation repeats after a credential refresh
// and fails if the account owner changes. A nil state callback models a
// provider without an auth manager.
func PrepareProviderRequest[T any](checkAuth bool, state func() *auth.AuthChangeState, prepare func(*auth.AuthChangeState) (T, error)) (T, error) {
	var zero T
	before := readAuthChangeState(state)
	for {
		prepared, err := prepare(before)
		if err != nil {
			return zero, err
		}
		if !checkAuth {
			return prepared, nil
		}
		after := readAuthChangeState(state)
		switch auth.CompareRequestAuthChange(before, after) {
		case auth.RequestAuthUnchanged:
			return prepared, nil
		case auth.RequestCredentialsChanged:
			before = after
		default:
			return zero, ErrAccountChangedDuringPreparation
		}
	}
}

func readAuthChangeState(state func() *auth.AuthChangeState) *auth.AuthChangeState {
	if state == nil {
		return nil
	}
	current := state()
	if current == nil {
		return nil
	}
	// Revisions are compared, never mutated, so snapshot by value.
	clone := *current
	return &clone
}
