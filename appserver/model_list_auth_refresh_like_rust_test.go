package appserver

import (
	"encoding/json"
	"testing"
)

// Mirrors Rust #54169 "Reload the model catalog after account changes": the app
// server retained its startup model provider, so signing in or out could leave
// model/list using stale provider settings or requiring a restart after managed
// provider requirements changed. The catalog is invalidated on a successful
// login or logout, and the next model/list reloads the effective configuration
// before it answers.
func TestModelListReconcilesCatalogAfterAccountChangeLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})

	// A fresh router owes no reconciliation.
	if router.modelCatalogRefreshPending() {
		t.Fatal("a fresh router has no pending catalog reconciliation")
	}
	// The startup (or any) reconciliation consumes the current revision.
	router.refreshModelCatalogAfterAuthChange()
	if router.modelCatalogRefreshPending() {
		t.Fatal("a completed reconciliation must consume the current auth revision")
	}

	// A successful login or logout invalidates the catalog.
	router.noteAuthChanged()
	if !router.modelCatalogRefreshPending() {
		t.Fatal("an account change must leave the catalog reconciliation pending")
	}

	// The next model/list reconciles it instead of answering from the provider
	// the app server started with. (The catalog service itself is not the subject
	// here, so the response value is ignored.)
	_, _ = router.handleModelList(&Request{JSONRPC: "2.0", ID: IntID(1), Method: MethodModelList, Params: json.RawMessage(`{}`)})
	if router.modelCatalogRefreshPending() {
		t.Fatal("model/list must reconcile the catalog before it answers")
	}

	// Answering again does not repeat the reconciliation until the next change.
	_, _ = router.handleModelList(&Request{JSONRPC: "2.0", ID: IntID(2), Method: MethodModelList, Params: json.RawMessage(`{}`)})
	if router.modelCatalogRefreshPending() {
		t.Fatal("model/list must not requeue a reconciliation without an account change")
	}

	// A same-owner token refresh is still a credential change (authRevision
	// advances), so it also invalidates the catalog.
	router.noteAuthChanged()
	if !router.modelCatalogRefreshPending() {
		t.Fatal("a credential change must leave the catalog reconciliation pending")
	}
}
