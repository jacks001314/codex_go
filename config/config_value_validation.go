package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ConfigValueError is the serde rejection Rust raises while it deserializes
// ConfigToml or the mcp server table: the message, then the dotted config path
// on the following line, which anyhow indents under its `Caused by:` block.
type ConfigValueError struct {
	message string
	path    string
}

func (e *ConfigValueError) Error() string {
	if e.path == "" {
		return e.message
	}
	return e.message + "\nin `" + e.path + "`"
}

// ConfigDocumentLocator carries the layer text being validated, so the two
// fields whose serde errors quote a TOML position can name the file, line and
// column the way Rust does.
type ConfigDocumentLocator struct {
	Path string
	Text string
}

// ValidateConfigValues reports the first field serde would reject, mirroring
// ConfigToml's deserializer so a document the Rust CLI refuses at bootstrap is
// refused here too instead of being silently accepted by the untyped loader.
func ValidateConfigValues(values map[string]any) error {
	return ValidateConfigDocumentValues(values, nil)
}

// ValidateConfigDocumentValues is ValidateConfigValues with the source document
// available for the position-reporting fields.
func ValidateConfigDocumentValues(values map[string]any, document *ConfigDocumentLocator) error {
	for _, field := range sortedConfigKeys(values) {
		kind, ok := configTomlFields[field]
		if !ok {
			continue
		}
		if err := validateConfigFieldValue(kind, values[field], field, false); err != nil {
			if contextErr, ok := err.(*ConfigValueError); ok && document != nil {
				if field == "shell_environment_policy" {
					if positioned := document.shellEnvironmentPolicyPosition(contextErr); positioned != nil {
						return positioned
					}
				}
				if kind.positional {
					if positioned := document.positionalConfigError(contextErr, field, kind.dualPositional); positioned != nil {
						return positioned
					}
				}
			}
			return err
		}
	}
	if features, ok := values["features"]; ok {
		// FeaturesToml is a map of booleans and serde reports the child failure
		// against the table itself, not against `features.<name>`.
		if table, ok := features.(map[string]any); ok {
			for _, name := range sortedConfigKeys(table) {
				if _, ok := table[name].(bool); !ok {
					return configInvalidType(table[name], "a boolean", "features")
				}
			}
		}
	}
	if servers, ok := values["mcp_servers"]; ok {
		return validateMCPServersTable(servers, "mcp_servers")
	}
	if providers, ok := values["model_providers"].(map[string]any); ok {
		return validateModelProvidersSemantics(providers)
	}
	return nil
}

// configChatWireAPIRemovedMessage is the wire_api rejection Rust raises from
// WireApi's deserializer.
const configChatWireAPIRemovedMessage = "`wire_api = \"chat\"` is no longer supported.\n" +
	"How to fix: set `wire_api = \"responses\"` in your provider config.\n" +
	"More info: https://github.com/openai/codex/discussions/7782"

// reservedConfigModelProviderIDs are the built-in providers a user may not
// override. `amazon-bedrock`, `amazon-bedrock-runtime` and `gcp` are exempt.
var reservedConfigModelProviderIDs = []string{"lmstudio", "ollama", "openai"}

