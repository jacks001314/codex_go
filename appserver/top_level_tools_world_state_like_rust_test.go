package appserver

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// Rust `TopLevelToolsSnapshot` (core/src/context/world_state/top_level_tools.rs)
// is an untagged enum whose ToolsOnly branch is a bare `{name: hash}` map, and
// Rust `ModelInstructionsSnapshot` (…/model.rs) records `{model, instructions}`
// for incremental windows. #53872 deleted the separate `base_instructions`
// section, so the Go record must persist exactly those two section shapes.
func TestIncrementalWorldStatePersistsRustSectionShapesLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-world-state-shapes")
	created := time.Now().UTC()
	instructions := "Use the available tools to help the user."
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())
	if _, err := router.incrementalToolCatalogForTurn("thread-world-state-shapes", nil, definitions, instructions, "turn-1", created); err != nil {
		t.Fatal(err)
	}
	record, err := router.threadRecord("thread-world-state-shapes", true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord error = %v", err)
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(record.Metadata.WorldState, &sections); err != nil {
		t.Fatalf("decode world state: %v", err)
	}
	for _, retired := range []string{"base_instructions", "baseInstructions"} {
		if _, ok := sections[retired]; ok {
			t.Fatalf("world state still records the retired %q section: %s", retired, record.Metadata.WorldState)
		}
	}
	catalog, ok := sections["top_level_tools"]
	if !ok || !bytes.HasPrefix(bytes.TrimSpace(catalog), []byte("{")) {
		t.Fatalf("top_level_tools section = %s, want Rust's ToolsOnly map", string(catalog))
	}
	var hashes map[string]string
	if err := json.Unmarshal(catalog, &hashes); err != nil {
		t.Fatalf("top_level_tools decode: %v", err)
	}
	if hashes["functions.exec"] == "" || hashes["tool_search"] == "" {
		t.Fatalf("top_level_tools hashes = %#v", hashes)
	}
	for _, retired := range []string{"tools", "exec", "hashes", "window"} {
		if _, ok := hashes[retired]; ok {
			t.Fatalf("top_level_tools kept the wrapper key %q: %s", retired, string(catalog))
		}
	}
	modelID, hash, known, withInstructions := session.DecodeModelStateSnapshot(sections["model"])
	if !known || !withInstructions || modelID != "gpt-5.4" || hash != session.BaseInstructionsHash(instructions) {
		t.Fatalf("model section = %s (%q/%q/%v/%v), want WithInstructions", string(sections["model"]), modelID, hash, known, withInstructions)
	}
}

// Rust `ModelInstructionsState::render_diff` keeps the incremental snapshot's
// WithInstructions shape when the model switches inside a window and refreshes
// the instructions through a `model_instructions` fragment instead of the
// ordinary ModelOnly model-switch path.
func TestIncrementalWindowModelSwitchKeepsWithInstructionsLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-world-state-switch")
	created := time.Now().UTC()
	instructions := "Use the available tools to help the user."
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())
	if _, err := router.incrementalToolCatalogForTurn("thread-world-state-switch", nil, definitions, instructions, "turn-1", created); err != nil {
		t.Fatal(err)
	}
	history := incrementalHistoryItems(t, router, "thread-world-state-switch")
	result, err := router.incrementalToolCatalogForTurnWithModel("thread-world-state-switch", history, definitions, instructions, "gpt-next", "turn-2", created.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	announced := false
	for _, item := range result.Items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text := textFromInputItemContent(object["content"]); strings.Contains(text, "The current model is gpt-next.") {
			announced = true
		}
	}
	if !announced {
		t.Fatalf("model switch items = %#v, want a model_instructions fragment", result.Items)
	}
	record, err := router.threadRecord("thread-world-state-switch", true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord error = %v", err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatal(err)
	}
	modelID, hash, known, withInstructions := session.DecodeModelStateSnapshot(state.Model)
	if !known || !withInstructions || modelID != "gpt-next" || hash != session.BaseInstructionsHash(instructions) {
		t.Fatalf("model section = %s (%q/%q/%v/%v), want WithInstructions for gpt-next", string(state.Model), modelID, hash, known, withInstructions)
	}
}

// Rust #53872 rewrites the `model` section for a replacement window with the
// latest model identity and the refreshed base-instruction hash.
func TestCompactionRewritesModelSectionLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-world-state-compaction")
	created := time.Now().UTC()
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())
	prefix := declarationPrefixSessionItemsLikeRust(t, definitions, "Old instructions.", "thread-world-state-compaction", created)
	record, err := router.threadRecord("thread-world-state-compaction", true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord error = %v", err)
	}
	latest := "Latest instructions."
	if err := placeIncrementalCatalogBeforeCompactedHistory(record, prefix, 1, "gpt-next", &latest); err != nil {
		t.Fatal(err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatal(err)
	}
	modelID, hash, known, withInstructions := session.DecodeModelStateSnapshot(state.Model)
	if !known || !withInstructions || modelID != "gpt-next" || hash != session.BaseInstructionsHash(latest) {
		t.Fatalf("replacement model section = %s (%q/%q/%v/%v)", string(state.Model), modelID, hash, known, withInstructions)
	}
	if len(state.BaseInstructions) != 0 {
		t.Fatalf("replacement world state kept base_instructions: %s", string(state.BaseInstructions))
	}
}
