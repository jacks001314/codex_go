package session

import (
	"encoding/json"
	"testing"
)

// The cases in this file mirror Rust
// `codex-rs/core/src/context/world_state/top_level_tools_tests.rs`
// (Rust #50540/#51202/#51119) and `base_instructions.rs` (Rust #51188); the Go
// worker keeps the same structured expectations instead of insta snapshots.

func liteDeclaration(name string) map[string]any {
	return map[string]any{
		"type":        "function",
		"name":        name,
		"description": "Look up a value.",
		"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func liteNamespace(name string, tools []any) map[string]any {
	return map[string]any{
		"type":        "namespace",
		"name":        name,
		"description": "Tools.",
		"tools":       tools,
	}
}

func liteAdditionalTools(tools []any) map[string]any {
	return map[string]any{"type": "additional_tools", "role": "developer", "tools": tools}
}

func liteDeveloperMessage(text string, kind string) map[string]any {
	item := map[string]any{
		"type":    "message",
		"role":    "developer",
		"content": []any{map[string]any{"type": "input_text", "text": text}},
	}
	if kind != "" {
		item["internal_chat_message_metadata_passthrough"] = map[string]any{
			"content_item_kinds": []any{kind},
		}
	}
	return item
}

func sameWorldStateJSON(t *testing.T, left any, right any) bool {
	t.Helper()
	leftJSON, err := json.Marshal(left)
	if err != nil {
		t.Fatalf("marshal left: %v", err)
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		t.Fatalf("marshal right: %v", err)
	}
	return string(leftJSON) == string(rightJSON)
}

func mustUpdates(t *testing.T, definitions []any, previous map[string]string, previousKnown bool) (map[string]string, []any) {
	t.Helper()
	hashes, updates, err := TopLevelToolsUpdates(definitions, previous, previousKnown)
	if err != nil {
		t.Fatalf("TopLevelToolsUpdates: %v", err)
	}
	return hashes, MergeWorldStateUpdates(updates)
}

func mustSnapshot(t *testing.T, definitions []any) map[string]string {
	t.Helper()
	hashes, err := TopLevelToolsHashes(definitions)
	if err != nil {
		t.Fatalf("TopLevelToolsHashes: %v", err)
	}
	return hashes
}

// Rust `snapshots_catalog_transitions`.
func TestTopLevelToolsCatalogTransitionsLikeRust(t *testing.T) {
	search := map[string]any{
		"type": "tool_search", "execution": "client", "description": "Search tools.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
	}
	web := map[string]any{"type": "web_search", "external_web_access": true}
	changedSearch := map[string]any{
		"type": "tool_search", "execution": "client", "description": "Search tools.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}},
	}
	original := mustSnapshot(t, []any{search})
	updated := mustSnapshot(t, []any{changedSearch, web})
	onlyWeb := mustSnapshot(t, []any{web})
	empty := mustSnapshot(t, []any{})

	// A first catalog (Absent) declares every definition.
	_, updates := mustUpdates(t, []any{search}, nil, false)
	if !sameWorldStateJSON(t, []any{liteAdditionalTools([]any{search})}, updates) {
		t.Fatalf("absent -> original = %#v", updates)
	}

	// An unchanged catalog emits nothing.
	_, updates = mustUpdates(t, []any{search}, original, true)
	if len(updates) != 0 {
		t.Fatalf("known -> same = %#v", updates)
	}

	// A changed and an added built-in travel together.
	_, updates = mustUpdates(t, []any{changedSearch, web}, original, true)
	if !sameWorldStateJSON(t, []any{liteAdditionalTools([]any{changedSearch, web})}, updates) {
		t.Fatalf("known -> updated = %#v", updates)
	}

	// A removed tool becomes a developer notice instead of a catalog entry.
	_, updates = mustUpdates(t, []any{web}, updated, true)
	if !sameWorldStateJSON(t, []any{liteDeveloperMessage(
		"The following tools are no longer available. Do not call them:\n- tool_search",
		ToolCatalogRemovedDefinitionKind,
	)}, updates) {
		t.Fatalf("known -> only web = %#v", updates)
	}

	_, updates = mustUpdates(t, []any{}, onlyWeb, true)
	if !sameWorldStateJSON(t, []any{liteDeveloperMessage(
		"The following tools are no longer available. Do not call them:\n- web_search",
		ToolCatalogRemovedDefinitionKind,
	)}, updates) {
		t.Fatalf("known -> empty = %#v", updates)
	}

	// A restored catalog re-declares the definition.
	_, updates = mustUpdates(t, []any{search}, empty, true)
	if !sameWorldStateJSON(t, []any{liteAdditionalTools([]any{search})}, updates) {
		t.Fatalf("known empty -> original = %#v", updates)
	}
}

// Rust `snapshots_namespace_transitions`.
func TestTopLevelToolsNamespaceTransitionsLikeRust(t *testing.T) {
	originalDefinitions := []any{
		liteNamespace("functions", []any{liteDeclaration("unchanged"), liteDeclaration("removed"), liteDeclaration("changed")}),
		liteNamespace("other", []any{liteDeclaration("lookup")}),
	}
	changedDeclaration := liteDeclaration("changed")
	changedDeclaration["parameters"] = map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}
	updatedDefinitions := []any{
		liteNamespace("functions", []any{liteDeclaration("unchanged"), changedDeclaration, liteDeclaration("added")}),
		liteNamespace("other", []any{liteDeclaration("lookup")}),
	}
	onlyOther := []any{liteNamespace("other", []any{liteDeclaration("lookup")})}
	onlyFunctions := []any{liteNamespace("functions", []any{liteDeclaration("unchanged")})}
	restored := []any{liteNamespace("functions", []any{liteDeclaration("unchanged"), liteDeclaration("restored")})}

	original := mustSnapshot(t, originalDefinitions)
	updated := mustSnapshot(t, updatedDefinitions)
	onlyOtherSnapshot := mustSnapshot(t, onlyOther)
	empty := mustSnapshot(t, []any{})

	// First catalog preserves the definition order.
	_, updates := mustUpdates(t, originalDefinitions, nil, false)
	if !sameWorldStateJSON(t, []any{liteAdditionalTools(originalDefinitions)}, updates) {
		t.Fatalf("absent -> original = %#v", updates)
	}
	_, updates = mustUpdates(t, originalDefinitions, original, true)
	if len(updates) != 0 {
		t.Fatalf("known -> same = %#v", updates)
	}

	// Only the changed member is re-declared and the namespace header carries the hint.
	expectedNamespace := liteNamespace("functions", []any{changedDeclaration, liteDeclaration("added")})
	expectedNamespace["description"] = "Tools.\n" + NamespaceUpdateHint
	_, updates = mustUpdates(t, updatedDefinitions, original, true)
	if !sameWorldStateJSON(t, []any{
		liteAdditionalTools([]any{expectedNamespace}),
		liteDeveloperMessage("The following tools are no longer available. Do not call them:\n- functions.removed", ToolCatalogRemovedDefinitionKind),
	}, updates) {
		t.Fatalf("known -> updated = %#v", updates)
	}

	// A dropped namespace subsumes its members.
	_, updates = mustUpdates(t, onlyOther, updated, true)
	if !sameWorldStateJSON(t, []any{liteDeveloperMessage(
		"The following namespaces are no longer available. Do not call tools in them unless those tools are declared in a later update:\n- functions",
		ToolCatalogRemovedDefinitionKind,
	)}, updates) {
		t.Fatalf("known updated -> only other = %#v", updates)
	}

	// A namespace that was absent is declared without the incremental hint.
	_, updates = mustUpdates(t, originalDefinitions, onlyOtherSnapshot, true)
	if !sameWorldStateJSON(t, []any{liteAdditionalTools([]any{
		liteNamespace("functions", []any{liteDeclaration("unchanged"), liteDeclaration("removed"), liteDeclaration("changed")}),
	})}, updates) {
		t.Fatalf("known only other -> original = %#v", updates)
	}

	// A whole namespace and individual members can disappear in the same update.
	_, updates = mustUpdates(t, onlyFunctions, updated, true)
	if !sameWorldStateJSON(t, []any{liteDeveloperMessage(
		"The following namespaces are no longer available. Do not call tools in them unless those tools are declared in a later update:\n- other\n\n"+
			"The following tools are no longer available. Do not call them:\n- functions.added\n- functions.changed",
		ToolCatalogRemovedDefinitionKind,
	)}, updates) {
		t.Fatalf("known updated -> only functions = %#v", updates)
	}

	_, updates = mustUpdates(t, []any{}, updated, true)
	if !sameWorldStateJSON(t, []any{liteDeveloperMessage(
		"The following namespaces are no longer available. Do not call tools in them unless those tools are declared in a later update:\n- functions\n- other",
		ToolCatalogRemovedDefinitionKind,
	)}, updates) {
		t.Fatalf("known updated -> empty = %#v", updates)
	}

	// An empty catalog is known state; do not repeat its removal notice.
	_, updates = mustUpdates(t, []any{}, empty, true)
	if len(updates) != 0 {
		t.Fatalf("known empty -> empty = %#v", updates)
	}

	// Restoration declares both an old member and one that has never been available.
	_, updates = mustUpdates(t, restored, empty, true)
	if !sameWorldStateJSON(t, []any{liteAdditionalTools([]any{
		liteNamespace("functions", []any{liteDeclaration("unchanged"), liteDeclaration("restored")}),
	})}, updates) {
		t.Fatalf("known empty -> restored = %#v", updates)
	}
}

