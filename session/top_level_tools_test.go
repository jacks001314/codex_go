package session

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/eventmap"
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

func mustUpdatesWithMessages(t *testing.T, definitions []any, previous map[string]string, previousKnown bool, messages ResolvedIncrementalToolMessages) (map[string]string, []any) {
	t.Helper()
	hashes, updates, err := TopLevelToolsUpdatesWithMessages(definitions, previous, previousKnown, messages)
	if err != nil {
		t.Fatalf("TopLevelToolsUpdatesWithMessages: %v", err)
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

	// One update hint precedes only the changed member declarations.
	expectedNamespace := liteNamespace("functions", []any{changedDeclaration, liteDeclaration("added")})
	_, updates = mustUpdates(t, updatedDefinitions, original, true)
	if !sameWorldStateJSON(t, []any{
		liteDeveloperMessage(ToolUpdateHint, ToolCatalogUpdateHintKind),
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

	// A namespace that was absent shares the same one-time update hint.
	_, updates = mustUpdates(t, originalDefinitions, onlyOtherSnapshot, true)
	if !sameWorldStateJSON(t, []any{
		liteDeveloperMessage(ToolUpdateHint, ToolCatalogUpdateHintKind),
		liteAdditionalTools([]any{
			liteNamespace("functions", []any{liteDeclaration("unchanged"), liteDeclaration("removed"), liteDeclaration("changed")}),
		}),
	}, updates) {
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
	if !sameWorldStateJSON(t, []any{
		liteDeveloperMessage(ToolUpdateHint, ToolCatalogUpdateHintKind),
		liteAdditionalTools([]any{
			liteNamespace("functions", []any{liteDeclaration("unchanged"), liteDeclaration("restored")}),
		}),
	}, updates) {
		t.Fatalf("known empty -> restored = %#v", updates)
	}
}

// Rust `new_namespaces_share_one_notice_before_their_declarations`.
func TestTopLevelToolsNewNamespacesShareOneUpdateNoticeLikeRust(t *testing.T) {
	original := liteNamespace("original", []any{liteDeclaration("lookup")})
	previous := mustSnapshot(t, []any{original})
	additions := []any{
		liteNamespace("second", []any{liteDeclaration("lookup")}),
		liteNamespace("third", []any{liteDeclaration("lookup")}),
	}
	definitions := append([]any{original}, additions...)
	snapshot, updates := mustUpdates(t, definitions, previous, true)
	if !sameWorldStateJSON(t, []any{
		liteDeveloperMessage(ToolUpdateHint, ToolCatalogUpdateHintKind),
		liteAdditionalTools(additions),
	}, updates) {
		t.Fatalf("new namespaces = %#v, want one shared update notice", updates)
	}
	_, updates = mustUpdates(t, definitions, snapshot, true)
	if len(updates) != 0 {
		t.Fatalf("unchanged namespaces emitted %#v", updates)
	}
}

func TestTopLevelToolsCatalogMessageOverridesLikeRust(t *testing.T) {
	empty := ""
	updateHint := "Catalog update."
	prefix := "Catalog {name}: "
	cleared := "Cleared {name}."
	removedTools := "Unavailable tools:"
	removedNamespaces := "Unavailable namespaces:"
	messages := ResolveIncrementalToolMessages(&IncrementalToolMessageOverrides{
		ToolUpdateHint:               &updateHint,
		NamespaceInstructionsPrefix:  &prefix,
		NamespaceInstructionsCleared: &cleared,
		RemovedToolsHeader:           &removedTools,
		RemovedNamespacesHeader:      &removedNamespaces,
	})
	original := []any{liteNamespace("functions", []any{liteDeclaration("lookup")})}
	previous := mustSnapshot(t, original)
	withInstructions := liteNamespace("functions", []any{liteDeclaration("lookup")})
	withInstructions["description"] = "Literal {name}."
	_, updates := mustUpdatesWithMessages(t, []any{withInstructions}, previous, true, messages)
	if !sameWorldStateJSON(t, []any{liteDeveloperMessage("Catalog functions: Literal {name}.", ToolCatalogDeveloperInstructionsKind)}, updates) {
		t.Fatalf("namespace instruction override = %#v", updates)
	}

	removed := []any{liteNamespace("other", []any{liteDeclaration("lookup")})}
	_, updates = mustUpdatesWithMessages(t, removed, previous, true, messages)
	if !sameWorldStateJSON(t, []any{
		liteDeveloperMessage("Catalog update.", ToolCatalogUpdateHintKind),
		liteAdditionalTools(removed),
		liteDeveloperMessage("Unavailable namespaces:\n- functions", ToolCatalogRemovedDefinitionKind),
	}, updates) {
		t.Fatalf("removal header override = %#v", updates)
	}

	emptyHint := ResolveIncrementalToolMessages(&IncrementalToolMessageOverrides{
		ToolUpdateHint: &empty,
	})
	changed := []any{liteNamespace("functions", []any{liteDeclaration("lookup"), liteDeclaration("added")})}
	_, updates = mustUpdatesWithMessages(t, changed, previous, true, emptyHint)
	if len(updates) != 1 {
		t.Fatalf("empty hint should retain declarations only: %#v", updates)
	}
	item, _ := updates[0].(map[string]any)
	if item["type"] != "additional_tools" {
		t.Fatalf("empty hint should retain declarations only: %#v", updates)
	}
	tooLong := strings.Repeat("x", 513)
	fallback := ResolveIncrementalToolMessages(&IncrementalToolMessageOverrides{ToolUpdateHint: &tooLong})
	if fallback.ToolUpdateHint != ToolUpdateHint {
		t.Fatalf("oversized override = %q, want bundled wording", fallback.ToolUpdateHint)
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
	if !sameWorldStateJSON(t, []any{
		liteDeveloperMessage(ToolUpdateHint, ToolCatalogUpdateHintKind),
		liteAdditionalTools(definitions),
	}, updates) {
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
	if !sameWorldStateJSON(t, []any{
		liteDeveloperMessage(ToolUpdateHint, ToolCatalogUpdateHintKind),
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

// Rust #53872 (`ModelInstructionsState` incremental mode): model identity and
// instruction content are tracked independently, and a changed window appends
// one replacement/removal notice without rewriting the existing prefix.
func TestModelInstructionsUpdateLikeRust(t *testing.T) {
	hash, updates, _ := ModelInstructionsUpdate("gpt-test", "Base instructions.", "", "", false, false, false)
	if len(updates) != 1 || updates[0].Placement != WorldStatePrefix ||
		!sameWorldStateJSON(t, []any{liteDeveloperMessage("Base instructions.", BaseInstructionsContentKind)}, MergeWorldStateUpdates(updates)) {
		t.Fatalf("new window update = %#v", updates)
	}
	if updates := func() []WorldStateUpdate {
		_, out, _ := ModelInstructionsUpdate("gpt-test", "Base instructions.", "gpt-test", hash, true, true, false)
		return out
	}(); len(updates) != 0 {
		t.Fatalf("unchanged update = %#v", updates)
	}
	if _, updates, _ := ModelInstructionsUpdate("gpt-test", "Instructions.", "gpt-test", hash, true, true, false); len(updates) != 1 {
		t.Fatalf("replacement update = %#v", updates)
	} else if !strings.Contains(updates[0].Text, "The following instructions replace the previous base instructions:") ||
		!strings.Contains(updates[0].Text, "Instructions.") {
		t.Fatalf("replacement item = %#v", updates[0])
	}
	removedHash, removed, _ := ModelInstructionsUpdate("gpt-test", "", "gpt-test", hash, true, true, false)
	if removedHash == "" || len(removed) != 1 {
		t.Fatalf("removal update = %#v", removed)
	}
	if !strings.Contains(removed[0].Text, "The previous base instructions no longer apply. No replacement base instructions are active.") {
		t.Fatalf("removal item = %#v", removed[0])
	}
	_, modelOnly, _ := ModelInstructionsUpdate("gpt-next", "Instructions.", "gpt-test", BaseInstructionsHash("Instructions."), true, true, false)
	if len(modelOnly) != 1 || !strings.Contains(modelOnly[0].Text, "The current model is gpt-next.") ||
		strings.Contains(modelOnly[0].Text, "replace the previous") {
		t.Fatalf("model-only item = %#v", modelOnly)
	}
	// A legacy hash-only snapshot tracks the new model silently with its
	// unchanged instructions, avoiding an upgrade-time false model change.
	if _, legacy, _ := ModelInstructionsUpdate("gpt-test", "Base instructions.", "", hash, true, true, false); len(legacy) != 0 {
		t.Fatalf("legacy snapshot update = %#v", legacy)
	}
}

// Rust #58405: with suppress_model_change_announcements a model switch omits the
// model ID, still sends changed instructions, and advances the recorded hash so
// repeated turns of the same window stay quiet.
func TestModelInstructionsUpdateSuppressesModelChangeAnnouncementsLikeRust(t *testing.T) {
	hash := BaseInstructionsHash("instructions")
	switched, updates, advance := ModelInstructionsUpdate("gpt-next", "instructions", "gpt-old", hash, true, true, true)
	if switched != hash {
		t.Fatalf("suppressed model switch hash = %q, want %q", switched, hash)
	}
	if len(updates) != 0 {
		t.Fatalf("suppressed model switch updates = %#v, want none", updates)
	}
	if !advance {
		t.Fatal("suppressed model switch must advance the recorded model snapshot")
	}
	if _, repeated, repeatedAdvance := ModelInstructionsUpdate("gpt-next", "instructions", "gpt-next", switched, true, true, true); len(repeated) != 0 || repeatedAdvance {
		t.Fatalf("repeated suppressed turn updates = %#v, want none", repeated)
	}
	_, changed, _ := ModelInstructionsUpdate("gpt-next", "new instructions", "gpt-old", hash, true, true, true)
	if len(changed) != 1 {
		t.Fatalf("suppressed model switch with new instructions updates = %#v, want one", changed)
	}
	if strings.Contains(changed[0].Text, "The current model is gpt-next.") {
		t.Fatalf("suppressed update announced the model: %#v", changed[0])
	}
	if !strings.Contains(changed[0].Text, "new instructions") {
		t.Fatalf("suppressed update lost the instructions: %#v", changed[0])
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

// TestWorldStateSnapshotWindowScopingLikeRust covers Rust's wire shapes and the
// Go-side window scoping used when a context window is replaced: Rust persists
// a bare tool-hash map, so Go keeps the window number in thread metadata and a
// snapshot recorded for another window is absent.
func TestWorldStateSnapshotWindowScopingLikeRust(t *testing.T) {
	catalog, err := EncodeTopLevelToolsSnapshot(map[string]string{"functions.exec": "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if string(catalog) != `{"functions.exec":"abc"}` {
		t.Fatalf("ToolsOnly snapshot = %s, want a bare hash map", string(catalog))
	}
	window2 := uint64(2)
	hashes, ok := TopLevelToolsSnapshotForWindow(catalog, 2, &window2)
	if !ok || hashes["functions.exec"] != "abc" {
		t.Fatalf("window 2 snapshot = %#v, %v", hashes, ok)
	}
	if _, ok := TopLevelToolsSnapshotForWindow(catalog, 3, &window2); ok {
		t.Fatal("a snapshot recorded for window 2 must be absent for window 3")
	}
	// A Rust-written snapshot has no window of its own: it belongs to whichever
	// window the Go record says is current.
	if hashes, ok := TopLevelToolsSnapshotForWindow(catalog, 7, nil); !ok || hashes["functions.exec"] != "abc" {
		t.Fatalf("Rust snapshot without Go window = %#v, %v", hashes, ok)
	}

	// Legacy Go snapshots keep their embedded window for in-place upgrades.
	legacy := json.RawMessage(`{"window":2,"hashes":{"functions.exec":"abc"}}`)
	if hashes, ok := TopLevelToolsSnapshotForWindow(legacy, 2, nil); !ok || hashes["functions.exec"] != "abc" {
		t.Fatalf("legacy window 2 snapshot = %#v, %v", hashes, ok)
	}
	if _, ok := TopLevelToolsSnapshotForWindow(legacy, 3, nil); ok {
		t.Fatal("legacy snapshot for window 2 must be absent for window 3")
	}

	// Rust's WithExec shape shares its `tools` half with the ordinary catalog.
	withExec := json.RawMessage(`{"tools":{"functions.exec":"abc"},"exec":{"fast":"def"}}`)
	if hashes, ok := TopLevelToolsSnapshotForWindow(withExec, 4, nil); !ok || hashes["functions.exec"] != "abc" || hashes["fast"] != "" {
		t.Fatalf("WithExec snapshot = %#v, %v", hashes, ok)
	}

	// Rust persists an empty catalog as a known baseline.
	if hashes, ok := TopLevelToolsSnapshotForWindow(json.RawMessage(`{}`), 0, nil); !ok || len(hashes) != 0 {
		t.Fatalf("empty snapshot = %#v, %v", hashes, ok)
	}
	if _, ok := TopLevelToolsSnapshotForWindow(nil, 0, nil); ok {
		t.Fatal("a missing snapshot must be absent")
	}

	// The retired base_instructions section stays readable for migration.
	instructions, err := EncodeBaseInstructionsSnapshot(2, "def")
	if err != nil {
		t.Fatal(err)
	}
	if hash, ok := BaseInstructionsSnapshotForWindow(instructions, 2); !ok || hash != "def" {
		t.Fatalf("window 2 base instructions = %q, %v", hash, ok)
	}
	if _, ok := BaseInstructionsSnapshotForWindow(instructions, 3); ok {
		t.Fatal("base instructions for window 2 must be absent for window 3")
	}

	modelSnapshot, err := EncodeModelStateSnapshot("gpt-test", "def")
	if err != nil {
		t.Fatal(err)
	}
	if string(modelSnapshot) != `{"model":"gpt-test","instructions":"def"}` {
		t.Fatalf("model snapshot = %s, want Rust's WithInstructions shape", string(modelSnapshot))
	}
	if modelID, hash, known, withInstructions := DecodeModelStateSnapshot(modelSnapshot); !known || !withInstructions || modelID != "gpt-test" || hash != "def" {
		t.Fatalf("model snapshot decode = %q/%q/%v/%v", modelID, hash, known, withInstructions)
	}
	if modelID, hash, known, withInstructions := DecodeModelStateSnapshot(json.RawMessage(`"gpt-test"`)); !known || withInstructions || modelID != "gpt-test" || hash != "" {
		t.Fatalf("ModelOnly decode = %q/%q/%v/%v", modelID, hash, known, withInstructions)
	}
}

// Rust #40184's build_rendered_message pairs every emitted content item with a
// positional classification. A typed fragment contributes more than one item,
// so the metadata list must grow with the content list rather than the number
// of fragments.
func TestMergeWorldStateUpdatesPreservesKindsPerTypedContentItemLikeRust(t *testing.T) {
	items := MergeWorldStateUpdates([]WorldStateUpdate{{
		Role: "user",
		Kind: "test.context",
		ContentItems: []eventmap.ContentItem{
			{Kind: eventmap.ContentInputText, Text: "hello"},
			{Kind: eventmap.ContentInputImage, ImageURL: "data:image/png;base64,AAA"},
			{Kind: eventmap.ContentInputAudio, AudioURL: "data:audio/wav;base64,YXVkaW8="},
		},
	}})
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	message, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("message type = %T", items[0])
	}
	content, ok := message["content"].([]map[string]any)
	if !ok || len(content) != 3 {
		t.Fatalf("content = %#v", message["content"])
	}
	metadata, ok := message["internal_chat_message_metadata_passthrough"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %#v", message["internal_chat_message_metadata_passthrough"])
	}
	kinds, ok := metadata["content_item_kinds"].([]string)
	if !ok || len(kinds) != len(content) {
		t.Fatalf("kinds = %#v, want %d entries", metadata["content_item_kinds"], len(content))
	}
	for i := range kinds {
		if kinds[i] != "test.context" {
			t.Fatalf("kind %d = %q", i, kinds[i])
		}
	}
}
