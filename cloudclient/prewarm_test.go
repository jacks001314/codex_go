package cloudclient

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func prewarmTestResponse(includeSandbox bool) []byte {
	var response []byte
	var profile []byte
	profile = appendActivationString(profile, 1, "base")
	profile = appendActivationString(profile, 2, "profile-1")
	response = appendActivationBytes(response, 1, profile)
	var approval []byte
	approval = appendActivationVarint(approval, 1, 2)
	response = appendActivationBytes(response, 2, approval)
	response = appendActivationVarint(response, 3, 1)
	response = appendActivationString(response, 4, "/workspace")
	response = appendActivationString(response, 7, "gpt-5")
	response = appendActivationString(response, 8, "openai")
	response = appendActivationString(response, 10, "high")
	response = appendActivationStringList(response, 11, []string{"/workspace"})
	if includeSandbox {
		var sandbox []byte
		sandbox = appendActivationBytes(sandbox, 2, []byte{8, 1})
		response = appendActivationBytes(response, 12, sandbox)
	}
	var serviceTier []byte
	serviceTier = appendActivationString(serviceTier, 1, "fast")
	response = appendActivationBytes(response, 13, serviceTier)
	response = appendActivationBytes(response, 14, activationTestThread("thread-1"))
	return response
}

func TestPrewarmDefaultThreadResolvesManagedSettingsLikeRust(t *testing.T) {
	activation, err := decodePrewarmResponse(prewarmTestResponse(true))
	if err != nil {
		t.Fatalf("decodePrewarmResponse() error = %v", err)
	}
	if activation.Thread.ID != "thread-1" || activation.Model != "gpt-5" || activation.ModelProvider != "openai" || activation.CWD != "/workspace" {
		t.Fatalf("activation = %#v", activation)
	}
	if activation.ServiceTier == nil || *activation.ServiceTier != "fast" || activation.ReasoningEffort == nil || *activation.ReasoningEffort != "high" {
		t.Fatalf("optional settings = %#v", activation)
	}
	sandbox, ok := activation.Sandbox.(map[string]any)
	if !ok || sandbox["type"] != "readOnly" || sandbox["networkAccess"] != true {
		t.Fatalf("sandbox = %#v", activation.Sandbox)
	}
	if len(activation.Thread.Turns) != 0 {
		t.Fatalf("turns = %#v", activation.Thread.Turns)
	}
	if _, err := decodePrewarmResponse(prewarmTestResponse(false)); err == nil {
		t.Fatal("missing sandbox was accepted")
	}
}

func TestPrewarmRequestUsesManagedEnvironmentAndOptionalModelLikeRust(t *testing.T) {
	fields, err := parseWireFields(encodePrewarmRequest("", ""))
	if err != nil {
		t.Fatalf("parseWireFields(default) error = %v", err)
	}
	environmentBytes, ok := fieldBytes(fields, 10)
	if !ok {
		t.Fatal("default request omitted environments")
	}
	environmentFields, err := parseWireFields(environmentBytes)
	if err != nil {
		t.Fatalf("parse environments: %v", err)
	}
	selection, ok := firstField(environmentFields, 1)
	if !ok || selection.typ != protowire.BytesType {
		t.Fatalf("selection = %#v", selection)
	}
	selectionFields, err := parseWireFields(selection.bytes)
	if err != nil {
		t.Fatalf("parse selection: %v", err)
	}
	if managed, ok := firstField(selectionFields, 1); !ok || managed.typ != protowire.BytesType || len(managed.bytes) != 0 {
		t.Fatalf("managed environment = %#v", managed)
	}
	if _, ok := fieldBytes(fields, 15); ok {
		t.Fatal("default request unexpectedly selected a model")
	}
	explicit, err := parseWireFields(encodePrewarmRequest("m", "p"))
	if err != nil {
		t.Fatalf("parseWireFields(explicit) error = %v", err)
	}
	if model, _ := fieldBytes(explicit, 15); string(model) != "m" {
		t.Fatalf("model = %q", model)
	}
	if provider, _ := fieldBytes(explicit, 16); string(provider) != "p" {
		t.Fatalf("provider = %q", provider)
	}
	plugins, ok := fieldBytes(fields, 28)
	if !ok {
		t.Fatal("prewarm omitted Codex Apps plugins")
	}
	pluginFields, err := parseWireFields(plugins)
	if err != nil {
		t.Fatalf("parse plugins: %v", err)
	}
	if sku, _ := fieldBytes(pluginFields, 1); string(sku) != "codex" {
		t.Fatalf("plugins productSku = %q", sku)
	}
}