// validateModelProvidersSemantics replays Rust's validate_model_providers, which
// runs after serde has read every entry and reports its failures against the
// whole table.
func validateModelProvidersSemantics(providers map[string]any) error {
	var conflicts []string
	for _, key := range sortedConfigKeys(providers) {
		provider, ok := providers[key].(map[string]any)
		if !ok {
			continue
		}
		if key == "openai" {
			onlyCatalogURL := true
			for name := range provider {
				if name != "model_catalog_url" {
					onlyCatalogURL = false
					break
				}
			}
			if onlyCatalogURL {
				continue
			}
		}
		for _, reserved := range reservedConfigModelProviderIDs {
			if key == reserved {
				conflicts = append(conflicts, "`"+key+"`")
				break
			}
		}
	}
	if len(conflicts) > 0 {
		return &ConfigValueError{
			message: "model_providers contains reserved built-in provider IDs: " + strings.Join(conflicts, ", ") +
				". Built-in providers cannot be overridden. Rename your custom provider (for example, `openai-custom`).",
			path: "model_providers",
		}
	}
	for _, key := range sortedConfigKeys(providers) {
		provider, ok := providers[key].(map[string]any)
		if !ok {
			continue
		}
		switch key {
		case "openai", "amazon-bedrock", "amazon-bedrock-runtime", "gcp":
			continue
		}
		name, _ := provider["name"].(string)
		if strings.TrimSpace(name) == "" {
			return &ConfigValueError{
				message: "model_providers." + key + ": provider name must not be empty",
				path:    "model_providers",
			}
		}
	}
	return nil
}

// ValidateMCPServersValues validates only the mcp server table, the narrower
// load `codex mcp remove` performs. Rust reads the raw TOML into the server map
// there and ignores unrelated top-level fields.
func ValidateMCPServersValues(values map[string]any) error {
	servers, ok := values["mcp_servers"]
	if !ok {
		return nil
	}
	return validateMCPServersTable(servers, "")
}

// mcpServerFieldKinds lists the McpServerConfig fields in the order serde
// walks them: the flattened transport first, then the struct itself.
var mcpServerFieldKinds = []struct {
	name string
	kind configFieldKind
}{
	{"command", configFieldKind{kind: configKindString}},
	{"args", configFieldKind{kind: configKindSequence}},
	{"env", configFieldKind{kind: configKindMap}},
	{"env_vars", configFieldKind{kind: configKindSequence}},
	{"cwd", configFieldKind{kind: configKindString}},
	{"url", configFieldKind{kind: configKindString}},
	{"bearer_token_env_var", configFieldKind{kind: configKindString}},
	{"http_headers", configFieldKind{kind: configKindMap}},
	{"env_http_headers", configFieldKind{kind: configKindMap}},
	{"http_headers_helper", configFieldKind{kind: configKindString}},
	{"auth", configFieldKind{kind: configKindEnum, variants: []string{"oauth", "chatgpt", "ema_auth"}}},
	{"environment_id", configFieldKind{kind: configKindString}},
	{"enabled", configFieldKind{kind: configKindBool}},
	{"required", configFieldKind{kind: configKindBool}},
	{"startup_readiness", configFieldKind{kind: configKindEnum, variants: []string{"connection", "catalog"}}},
	{"supports_parallel_tool_calls", configFieldKind{kind: configKindBool}},
	{"tool_input_schema_max_bytes", configFieldKind{kind: configKindInteger, expected: "a nonzero usize"}},
	{"omit_tools_from", configFieldKind{kind: configKindSequence}},
	{"startup_timeout_sec", configFieldKind{kind: configKindFloat, expected: "f64"}},
	{"tool_timeout_sec", configFieldKind{kind: configKindFloat, expected: "f64"}},
	{"default_tools_approval_mode", configFieldKind{kind: configKindEnum, variants: []string{"auto", "prompt", "writes", "approve"}}},
	{"enabled_tools", configFieldKind{kind: configKindSequence}},
	{"disabled_tools", configFieldKind{kind: configKindSequence}},
	{"scopes", configFieldKind{kind: configKindSequence}},
	{"oauth", configFieldKind{kind: configKindStruct, expected: "McpServerOAuthConfig"}},
	{"oauth_resource", configFieldKind{kind: configKindString}},
	{"tools", configFieldKind{kind: configKindMap}},
}