// Rust `initial_catalog_preserves_order_and_unchanged_catalog_emits_nothing`.
func TestTopLevelToolsInitialCatalogPreservesOrderLikeRust(t *testing.T) {
	definitions := []any{
		liteNamespace("zebra", []any{liteDeclaration("lookup")}),
		liteNamespace("alpha", []any{liteDeclaration("lookup")}),
	}
	hashes := mustSnapshot(t, definitions)
	_, updates := mustUpdates(t, definitions, nil, false)
	if !sameWorldStateJSON(t, []any{liteAdditionalTools(definitions)}, updates) {
		t.Fatalf("absent -> definitions = %#v", updates)
	}
	_, updates = mustUpdates(t, definitions, nil, false)
	if !sameWorldStateJSON(t, []any{liteAdditionalTools(definitions)}, updates) {
		t.Fatalf("unknown -> definitions = %#v", updates)
	}
	keys := []string{}
	for key := range hashes {
		keys = append(keys, key)
	}
	if len(keys) != 4 {
		t.Fatalf("snapshot keys = %#v", keys)
	}
	for _, want := range []string{"alpha", "alpha.lookup", "zebra", "zebra.lookup"} {
		if _, ok := hashes[want]; !ok {
			t.Fatalf("snapshot missing %q: %#v", want, keys)
		}
	}
	_, updates = mustUpdates(t, definitions, hashes, true)
	if len(updates) != 0 {
		t.Fatalf("unchanged catalog emitted %#v", updates)
	}
}

