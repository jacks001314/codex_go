package model

import (
	"errors"
	"testing"

	"codex_go/auth"
)

// Covers Rust #58121's preparation policy: managed credentials that change
// during setup repeat preparation, an owner change aborts, and an auth manager
// that appears or disappears during setup is treated as an owner change.
func TestPrepareProviderRequestLikeRust(t *testing.T) {
	state := &auth.AuthChangeState{}
	t.Run("credential change retries", func(t *testing.T) {
		attempts := 0
		prepared, err := PrepareProviderRequest(true, func() *auth.AuthChangeState { return state }, func(*auth.AuthChangeState) (string, error) {
			attempts++
			if attempts == 1 {
				state.Generation++
			}
			return "prepared", nil
		})
		if err != nil || prepared != "prepared" || attempts != 2 {
			t.Fatalf("prepared = %q, err = %v, attempts = %d", prepared, err, attempts)
		}
	})
	t.Run("owner change aborts", func(t *testing.T) {
		owner := auth.AuthChangeState{}
		_, err := PrepareProviderRequest(true, func() *auth.AuthChangeState { return &owner }, func(*auth.AuthChangeState) (string, error) {
			owner.OwnerGeneration++
			return "prepared", nil
		})
		if !errors.Is(err, ErrAccountChangedDuringPreparation) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("auth manager appears", func(t *testing.T) {
		var current *auth.AuthChangeState
		prepared := &auth.AuthChangeState{}
		_, err := PrepareProviderRequest(true, func() *auth.AuthChangeState { return current }, func(*auth.AuthChangeState) (string, error) {
			current = prepared
			return "prepared", nil
		})
		if !errors.Is(err, ErrAccountChangedDuringPreparation) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("unchecked auth keeps explicit bearer", func(t *testing.T) {
		attempts := 0
		prepared, err := PrepareProviderRequest(false, nil, func(*auth.AuthChangeState) (string, error) {
			attempts++
			return "bearer", nil
		})
		if err != nil || prepared != "bearer" || attempts != 1 {
			t.Fatalf("prepared = %q, err = %v, attempts = %d", prepared, err, attempts)
		}
	})
}
