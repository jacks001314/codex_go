package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// managedRestrictedPermissions is a restricted runtime permission profile, so
// the filesystem sandbox path stays active instead of short-circuiting.
const managedRestrictedPermissions = `{"type":"managed","file_system":{"type":"restricted","entries":[]},"network":"restricted"}`

// Mirrors Rust exec-server-protocol `protocol.rs` (#51786): the controller's
// sandbox override round-trips through the wire form in camelCase, the
// no-override value never appears on the wire, and a full exec request keeps the
// nested sandbox override.
func TestSandboxOverrideWireShapeLikeRust(t *testing.T) {
	cases := []struct {
		override SandboxOverride
		literal  string
	}{
		{SandboxOverrideNone, ""},
		{SandboxOverrideEscalatedWithRestrictions, `"escalatedSandboxWithRestrictions"`},
		{SandboxOverrideBypassFirstAttempt, `"bypassSandboxFirstAttempt"`},
	}
	for _, test := range cases {
		context := FileSystemSandboxContext{
			Permissions:     json.RawMessage(managedRestrictedPermissions),
			SandboxOverride: test.override,
		}
		data, err := json.Marshal(context)
		if err != nil {
			t.Fatalf("Marshal(%q) error = %v", test.override, err)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatalf("Unmarshal(wire) error = %v", err)
		}
		raw, present := wire["sandboxOverride"]
		switch {
		case test.literal == "" && present:
			t.Fatalf("no-override wire form kept sandboxOverride: %s", data)
		case test.literal != "" && (!present || string(raw) != test.literal):
			t.Fatalf("wire sandboxOverride = %s, want %s", raw, test.literal)
		}
		if got := context.SandboxOverride.IsNoOverride(); got != (test.override == SandboxOverrideNone) {
			t.Fatalf("IsNoOverride() = %v for %q", got, test.override)
		}

		var decoded FileSystemSandboxContext
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal(context) error = %v", err)
		}
		if decoded.SandboxOverride != test.override {
			t.Fatalf("decoded override = %q, want %q", decoded.SandboxOverride, test.override)
		}

		params := ExecParams{
			ProcessID: "override-1",
			Argv:      []string{"echo", "hi"},
			CWD:       t.TempDir(),
			Env:       map[string]string{},
			Sandbox:   data,
		}
		paramsData, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("Marshal(params) error = %v", err)
		}
		var roundTripped ExecParams
		if err := json.Unmarshal(paramsData, &roundTripped); err != nil {
			t.Fatalf("Unmarshal(params) error = %v", err)
		}
		var sandboxOnWire FileSystemSandboxContext
		if err := json.Unmarshal(roundTripped.Sandbox, &sandboxOnWire); err != nil {
			t.Fatalf("Unmarshal(params.sandbox) error = %v", err)
		}
		if sandboxOnWire.SandboxOverride != test.override {
			t.Fatalf("exec round trip override = %q, want %q", sandboxOnWire.SandboxOverride, test.override)
		}
	}
}

// Mirrors the legacy-wire half of the Rust test: a request that omits
// `sandboxOverride` deserializes to NoOverride instead of failing.
func TestSandboxOverrideDefaultsToNoOverrideLikeRust(t *testing.T) {
	legacy := `{"permissions":` + managedRestrictedPermissions + `,"cwd":"file:///workspace"}`
	var decoded FileSystemSandboxContext
	if err := json.Unmarshal([]byte(legacy), &decoded); err != nil {
		t.Fatalf("Unmarshal(legacy wire) error = %v", err)
	}
	if !decoded.SandboxOverride.IsNoOverride() {
		t.Fatalf("legacy wire override = %q, want no override", decoded.SandboxOverride)
	}
	if SandboxOverrideNone.IsNoOverride() != true {
		t.Fatal("SandboxOverrideNone must mirror Rust SandboxOverride::NoOverride")
	}
}