// validateMCPServersTable walks the server map. base is the path prefix the
// caller's deserializer reports: `mcp_servers` for the bootstrap load and an
// empty string for `codex mcp remove`, which decodes the bare server map.
func validateMCPServersTable(value any, base string) error {
	servers, ok := value.(map[string]any)
	if !ok {
		return configInvalidType(value, "a map", joinConfigPath(base, "mcp_servers"))
	}
	for _, name := range sortedConfigKeys(servers) {
		path := joinConfigPath(base, "mcp_servers."+name)
		table, ok := servers[name].(map[string]any)
		if !ok {
			return configInvalidType(servers[name], "struct RawMcpServerConfig", path)
		}
		if err := validateMCPServerValue(table, path); err != nil {
			return err
		}
	}
	return nil
}

func joinConfigPath(base string, path string) string {
	if base == "" {
		return strings.TrimPrefix(path, "mcp_servers.")
	}
	return path
}

func validateMCPServerValue(table map[string]any, path string) error {
	for _, field := range mcpServerFieldKinds {
		value, ok := table[field.name]
		if !ok {
			continue
		}
		if err := validateConfigFieldValue(field.kind, value, path+"."+field.name, false); err != nil {
			return err
		}
	}
	for _, name := range []string{"env", "env_vars", "http_headers", "env_http_headers"} {
		children, ok := table[name].(map[string]any)
		if !ok {
			continue
		}
		for _, key := range sortedConfigKeys(children) {
			if _, ok := children[key].(string); !ok {
				return configInvalidType(children[key], "a string", path+"."+name+"."+key)
			}
		}
	}
	_, hasCommand := table["command"]
	_, hasURL := table["url"]
	if hasCommand {
		// The flattened transport is stdio as soon as `command` is set, so every
		// HTTP-only field is rejected against the server table itself.
		for _, field := range []string{"url", "bearer_token_env_var", "http_headers", "env_http_headers", "http_headers_helper", "auth", "oauth_resource"} {
			if _, ok := table[field]; ok {
				return &ConfigValueError{message: field + " is not supported for stdio", path: path}
			}
		}
		return nil
	}
	if hasURL {
		return nil
	}
	return &ConfigValueError{message: "invalid transport", path: path}
}

