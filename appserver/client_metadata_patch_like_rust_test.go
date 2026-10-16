package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/turn"
)

// Rust #54053 (`app-server/tests/suite/v2/client_metadata.rs`): the
// `responsesapiClientMetadataPatch` field adds or overwrites individual entries
// while retaining the others, so a turn keeps the metadata it already reported.
// The behaviour lives in `turn.ApplyResponsesAPIClientMetadataUpdate`; the test
// lives here because `./turn/`'s test binary does not compile at this baseline
// (`turn/environment_file_systems_like_rust_test.go:81` is missing
// `tool.EnvironmentFileSystem.WriteFileStream`, a lane-B surface).
func TestApplyResponsesAPIClientMetadataUpdatePatchRetainsKeysLikeRust(t *testing.T) {
	current := map[string]string{"source": "client", "kept": "yes"}
	got := turn.ApplyResponsesAPIClientMetadataUpdate(current, turn.ResponsesAPIClientMetadataUpdate{
		Patch: map[string]string{"source": "patched", "added": "1"},
	})
	want := map[string]string{"source": "patched", "kept": "yes", "added": "1"}
	if len(got) != len(want) {
		t.Fatalf("metadata = %#v, want %#v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("metadata = %#v, want %#v", got, want)
		}
	}
}

// Rust #54053: the patch applies after the replacement when both are supplied,
// so the patch wins on its own keys and the replacement's other entries replace
// the current map.
func TestApplyResponsesAPIClientMetadataUpdateOrderLikeRust(t *testing.T) {
	current := map[string]string{"stale": "drop-me"}
	got := turn.ApplyResponsesAPIClientMetadataUpdate(current, turn.ResponsesAPIClientMetadataUpdate{
		Replacement:    map[string]string{"source": "replacement"},
		HasReplacement: true,
		Patch:          map[string]string{"source": "patched"},
	})
	if len(got) != 1 || got["source"] != "patched" {
		t.Fatalf("metadata = %#v, want the replacement replaced then patched", got)
	}
}

// Rust #54053: the existing field keeps replacement semantics, including
// clearing every unprotected entry with an empty map.
func TestApplyResponsesAPIClientMetadataUpdateEmptyReplacementClearsLikeRust(t *testing.T) {
	current := map[string]string{"source": "client", "kept": "yes"}
	got := turn.ApplyResponsesAPIClientMetadataUpdate(current, turn.ResponsesAPIClientMetadataUpdate{
		Replacement:    map[string]string{},
		HasReplacement: true,
	})
	if len(got) != 0 {
		t.Fatalf("metadata = %#v, want every unprotected entry cleared", got)
	}
}

// Rust #54053: an update carrying neither part leaves the current metadata
// untouched, and reserved keys never pass the client-metadata filter.
func TestApplyResponsesAPIClientMetadataUpdateEmptyAndReservedLikeRust(t *testing.T) {
	current := map[string]string{"source": "client"}
	if got := turn.ApplyResponsesAPIClientMetadataUpdate(current, turn.ResponsesAPIClientMetadataUpdate{}); len(got) != 1 || got["source"] != "client" {
		t.Fatalf("metadata = %#v, want the current map untouched", got)
	}
	got := turn.ApplyResponsesAPIClientMetadataUpdate(current, turn.ResponsesAPIClientMetadataUpdate{
		Patch: map[string]string{"thread_id": "forged", "source": "client-2"},
	})
	if _, ok := got["thread_id"]; ok {
		t.Fatalf("metadata = %#v, want the reserved key dropped", got)
	}
	if got["source"] != "client-2" {
		t.Fatalf("metadata = %#v, want the unprotected patch entry applied", got)
	}
}

// Rust #54053: `turn/start` and `turn/steer` accept the experimental
// `responsesapiClientMetadataPatch` field alongside `responsesapiClientMetadata`.
func TestTurnParamsDecodeClientMetadataPatchLikeRust(t *testing.T) {
	var start turn.TurnStartParams
	if err := json.Unmarshal([]byte(`{"threadId":"t","responsesapiClientMetadata":{"source":"replacement"},"responsesapiClientMetadataPatch":{"patched":"1"}}`), &start); err != nil {
		t.Fatalf("turn/start unmarshal: %v", err)
	}
	if start.ResponsesAPIMetadata["source"] != "replacement" || start.ResponsesAPIMetadataPatch["patched"] != "1" {
		t.Fatalf("turn/start params = %#v / %#v", start.ResponsesAPIMetadata, start.ResponsesAPIMetadataPatch)
	}
	var steer turn.TurnSteerParams
	if err := json.Unmarshal([]byte(`{"threadId":"t","expectedTurnId":"turn-1","input":[],"responsesapiClientMetadataPatch":{"patched":"2"}}`), &steer); err != nil {
		t.Fatalf("turn/steer unmarshal: %v", err)
	}
	if steer.ResponsesAPIMetadataPatch["patched"] != "2" || steer.ResponsesAPIMetadata != nil {
		t.Fatalf("turn/steer params = %#v / %#v", steer.ResponsesAPIMetadata, steer.ResponsesAPIMetadataPatch)
	}
}
