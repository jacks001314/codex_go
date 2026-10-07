package session

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// This file ports the Responses Lite incremental tool catalog world-state
// section (Rust `codex-rs/core/src/context/world_state/top_level_tools.rs`,
// #50540 `Send incremental tool catalog updates in Responses Lite`), the base
// instruction section that sits next to it (Rust
// `context/world_state/base_instructions.rs`, #51188), and the ordered
// world-state update model they use (Rust `context/world_state/mod.rs`, #50441).
//
// When `IncrementalTools` is enabled for a Responses Lite model the initial
// catalog is recorded once at the start of a context window and later turns
// append only added or changed declarations, plus developer notices for
// removals and namespace instruction changes, so an existing request prefix
// stays byte-identical.

const (
	// NamespaceUpdateHint decorates a namespace that is re-declared because one
	// of its members changed (Rust `NAMESPACE_UPDATE_HINT`, #51119).
	NamespaceUpdateHint = "This is an incremental namespace update. Previously declared tools remain available for direct calls unless explicitly marked unavailable. If a tool is redefined here, its latest definition replaces the earlier one."
	// removedToolsHeader and removedNamespacesHeader head the two sections of a
	// single removal notice (#51202 distinguishes whole namespace removals).
	removedToolsHeader      = "The following tools are no longer available. Do not call them:"
	removedNamespacesHeader = "The following namespaces are no longer available. Do not call tools in them unless those tools are declared in a later update:"

	// Content kinds recorded on the harness-authored developer messages.
	ToolCatalogDeveloperInstructionsKind = "generic.developer_instructions"
	ToolCatalogRemovedDefinitionKind     = "tools.removed_definition"
	BaseInstructionsContentKind          = "model.base_instructions"
)

// WorldStatePlacement mirrors Rust `Placement`: how one world-state
// contribution is placed when the context is assembled (#50441).
type WorldStatePlacement int

const (
	// WorldStateMergeable fragments merge into one developer message.
	WorldStateMergeable WorldStatePlacement = iota
	// WorldStateStandalone fragments keep their own message boundary.
	WorldStateStandalone
	// WorldStatePrefix content is placed at the front of the initial context.
	WorldStatePrefix
)

// WorldStateUpdate mirrors Rust `WorldStateUpdate`: one ordered contribution to
// the model context, either a ready response item or a developer fragment.
type WorldStateUpdate struct {
	Placement WorldStatePlacement
	// Item is a ready response item (an `additional_tools` declaration); when
	// non-nil the fragment fields are ignored.
	Item map[string]any
	// Role is the fragment role; an empty role defaults to "developer".
	Role string
	// Text is the fragment body.
	Text string
	// Kind is the fragment's content_item_kind, recorded as message metadata.
	Kind string
	// Standalone marks a fragment that must not merge with its neighbours.
	// (Placement==WorldStateStandalone carries the same meaning; this field is
	// kept for callers that build updates positionally.)
}

// TopLevelToolsSnapshot is the persisted comparison state of the Responses Lite
// top-level tool catalog: the window it belongs to and the stable hash of every
// namespace, namespace member and built-in declaration. A snapshot recorded for
// a different window is treated as absent, so a window replacement (compaction
// or context reset) re-declares the full catalog.
type TopLevelToolsSnapshot struct {
	Window *uint64           `json:"window,omitempty"`
	Hashes map[string]string `json:"hashes,omitempty"`
}

// BaseInstructionsSnapshot is the persisted comparison state of the recorded
// base instructions (#51188).
type BaseInstructionsSnapshot struct {
	Window *uint64 `json:"window,omitempty"`
	Hash   string  `json:"hash,omitempty"`
}

// DecodeTopLevelToolsSnapshot decodes a persisted catalog snapshot. ok is false
// when nothing usable was recorded, which callers treat as "absent" (a fresh
// window) or "unknown" (retained history without a typed snapshot).
func DecodeTopLevelToolsSnapshot(raw json.RawMessage) (TopLevelToolsSnapshot, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return TopLevelToolsSnapshot{}, false
	}
	var snapshot TopLevelToolsSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return TopLevelToolsSnapshot{}, false
	}
	if snapshot.Window == nil {
		return TopLevelToolsSnapshot{}, false
	}
	if snapshot.Hashes == nil {
		snapshot.Hashes = map[string]string{}
	}
	return snapshot, true
}