func validateConfigFieldValue(kind configFieldKind, value any, path string, collapse bool) error {
	switch kind.kind {
	case configKindString:
		if _, ok := value.(string); !ok {
			return configInvalidType(value, "a string", path)
		}
	case configKindPathString:
		if _, ok := value.(string); !ok {
			return configInvalidType(value, "path string", path)
		}
	case configKindBool:
		if _, ok := value.(bool); !ok {
			return configInvalidType(value, "a boolean", path)
		}
	case configKindInteger:
		if !isConfigInteger(value) {
			return configInvalidType(value, kind.expected, path)
		}
	case configKindFloat:
		switch value.(type) {
		case float64, int64, int:
			// Duration seconds accept an integer literal as f64.
		default:
			return configInvalidType(value, kind.expected, path)
		}
	case configKindSequence:
		if _, ok := value.([]any); !ok {
			return configInvalidType(value, "a sequence", path)
		}
	case configKindMap:
		if _, ok := value.(map[string]any); !ok {
			return configInvalidType(value, "a map", path)
		}
	case configKindStruct:
		table, ok := value.(map[string]any)
		if !ok {
			return configInvalidType(value, "struct "+kind.expected, path)
		}
		return validateConfigStructFields(kind, table, path, collapse)
	case configKindStructMap:
		table, ok := value.(map[string]any)
		if !ok {
			return configInvalidType(value, "a map", path)
		}
		for _, name := range sortedConfigKeys(table) {
			entryPath := configChildPath(collapse, path, name)
			entry, ok := table[name].(map[string]any)
			if !ok {
				return configInvalidType(table[name], "struct "+kind.expected, entryPath)
			}
			if err := validateConfigStructFields(kind, entry, entryPath, collapse); err != nil {
				return err
			}
		}
	case configKindStructSequence:
		items, ok := value.([]any)
		if !ok {
			return configInvalidType(value, "a sequence", path)
		}
		for _, item := range items {
			table, ok := item.(map[string]any)
			if !ok {
				return configInvalidType(item, "struct "+kind.expected, path)
			}
			if err := validateConfigStructFields(kind, table, path, collapse); err != nil {
				return err
			}
		}
	case configKindPathSequence:
		items, ok := value.([]any)
		if !ok {
			return configInvalidType(value, "a sequence", path)
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return configInvalidType(item, "path string", path)
			}
		}
	case configKindStringMapOfMaps:
		table, ok := value.(map[string]any)
		if !ok {
			return configInvalidType(value, "a map", path)
		}
		for _, name := range sortedConfigKeys(table) {
			innerPath := configChildPath(collapse, path, name)
			inner, ok := table[name].(map[string]any)
			if !ok {
				return configInvalidType(table[name], "a map", innerPath)
			}
			for _, key := range sortedConfigKeys(inner) {
				if _, ok := inner[key].(string); !ok {
					return configInvalidType(inner[key], "a string", configChildPath(collapse, innerPath, key))
				}
			}
		}
	case configKindValueMap:
		table, ok := value.(map[string]any)
		if !ok {
			return configInvalidType(value, "a map", path)
		}
		for _, name := range sortedConfigKeys(table) {
			if err := validateConfigFieldValue(*kind.entry, table[name], configChildPath(collapse, path, name), collapse); err != nil {
				return err
			}
		}
	case configKindUntaggedStruct:
		if kind.allowBool {
			if _, ok := value.(bool); ok {
				return nil
			}
		}
		table, ok := value.(map[string]any)
		if !ok {
			return configUntaggedEnumError(kind.expected, path)
		}
		for _, name := range sortedConfigKeys(table) {
			field, ok := kind.fields[name]
			if !ok {
				continue
			}
			if err := validateConfigFieldValue(field, table[name], appendConfigPath(path, name), false); err != nil {
				return configUntaggedEnumError(kind.expected, path)
			}
		}
	case configKindTaggedEnum:
		if _, ok := value.(map[string]any); !ok {
			return configInvalidType(value, "internally tagged enum "+kind.expected, path)
		}
	case configKindStringMap:
		table, ok := value.(map[string]any)
		if !ok {
			return configInvalidType(value, "a map", path)
		}
		for _, name := range sortedConfigKeys(table) {
			if _, ok := table[name].(string); !ok {
				return configInvalidType(table[name], "a string", configChildPath(collapse, path, name))
			}
		}
	case configKindEnumMap:
		table, ok := value.(map[string]any)
		if !ok {
			return configInvalidType(value, "a map", path)
		}
		for _, name := range sortedConfigKeys(table) {
			if err := validateConfigEnumValue(kind, table[name], configChildPath(collapse, path, name)); err != nil {
				return err
			}
		}
	case configKindStringSequence:
		items, ok := value.([]any)
		if !ok {
			return configInvalidType(value, "a sequence", path)
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return configInvalidType(item, "a string", path)
			}
		}
	case configKindUsize, configKindU64, configKindU32, configKindI32:
		expected := configIntegerExpected(kind)
		if !isConfigInteger(value) {
			return configInvalidType(value, expected, path)
		}
		if count := configIntegerValue(value); !configIntegerInRange(kind.kind, count) {
			return &ConfigValueError{message: fmt.Sprintf("invalid value: integer `%d`, expected %s", count, expected), path: path}
		}
	case configKindNonZeroUsize, configKindNonZeroU64:
		expected := configIntegerExpected(kind)
		if !isConfigInteger(value) {
			return configInvalidType(value, expected, path)
		}
		if count := configIntegerValue(value); count <= 0 {
			return &ConfigValueError{message: fmt.Sprintf("invalid value: integer `%d`, expected %s", count, expected), path: path}
		}
	case configKindWireAPI:
		name, ok := value.(string)
		if !ok {
			return configInvalidType(value, "a string", path)
		}
		if name == "chat" {
			return &ConfigValueError{message: configChatWireAPIRemovedMessage, path: path}
		}
		if name != "responses" {
			return &ConfigValueError{message: "unknown variant `" + name + "`, expected `responses`", path: path}
		}
	case configKindEnum:
		return validateConfigEnumValue(kind, value, path)
	case configKindUntaggedEnum:
		return validateConfigUntaggedEnumValue(kind, value, path)
	}
	return nil
}