func wireBytesFields(fields []wireField, number protowire.Number) [][]byte {
	out := make([][]byte, 0, 1)
	for _, field := range fields {
		if field.number == number && field.typ == protowire.BytesType {
			out = append(out, field.bytes)
		}
	}
	return out
}

// Rust #60007/#60309: a selected project encodes the managed environment
// overrides and defers executor readiness; the default request omits both.
func TestPrewarmRequestIncludesProjectEnvironmentAndDeferredFlagLikeRust(t *testing.T) {
	project := &ProjectEnvironment{
		Name:       "repo",
		OriginURLs: []string{"https://github.com/org/repo"},
		CWD:        "/workspace/org/repo",
	}
	fields, err := parseWireFields(encodePrewarmRequestWithProject("", "", project))
	if err != nil {
		t.Fatalf("parse project request: %v", err)
	}
	deferred, ok := firstField(fields, 29)
	if !ok || deferred.typ != protowire.VarintType || deferred.value != 1 {
		t.Fatalf("deferred_environment = %#v", deferred)
	}
	environments := wireBytesFields(fields, 10)
	if len(environments) != 1 {
		t.Fatalf("environments = %d", len(environments))
	}
	environmentFields, err := parseWireFields(environments[0])
	if err != nil {
		t.Fatalf("parse environments: %v", err)
	}
	selections := wireBytesFields(environmentFields, 1)
	if len(selections) != 1 {
		t.Fatalf("selections = %d", len(selections))
	}
	selectionFields, err := parseWireFields(selections[0])
	if err != nil {
		t.Fatalf("parse selection: %v", err)
	}
	manageds := wireBytesFields(selectionFields, 1)
	if len(manageds) != 1 {
		t.Fatalf("managed environments = %d", len(manageds))
	}
	managedFields, err := parseWireFields(manageds[0])
	if err != nil {
		t.Fatalf("parse managed environment: %v", err)
	}
	if id, _ := fieldBytes(managedFields, 1); string(id) != "asenvcfg_base" {
		t.Fatalf("environment config id = %q", id)
	}
	if cwd, _ := fieldBytes(managedFields, 2); string(cwd) != "/workspace/org/repo" {
		t.Fatalf("managed cwd = %q", cwd)
	}
	overridesBytes := wireBytesFields(managedFields, 3)
	if len(overridesBytes) != 1 {
		t.Fatalf("overrides = %d", len(overridesBytes))
	}
	overrides, err := parseWireFields(overridesBytes[0])
	if err != nil {
		t.Fatalf("parse overrides: %v", err)
	}
	if name, _ := fieldBytes(overrides, 2); string(name) != "repo" {
		t.Fatalf("overrides name = %q", name)
	}
	repositoriesBytes := wireBytesFields(overrides, 1)
	if len(repositoriesBytes) != 1 {
		t.Fatalf("repositories = %d", len(repositoriesBytes))
	}
	repositories, err := parseWireFields(repositoriesBytes[0])
	if err != nil {
		t.Fatalf("parse repositories: %v", err)
	}
	values := wireBytesFields(repositories, 1)
	if len(values) != 1 {
		t.Fatalf("repository values = %d", len(values))
	}
	valueFields, err := parseWireFields(values[0])
	if err != nil {
		t.Fatalf("parse repository override: %v", err)
	}
	if origin, _ := fieldBytes(valueFields, 5); string(origin) != "https://github.com/org/repo" {
		t.Fatalf("origin url = %q", origin)
	}

	defaultFields, err := parseWireFields(encodePrewarmRequest("m", "p"))
	if err != nil {
		t.Fatalf("parse default request: %v", err)
	}
	if deferred, ok := firstField(defaultFields, 29); ok {
		t.Fatalf("default request set deferred_environment = %#v", deferred)
	}
}