// Mirrors Rust's enum deserialization: only the camelCase spellings (and the
// absent field) are accepted, and an unknown spelling is rejected instead of
// silently falling back, with the same serde wording the sibling
// WindowsSandboxLevel parser uses.
func TestSandboxOverrideValidateMatchesSerdeLikeRust(t *testing.T) {
	for _, value := range []SandboxOverride{SandboxOverrideNone, SandboxOverrideEscalatedWithRestrictions, SandboxOverrideBypassFirstAttempt} {
		if err := value.Validate(); err != nil {
			t.Fatalf("Validate(%q) error = %v", value, err)
		}
	}
	want := "unknown variant `bogus`, expected one of `noOverride`, `escalatedSandboxWithRestrictions`, `bypassSandboxFirstAttempt`"
	if err := SandboxOverride("bogus").Validate(); err == nil || err.Error() != want {
		t.Fatalf("Validate(bogus) error = %v, want %q", err, want)
	}
	if err := SandboxOverride("NoOverride").Validate(); err == nil {
		t.Fatal("Validate(NoOverride) accepted the Rust variant name instead of the camelCase wire value")
	}
}

// Mirrors Rust `exec-server/src/fs_sandbox.rs` (#51786): the caller's override is
// carried through filesystem sandbox preparation, and an unknown spelling is
// invalid params before any helper runs.
func TestSandboxOverrideCarriedThroughFilesystemSandboxLikeRust(t *testing.T) {
	root := t.TempDir()
	for _, override := range []SandboxOverride{SandboxOverrideNone, SandboxOverrideEscalatedWithRestrictions, SandboxOverrideBypassFirstAttempt} {
		context := FileSystemSandboxContext{
			Permissions:     json.RawMessage(managedRestrictedPermissions),
			SandboxOverride: override,
			CWD:             root,
		}
		required, _, _, cwd, workspaceRoots, err := prepareFSSandbox(&context)
		if err != nil || !required {
			t.Fatalf("prepareFSSandbox(%q) = required %v, err %v", override, required, err)
		}
		if context.SandboxOverride != override {
			t.Fatalf("prepareFSSandbox(%q) override = %q", override, context.SandboxOverride)
		}
		if cwd == "" || len(workspaceRoots) != 1 {
			t.Fatalf("prepareFSSandbox(%q) cwd = %q, roots = %#v", override, cwd, workspaceRoots)
		}
	}

	invalid := FileSystemSandboxContext{
		Permissions:     json.RawMessage(managedRestrictedPermissions),
		SandboxOverride: SandboxOverride("bogus"),
		CWD:             root,
	}
	_, _, _, _, _, err := prepareFSSandbox(&invalid)
	var failure *requestFailure
	if !errors.As(err, &failure) || failure.code != -32602 ||
		!strings.Contains(failure.message, "file system sandbox context: unknown variant `bogus`") {
		t.Fatalf("prepareFSSandbox(bogus) error = %#v", err)
	}
}

// Mirrors the request boundary: a filesystem request whose sandbox context
// carries an unknown override is invalid params, exactly like the sibling
// Windows sandbox level gate.
func TestSandboxOverrideRejectedAtFilesystemRequestLikeRust(t *testing.T) {
	params := FSReadFileParams{
		Path: pathToURI(filepath.Join(t.TempDir(), "value.txt")),
		Sandbox: &FileSystemSandboxContext{
			Permissions:     json.RawMessage(managedRestrictedPermissions),
			SandboxOverride: SandboxOverride("escalated"),
			CWD:             pathToURI(t.TempDir()),
		},
	}
	_, err := NewServer().handleRequest(context.Background(), &request{
		Method: MethodFSReadFile,
		Params: mustMarshalRawMessage(t, params),
	})
	var failure *requestFailure
	if !errors.As(err, &failure) || failure.code != -32602 ||
		!strings.Contains(failure.message, "unknown variant `escalated`") {
		t.Fatalf("handleRequest(fs/readFile) error = %#v", err)
	}
}