// validateConfigStructFields descends into a struct's own fields in the order
// serde walks them. Unknown keys stay allowed, as they do in Rust.
func validateConfigStructFields(kind configFieldKind, table map[string]any, path string, collapse bool) error {
	if kind.boolValues {
		for _, name := range sortedConfigKeys(table) {
			if _, ok := table[name].(bool); !ok {
				return configInvalidType(table[name], "a boolean", path)
			}
		}
		return nil
	}
	// serde reads the named fields first and the flattened ones afterwards, so a
	// bad `tui.effects.starfield` wins over a bad `tui.notifications`.
	for pass := 0; pass < 2; pass++ {
		for _, name := range sortedConfigKeys(table) {
			field, ok := kind.fields[name]
			if !ok || field.flattenPath != (pass == 1) {
				continue
			}
			fieldName := name
			if field.pathName != "" {
				fieldName = field.pathName
			}
			fieldPath := configChildPath(collapse, path, fieldName)
			childCollapse := collapse
			if field.flattenPath {
				fieldPath = path
				childCollapse = true
			}
			if err := validateConfigFieldValue(field, table[name], fieldPath, childCollapse); err != nil {
				return err
			}
		}
	}
	if kind.flattenEntries {
		// A `#[serde(flatten)]` map owns every key the named fields did not
		// claim, and serde reports its failures against this table.
		for _, name := range sortedConfigKeys(table) {
			if _, ok := kind.fields[name]; ok {
				continue
			}
			if err := validateConfigFieldValue(*kind.entry, table[name], path, true); err != nil {
				return err
			}
		}
	}
	for _, name := range kind.required {
		if _, ok := table[name]; !ok {
			return &ConfigValueError{message: "missing field `" + name + "`", path: path}
		}
	}
	return nil
}

func appendConfigPath(base string, name string) string {
	if base == "" {
		return name
	}
	return base + "." + name
}

// configChildPath keeps a flattened entry's descendants on the enclosing
// table's path: serde reports every failure inside a `#[serde(flatten)]` map
// against the table that holds it.
func configChildPath(collapse bool, base string, name string) string {
	if collapse {
		return base
	}
	return appendConfigPath(base, name)
}

func configIntegerExpected(kind configFieldKind) string {
	if kind.expected != "" {
		return kind.expected
	}
	switch kind.kind {
	case configKindU32:
		return "u32"
	case configKindU64:
		return "u64"
	case configKindI32:
		return "i32"
	case configKindNonZeroUsize:
		return "a nonzero usize"
	case configKindNonZeroU64:
		return "a nonzero u64"
	}
	return kind.expected
}

func configIntegerValue(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	}
	return 0
}

func configIntegerInRange(kind configValueKind, value int64) bool {
	switch kind {
	case configKindUsize, configKindU64:
		return value >= 0
	case configKindU32:
		return value >= 0 && value <= 4294967295
	case configKindI32:
		return value >= -2147483648 && value <= 2147483647
	}
	return true
}

// validateConfigUntaggedEnumValue mirrors serde's error for an untagged repr
// (`forced_chatgpt_workspace_id`), which reports one "did not match any
// variant" message instead of a type mismatch.
func validateConfigUntaggedEnumValue(kind configFieldKind, value any, path string) error {
	if _, ok := value.(string); ok && kind.allowString {
		return nil
	}
	if _, ok := value.(bool); ok && kind.allowBool {
		return nil
	}
	if kind.nonzeroU16 {
		if count, ok := value.(int64); ok && count >= 1 && count <= 65535 {
			return nil
		}
		if items, ok := value.([]any); ok {
			allChannels := true
			for _, item := range items {
				count, ok := item.(int64)
				if !ok || count < 1 || count > 65535 {
					allChannels = false
					break
				}
			}
			if allChannels {
				return nil
			}
		}
		return configUntaggedEnumError(kind.expected, path)
	}
	if items, ok := value.([]any); ok {
		allStrings := true
		for _, item := range items {
			if _, ok := item.(string); !ok {
				allStrings = false
				break
			}
		}
		if allStrings {
			return nil
		}
	}
	return configUntaggedEnumError(kind.expected, path)
}