// Rust `empty_catalog_is_persisted_and_can_add_tools_again`.
func TestTopLevelToolsEmptyCatalogPersistedLikeRust(t *testing.T) {
	definitions := []any{liteNamespace("functions", []any{liteDeclaration("lookup")})}
	previous := mustSnapshot(t, definitions)
	snapshot, updates := mustUpdates(t, []any{}, previous, true)
	if len(snapshot) != 0 {
		t.Fatalf("empty snapshot = %#v", snapshot)
	}
	if !sameWorldStateJSON(t, []any{liteDeveloperMessage(
		"The following namespaces are no longer available. Do not call tools in them unless those tools are declared in a later update:\n- functions",
		ToolCatalogRemovedDefinitionKind,
	)}, updates) {
		t.Fatalf("removal notice = %#v", updates)
	}
	_, updates = mustUpdates(t, definitions, snapshot, true)
	if !sameWorldStateJSON(t, []any{liteAdditionalTools(definitions)}, updates) {
		t.Fatalf("re-added catalog = %#v", updates)
	}
}

// Rust `namespace_description_change_does_not_repeat_unchanged_tools`.
func TestTopLevelToolsNamespaceDescriptionChangeLikeRust(t *testing.T) {
	original := []any{liteNamespace("functions", []any{liteDeclaration("lookup")})}
	previous := mustSnapshot(t, original)
	updated := liteNamespace("functions", []any{liteDeclaration("lookup")})
	updated["description"] = "Updated namespace guidance."
	_, updates := mustUpdates(t, []any{updated}, previous, true)
	if !sameWorldStateJSON(t, []any{liteDeveloperMessage(
		"Updated instructions for the functions namespace:\nUpdated namespace guidance.",
		ToolCatalogDeveloperInstructionsKind,
	)}, updates) {
		t.Fatalf("description change = %#v", updates)
	}
	snapshot := mustSnapshot(t, []any{updated})
	_, updates = mustUpdates(t, []any{updated}, snapshot, true)
	if len(updates) != 0 {
		t.Fatalf("unchanged description emitted %#v", updates)
	}
}