// DecodeBaseInstructionsSnapshot decodes a persisted base-instruction snapshot.
func DecodeBaseInstructionsSnapshot(raw json.RawMessage) (BaseInstructionsSnapshot, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return BaseInstructionsSnapshot{}, false
	}
	var snapshot BaseInstructionsSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return BaseInstructionsSnapshot{}, false
	}
	if snapshot.Window == nil {
		return BaseInstructionsSnapshot{}, false
	}
	return snapshot, true
}

// EncodeTopLevelToolsSnapshot serializes a catalog snapshot for the record.
func EncodeTopLevelToolsSnapshot(window uint64, hashes map[string]string) (json.RawMessage, error) {
	if hashes == nil {
		hashes = map[string]string{}
	}
	return json.Marshal(TopLevelToolsSnapshot{Window: &window, Hashes: hashes})
}

// EncodeBaseInstructionsSnapshot serializes a base-instruction snapshot.
func EncodeBaseInstructionsSnapshot(window uint64, hash string) (json.RawMessage, error) {
	return json.Marshal(BaseInstructionsSnapshot{Window: &window, Hash: hash})
}

// TopLevelToolsHashes computes the stable per-declaration hashes of a serialized
// Responses Lite catalog (Rust `TopLevelToolsState::new`). Namespace members are
// hashed by their qualified `namespace.member` name; a namespace header is
// hashed without its members so a member-only change leaves the header stable.
func TopLevelToolsHashes(definitions []any) (map[string]string, error) {
	hashes := map[string]string{}
	insert := func(name string, definition any) error {
		if name == "" {
			return fmt.Errorf("responses lite tool declaration without a name or type")
		}
		if _, exists := hashes[name]; exists {
			return fmt.Errorf("responses lite tool declaration collision: %s", name)
		}
		hashes[name] = worldStateJSONHash(definition)
		return nil
	}
	for _, definition := range definitions {
		name := topLevelDefinitionName(definition)
		if members, ok := topLevelDefinitionMembers(definition); ok {
			for _, member := range members {
				memberName := topLevelDefinitionName(member)
				if err := insert(name+"."+memberName, member); err != nil {
					return nil, err
				}
			}
		}
		header, err := cloneJSONWithoutKey(definition, "tools")
		if err != nil {
			return nil, err
		}
		if err := insert(name, header); err != nil {
			return nil, err
		}
	}
	return hashes, nil
}

// TopLevelToolsUpdates diffs the current catalog against the recorded snapshot
// and returns the snapshot to persist plus the ordered updates to append
// (Rust `TopLevelToolsState::render_diff`). previousKnown is false for a fresh
// window or when only retained history (without a typed snapshot) exists; both
// start a fresh catalog rather than migrating legacy history.
//
// The first update, when present, is the `additional_tools` prefix item holding
// only added or changed declarations. Namespace instruction changes and the
// removal notice follow as developer messages.
func TopLevelToolsUpdates(definitions []any, previous map[string]string, previousKnown bool) (map[string]string, []WorldStateUpdate, error) {
	hashes, err := TopLevelToolsHashes(definitions)
	if err != nil {
		return nil, nil, err
	}
	changed := func(name string) bool {
		prior, hadPrior := previous[name]
		current, hasCurrent := hashes[name]
		if hadPrior != hasCurrent {
			return true
		}
		return prior != current
	}

	namespaceUpdates := []WorldStateUpdate{}
	tools := []any{}
	for _, definition := range definitions {
		name := topLevelDefinitionName(definition)
		members, isNamespace := topLevelDefinitionMembers(definition)
		if !isNamespace {
			if changed(name) {
				tools = append(tools, definition)
			}
			continue
		}
		changedMembers := make([]any, 0, len(members))
		for _, member := range members {
			if changed(name + "." + topLevelDefinitionName(member)) {
				changedMembers = append(changedMembers, member)
			}
		}
		if len(changedMembers) > 0 {
			namespace, err := cloneJSONWithoutKey(definition, "tools")
			if err != nil {
				return nil, nil, err
			}
			namespace["tools"] = changedMembers
			if _, previouslyDeclared := previous[name]; previousKnown && previouslyDeclared {
				description := topLevelDefinitionDescription(definition)
				if description == "" {
					namespace["description"] = NamespaceUpdateHint
				} else {
					namespace["description"] = description + "\n" + NamespaceUpdateHint
				}
			}
			tools = append(tools, namespace)
			continue
		}
		if changed(name) {
			// A namespace declaration requires a member, so a metadata-only
			// change travels as developer text instead.
			instructions := topLevelDefinitionDescription(definition)
			text := fmt.Sprintf("The %s namespace no longer has additional instructions.", name)
			if instructions != "" {
				text = fmt.Sprintf("Updated instructions for the %s namespace:\n%s", name, instructions)
			}
			namespaceUpdates = append(namespaceUpdates, WorldStateUpdate{
				Placement: WorldStateMergeable,
				Role:      "developer",
				Text:      text,
				Kind:      ToolCatalogDeveloperInstructionsKind,
			})
		}
	}

	updates := []WorldStateUpdate{}
	if len(tools) > 0 {
		updates = append(updates, WorldStateUpdate{
			Placement: WorldStatePrefix,
			Item: map[string]any{
				"type":  "additional_tools",
				"role":  "developer",
				"tools": tools,
			},
		})
	}
	updates = append(updates, namespaceUpdates...)
	if previousKnown {
		namespaces, removedTools := removedTopLevelDeclarations(previous, hashes)
		if len(namespaces) > 0 || len(removedTools) > 0 {
			updates = append(updates, WorldStateUpdate{
				Placement: WorldStateStandalone,
				Role:      "developer",
				Text:      RemovedToolsNotice(namespaces, removedTools),
				Kind:      ToolCatalogRemovedDefinitionKind,
			})
		}
	}
	return hashes, updates, nil
}

