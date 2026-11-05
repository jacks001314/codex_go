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
}