func configUntaggedEnumError(name string, path string) error {
	if name == "" {
		name = "Repr"
	}
	return &ConfigValueError{message: "data did not match any variant of untagged enum " + name, path: path}
}

func validateConfigEnumValue(kind configFieldKind, value any, path string) error {
	name, isString := value.(string)
	var table map[string]any
	if !isString {
		// serde reads a non-string value as an externally tagged variant, so an
		// inline table names a variant while every other type is a unit variant.
		var isTable bool
		table, isTable = value.(map[string]any)
		if !isTable {
			if kind.stringOrMap {
				return configInvalidType(value, "string or map", path)
			}
			return &ConfigValueError{message: "invalid type: unit variant, expected string only", path: path}
		}
		keys := sortedConfigKeys(table)
		if len(keys) == 0 {
			if kind.stringOrMap {
				return &ConfigValueError{message: "invalid value: map, expected map with a single key", path: path}
			}
			return &ConfigValueError{message: "invalid type: unit variant, expected string only", path: path}
		}
		name = keys[0]
	}
	if structName, isStruct := kind.structVariants[name]; isStruct {
		if isString {
			return &ConfigValueError{message: "invalid type: unit variant, expected struct variant", path: path}
		}
		payload, isTable := table[name].(map[string]any)
		if !isTable {
			return configInvalidType(table[name], "struct variant "+structName, path)
		}
		for _, required := range kind.structVariantRequired {
			if _, ok := payload[required]; !ok {
				return &ConfigValueError{message: "missing field `" + required + "`", path: path}
			}
		}
		return nil
	}
	for _, variant := range kind.variants {
		if name == variant {
			return nil
		}
	}
	return &ConfigValueError{message: "unknown variant `" + name + "`, " + describeConfigVariants(kind.variants), path: path}
}

// describeConfigVariants renders serde's expected-variant list: a two-variant
// enum reads `expected \`a\` or \`b\“ and longer ones read
// `expected one of \`a\`, \`b\`, \`c\“.
func describeConfigVariants(variants []string) string {
	if len(variants) == 2 {
		return "expected `" + variants[0] + "` or `" + variants[1] + "`"
	}
	quoted := make([]string, len(variants))
	for index, variant := range variants {
		quoted[index] = "`" + variant + "`"
	}
	return "expected one of " + strings.Join(quoted, ", ")
}

func configInvalidType(value any, expected, path string) *ConfigValueError {
	return &ConfigValueError{message: "invalid type: " + describeConfigValue(value) + ", expected " + expected, path: path}
}

// describeConfigValue renders serde's `Unexpected` spelling for a decoded TOML
// value.
func describeConfigValue(value any) string {
	switch typed := value.(type) {
	case string:
		return fmt.Sprintf("string %q", typed)
	case bool:
		return fmt.Sprintf("boolean `%t`", typed)
	case int64:
		return fmt.Sprintf("integer `%d`", typed)
	case int:
		return fmt.Sprintf("integer `%d`", typed)
	case float64:
		return "floating point `" + describeConfigFloat(typed) + "`"
	case []any:
		return "sequence"
	case map[string]any:
		return "map"
	case nil:
		return "unit"
	}
	return fmt.Sprintf("%T", value)
}

func describeConfigFloat(value float64) string {
	text := strconv.FormatFloat(value, 'g', -1, 64)
	if !strings.ContainsAny(text, ".eE") {
		text += ".0"
	}
	return text
}