// Rust `tool_type_change_keeps_the_callable_name_available`.
func TestTopLevelToolsToolTypeChangeLikeRust(t *testing.T) {
	original := []any{liteNamespace("functions", []any{liteDeclaration("lookup"), liteDeclaration("removed")})}
	previous := mustSnapshot(t, original)
	custom := map[string]any{
		"type": "custom", "name": "lookup", "description": "Look up a value.",
		"format": map[string]any{"type": "text"},
	}
	_, updates := mustUpdates(t, []any{liteNamespace("functions", []any{custom})}, previous, true)
	expectedNamespace := liteNamespace("functions", []any{custom})
	expectedNamespace["description"] = "Tools.\n" + NamespaceUpdateHint
	if !sameWorldStateJSON(t, []any{
		liteAdditionalTools([]any{expectedNamespace}),
		liteDeveloperMessage("The following tools are no longer available. Do not call them:\n- functions.removed", ToolCatalogRemovedDefinitionKind),
	}, updates) {
		t.Fatalf("type change = %#v", updates)
	}
}

// Rust `namespace_replaced_by_builtin_keeps_the_replacement_available`.
func TestTopLevelToolsNamespaceReplacedByBuiltinLikeRust(t *testing.T) {
	original := []any{liteNamespace("web_search", []any{liteDeclaration("lookup")})}
	previous := mustSnapshot(t, original)
	replacement := map[string]any{"type": "web_search"}
	_, updates := mustUpdates(t, []any{replacement}, previous, true)
	if !sameWorldStateJSON(t, []any{
		liteAdditionalTools([]any{replacement}),
		liteDeveloperMessage("The following tools are no longer available. Do not call them:\n- web_search.lookup", ToolCatalogRemovedDefinitionKind),
	}, updates) {
		t.Fatalf("namespace replaced by builtin = %#v", updates)
	}
}

// Rust `WorldStateHash::from_json` hashes object keys in a stable order, so a
// declaration that only changed JSON key order is not re-sent.
func TestTopLevelToolsHashesIgnoreJSONKeyOrderLikeRust(t *testing.T) {
	first := []byte(`{"type":"function","name":"lookup","description":"Look up a value.","parameters":{"type":"object","properties":{"query":{"type":"string"}}}}`)
	second := []byte(`{"parameters":{"properties":{"query":{"type":"string"}},"type":"object"},"description":"Look up a value.","name":"lookup","type":"function"}`)
	var left, right any
	if err := json.Unmarshal(first, &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second, &right); err != nil {
		t.Fatal(err)
	}
	leftHashes, err := TopLevelToolsHashes([]any{left})
	if err != nil {
		t.Fatal(err)
	}
	rightHashes, err := TopLevelToolsHashes([]any{right})
	if err != nil {
		t.Fatal(err)
	}
	if leftHashes["lookup"] != rightHashes["lookup"] {
		t.Fatalf("hash depends on key order: %q != %q", leftHashes["lookup"], rightHashes["lookup"])
	}
}

// Rust `BaseInstructionsState::render_diff` / `base_instructions_tests.rs`.
func TestBaseInstructionsUpdateLikeRust(t *testing.T) {
	hash, updates := BaseInstructionsUpdate("Use the available tools to help the user.", false)
	if len(updates) != 1 {
		t.Fatalf("absent base instructions = %#v", updates)
	}
	if !sameWorldStateJSON(t, []any{liteDeveloperMessage("Use the available tools to help the user.", BaseInstructionsContentKind)}, MergeWorldStateUpdates(updates)) {
		t.Fatalf("absent base instructions item = %#v", MergeWorldStateUpdates(updates))
	}
	if updates[0].Placement != WorldStatePrefix {
		t.Fatalf("base instructions placement = %v", updates[0].Placement)
	}
	// An existing window never repeats the recorded instructions.
	_, updates = BaseInstructionsUpdate("Use the available tools to help the user.", true)
	if len(updates) != 0 {
		t.Fatalf("known base instructions = %#v", updates)
	}
	// Empty instructions are recorded as a hash but never emitted.
	emptyHash, updates := BaseInstructionsUpdate("", false)
	if len(updates) != 0 || emptyHash == "" {
		t.Fatalf("empty base instructions = %#v (%q)", updates, emptyHash)
	}
	if hash == emptyHash {
		t.Fatalf("distinct instruction text hashed equally: %q", hash)
	}
}