// BaseInstructionsUpdate returns the ordered updates for the fixed base
// instructions recorded at the start of an incremental tool window (Rust
// `BaseInstructionsState::render_diff`, #51188): the fragment is emitted only
// when the window has no previous snapshot and the text is non-empty, so later
// turns never repeat it. The returned hash is the snapshot to persist.
func BaseInstructionsUpdate(text string, previousKnown bool) (string, []WorldStateUpdate) {
	hash := BaseInstructionsHash(text)
	if previousKnown || strings.TrimSpace(text) == "" {
		return hash, nil
	}
	return hash, []WorldStateUpdate{{
		Placement: WorldStatePrefix,
		Role:      "developer",
		Text:      text,
		Kind:      BaseInstructionsContentKind,
	}}
}

// BaseInstructionsHash hashes a base-instruction fragment the way Rust's
// `WorldStateHash::from_fragment` does (version tag + length-prefixed role and
// rendered content, SHA-1 in lowercase hex).
func BaseInstructionsHash(text string) string {
	hasher := sha1.New()
	hasher.Write([]byte("codex-world-state-fragment-v1\x00"))
	hashComponent(hasher, "developer")
	hashComponent(hasher, text)
	return hex.EncodeToString(hasher.Sum(nil))
}

// RemovedToolsNotice renders the single removal notice that lists whole
// namespace removals and individual tool removals in separate sections
// (Rust `RemovedTools::body`).
func RemovedToolsNotice(namespaces []string, tools []string) string {
	sections := []struct {
		header string
		names  []string
	}{
		{removedNamespacesHeader, namespaces},
		{removedToolsHeader, tools},
	}
	var body strings.Builder
	for _, section := range sections {
		if len(section.names) == 0 {
			continue
		}
		if body.Len() > 0 {
			body.WriteString("\n\n")
		}
		body.WriteString(section.header)
		for _, name := range section.names {
			body.WriteString("\n- ")
			body.WriteString(name)
		}
	}
	return body.String()
}

// MergeWorldStateUpdates flattens ordered world-state updates into request
// items, mirroring Rust `merge_world_state_updates`: response items keep their
// position, mergeable fragments of one role collapse into a single developer
// message, and standalone fragments keep their own message.
func MergeWorldStateUpdates(updates []WorldStateUpdate) []any {
	items := []any{}
	pending := []WorldStateUpdate{}
	flush := func() {
		if len(pending) == 0 {
			return
		}
		items = append(items, renderedWorldStateMessage(pending))
		pending = nil
	}
	for _, update := range updates {
		if update.Item != nil {
			flush()
			items = append(items, cloneAnyMapDeep(update.Item))
			continue
		}
		if update.Placement != WorldStateMergeable || strings.TrimSpace(update.Text) == "" {
			flush()
			if strings.TrimSpace(update.Text) == "" {
				continue
			}
			items = append(items, renderedWorldStateMessage([]WorldStateUpdate{update}))
			continue
		}
		if len(pending) > 0 && worldStateUpdateRole(pending[0]) != worldStateUpdateRole(update) {
			flush()
		}
		pending = append(pending, update)
	}
	flush()
	return items
}