func isConfigInteger(value any) bool {
	switch value.(type) {
	case int64, int:
		return true
	}
	return false
}

func sortedConfigKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// positionalConfigError renders serde's position-carrying form for `projects`
// and `project_root_markers`. The dual form keeps the inner serde error as a
// second anyhow cause, which is why the path line sits under it.
func (d *ConfigDocumentLocator) positionalConfigError(base *ConfigValueError, field string, dual bool) error {
	line, column, ok := tomlFieldValuePosition(d.Text, field)
	if !ok {
		return nil
	}
	location := fmt.Sprintf("%s:%d:%d: %s", d.Path, line, column, base.message)
	if !dual {
		return &ConfigValueError{message: location, path: ""}
	}
	// anyhow aligns the continuation of a numbered cause under its text, so the
	// path line of the inner serde error carries three extra spaces.
	return &ConfigValueError{message: "0: " + location + "\n1: " + base.message + "\n   in `" + field + "`"}
}

// shellEnvironmentPolicyPosition renders the three filter fields the way their
// custom deserializer does: the position of the `[shell_environment_policy]`
// header, then the bare message.
func (d *ConfigDocumentLocator) shellEnvironmentPolicyPosition(base *ConfigValueError) error {
	for _, field := range []string{"exclude", "include_only", "filters"} {
		prefix := "shell_environment_policy." + field
		if base.path != prefix && !strings.HasPrefix(base.path, prefix+".") {
			continue
		}
		line, column, ok := tomlTableHeaderPosition(d.Text, "shell_environment_policy")
		if !ok {
			return nil
		}
		return &ConfigValueError{message: fmt.Sprintf("%s:%d:%d: %s", d.Path, line, column, base.message)}
	}
	return nil
}

// tomlTableHeaderPosition reports where a `[table]` header starts, as a 1-based
// line and the column of its opening bracket.
func tomlTableHeaderPosition(text string, table string) (int, int, bool) {
	offset := 0
	for _, raw := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimLeft(raw, " \t")
		if strings.HasPrefix(trimmed, "["+table+"]") || strings.HasPrefix(trimmed, "[["+table+"]]") {
			return lineColumnAt(text, offset+len(raw)-len(trimmed))
		}
		offset += len(raw)
	}
	return 0, 0, false
}

// tomlFieldValuePosition reports where a top-level `field = value` puts its
// value, as a 1-based line and column.
func tomlFieldValuePosition(text string, field string) (int, int, bool) {
	offset := 0
	inTable := false
	for _, raw := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimLeft(raw, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			offset += len(raw)
			continue
		}
		if strings.HasPrefix(trimmed, "[[") {
			// An array-of-tables header puts the value at the `[` of its line,
			// which is how serde reports `[[projects]]`.
			if end := strings.Index(trimmed, "]]"); end > 0 && strings.TrimSpace(trimmed[2:end]) == field {
				return lineColumnAt(text, offset+len(raw)-len(trimmed))
			}
			inTable = true
			offset += len(raw)
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inTable = true
			offset += len(raw)
			continue
		}
		if inTable || !strings.HasPrefix(trimmed, field) {
			offset += len(raw)
			continue
		}
		rest := trimmed[len(field):]
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, "=") {
			offset += len(raw)
			continue
		}
		rest = rest[1:]
		valueStart := len(raw) - len(rest)
		for valueStart < len(raw) && (raw[valueStart] == ' ' || raw[valueStart] == '\t') {
			valueStart++
		}
		return lineColumnAt(text, offset+valueStart)
	}
	return 0, 0, false
}

func lineColumnAt(text string, offset int) (int, int, bool) {
	if offset < 0 || offset > len(text) {
		return 0, 0, false
	}
	line := 1 + strings.Count(text[:offset], "\n")
	lineStart := strings.LastIndexByte(text[:offset], '\n') + 1
	return line, offset - lineStart + 1, true
}