// The order and placement of a prefix item, a mergeable instruction fragment and
// a standalone removal notice survive merging (Rust `merge_world_state_updates`).
func TestMergeWorldStateUpdatesOrderingLikeRust(t *testing.T) {
	updates := []WorldStateUpdate{
		{Placement: WorldStatePrefix, Item: liteAdditionalTools([]any{liteDeclaration("lookup")})},
		{Placement: WorldStateMergeable, Role: "developer", Text: "first", Kind: ToolCatalogDeveloperInstructionsKind},
		{Placement: WorldStateMergeable, Role: "developer", Text: "second", Kind: ToolCatalogDeveloperInstructionsKind},
		{Placement: WorldStateStandalone, Role: "developer", Text: "notice", Kind: ToolCatalogRemovedDefinitionKind},
	}
	items := MergeWorldStateUpdates(updates)
	if len(items) != 3 {
		t.Fatalf("merged items = %#v", items)
	}
	if !sameWorldStateJSON(t, liteAdditionalTools([]any{liteDeclaration("lookup")}), items[0]) {
		t.Fatalf("first item = %#v", items[0])
	}
	merged := map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": "first"},
			map[string]any{"type": "input_text", "text": "second"},
		},
		"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []any{ToolCatalogDeveloperInstructionsKind, ToolCatalogDeveloperInstructionsKind},
		},
	}
	if !sameWorldStateJSON(t, merged, items[1]) {
		t.Fatalf("merged fragment = %#v", items[1])
	}
	if !sameWorldStateJSON(t, liteDeveloperMessage("notice", ToolCatalogRemovedDefinitionKind), items[2]) {
		t.Fatalf("standalone notice = %#v", items[2])
	}
}

// TestWorldStateSnapshotWindowScopingLikeRust covers the window scoping Rust
// #50540/#51188 rely on when a context window is replaced: a snapshot recorded
// for another window is absent, so the replacement window re-declares the full
// catalog and base instructions instead of diffing against the old window.
func TestWorldStateSnapshotWindowScopingLikeRust(t *testing.T) {
	catalog, err := EncodeTopLevelToolsSnapshot(2, map[string]string{"functions.exec": "abc"})
	if err != nil {
		t.Fatal(err)
	}
	instructions, err := EncodeBaseInstructionsSnapshot(2, "def")
	if err != nil {
		t.Fatal(err)
	}
	hashes, ok := TopLevelToolsSnapshotForWindow(catalog, 2)
	if !ok || hashes["functions.exec"] != "abc" {
		t.Fatalf("window 2 snapshot = %#v, %v", hashes, ok)
	}
	if _, ok := TopLevelToolsSnapshotForWindow(catalog, 3); ok {
		t.Fatal("a snapshot for window 2 must be absent for window 3")
	}
	if hash, ok := BaseInstructionsSnapshotForWindow(instructions, 2); !ok || hash != "def" {
		t.Fatalf("window 2 base instructions = %q, %v", hash, ok)
	}
	if _, ok := BaseInstructionsSnapshotForWindow(instructions, 3); ok {
		t.Fatal("base instructions for window 2 must be absent for window 3")
	}
	if _, ok := TopLevelToolsSnapshotForWindow(nil, 0); ok {
		t.Fatal("a missing snapshot must be absent")
	}
	// Window zero is a real window (a fresh thread), so it must round-trip.
	zero, err := EncodeTopLevelToolsSnapshot(0, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := TopLevelToolsSnapshotForWindow(zero, 0); !ok {
		t.Fatalf("window 0 snapshot = %s, want it recorded", string(zero))
	}
	if _, ok := TopLevelToolsSnapshotForWindow(zero, 1); ok {
		t.Fatal("window 0 must not match window 1")
	}
}