func worldStateUpdateRole(update WorldStateUpdate) string {
	role := strings.TrimSpace(update.Role)
	if role == "" {
		return "developer"
	}
	return role
}

func renderedWorldStateMessage(updates []WorldStateUpdate) map[string]any {
	content := make([]map[string]any, 0, len(updates))
	kinds := make([]string, 0, len(updates))
	for _, update := range updates {
		content = append(content, map[string]any{
			"type": "input_text",
			"text": strings.Trim(update.Text, "\n"),
		})
		if kind := strings.TrimSpace(update.Kind); kind != "" {
			kinds = append(kinds, kind)
		}
	}
	item := map[string]any{
		"type":    "message",
		"role":    worldStateUpdateRole(updates[0]),
		"content": content,
	}
	if len(kinds) > 0 {
		item["internal_chat_message_metadata_passthrough"] = map[string]any{
			"content_item_kinds": kinds,
		}
	}
	return item
}

// removedTopLevelDeclarations splits the previous declarations that are gone
// into whole namespaces and individual tools (Rust `render_diff` removal
// computation, #51202). A removed namespace subsumes its members, so the tool
// list carries qualified `namespace.member` names only for members whose
// namespace survives.
func removedTopLevelDeclarations(previous map[string]string, current map[string]string) ([]string, []string) {
	namespaceSet := map[string]struct{}{}
	for key := range previous {
		namespace, _, ok := strings.Cut(key, ".")
		if !ok {
			continue
		}
		if _, declared := previous[namespace]; !declared {
			continue
		}
		if _, stillDeclared := current[namespace]; stillDeclared {
			continue
		}
		namespaceSet[namespace] = struct{}{}
	}
	namespaces := make([]string, 0, len(namespaceSet))
	for namespace := range namespaceSet {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)

	tools := []string{}
	for key := range previous {
		if _, stillDeclared := current[key]; stillDeclared {
			continue
		}
		if _, removed := namespaceSet[key]; removed {
			continue
		}
		if namespace, _, ok := strings.Cut(key, "."); ok {
			if _, removed := namespaceSet[namespace]; removed {
				continue
			}
		}
		tools = append(tools, key)
	}
	sort.Strings(tools)
	return namespaces, tools
}

func topLevelDefinitionName(definition any) string {
	object, ok := definition.(map[string]any)
	if !ok {
		return ""
	}
	if name, ok := object["name"].(string); ok && strings.TrimSpace(name) != "" {
		return name
	}
	if kind, ok := object["type"].(string); ok {
		return kind
	}
	return ""
}

func topLevelDefinitionDescription(definition any) string {
	object, ok := definition.(map[string]any)
	if !ok {
		return ""
	}
	description, _ := object["description"].(string)
	return description
}

func topLevelDefinitionMembers(definition any) ([]any, bool) {
	object, ok := definition.(map[string]any)
	if !ok {
		return nil, false
	}
	members, ok := object["tools"].([]any)
	return members, ok
}

func cloneJSONWithoutKey(definition any, key string) (map[string]any, error) {
	normalized, err := normalizeJSONValue(definition)
	if err != nil {
		return nil, err
	}
	object, ok := normalized.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("responses lite tool declaration is not an object")
	}
	delete(object, key)
	return object, nil
}

func cloneAnyMapDeep(value map[string]any) map[string]any {
	normalized, err := normalizeJSONValue(value)
	if err != nil {
		return value
	}
	object, ok := normalized.(map[string]any)
	if !ok {
		return value
	}
	return object
}

func normalizeJSONValue(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// worldStateJSONHash returns the stable fingerprint Rust computes with
// `WorldStateHash::from_json`: the JSON serialization with every object's keys
// sorted, SHA-1 in lowercase hex. Go's encoding/json already emits object keys
// in sorted order, and HTML escaping is disabled so the bytes match the plain
// JSON Rust hashes.
func worldStateJSONHash(value any) string {
	normalized, err := normalizeJSONValue(value)
	if err != nil {
		normalized = value
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(normalized); err != nil {
		return ""
	}
	encoded := bytes.TrimRight(buffer.Bytes(), "\n")
	sum := sha1.Sum(encoded)
	return hex.EncodeToString(sum[:])
}

func hashComponent(hasher interface{ Write([]byte) (int, error) }, value string) {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	var length [8]byte
	size := uint64(len(value))
	for i := 7; i >= 0; i-- {
		length[i] = byte(size)
		size >>= 8
	}
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write([]byte(value))
}
