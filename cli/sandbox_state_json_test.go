package cli

import (
	"runtime"
	"testing"
)

// The expectations below are byte-for-byte the stderr 0.166 prints for the same
// --sandbox-state-json payload; out/r74-rust-expectations.json holds the capture
// this table was generated from. The `Error: ` head and the final newline are
// added by main when the error escapes, so the table starts after that prefix.
func TestParseSandboxStateJSONValueMatchesRust166(t *testing.T) {
	cases := []struct {
		name        string
		payload     string
		want        string
		windowsOnly bool
	}{
		{name: "empty", payload: "", want: "invalid --sandbox-state-json value: EOF while parsing a value at line 1 column 0", windowsOnly: false},
		{name: "whitespace", payload: "   ", want: "invalid --sandbox-state-json value: EOF while parsing a value at line 1 column 3", windowsOnly: false},
		{name: "garbage", payload: "notjson", want: "invalid --sandbox-state-json value: expected ident at line 1 column 2", windowsOnly: false},
		{name: "number", payload: "1", want: "invalid --sandbox-state-json value: invalid type: integer `1`, expected struct SandboxState at line 1 column 1", windowsOnly: false},
		{name: "null", payload: "null", want: "invalid --sandbox-state-json value: invalid type: null, expected struct SandboxState at line 1 column 4", windowsOnly: false},
		{name: "string", payload: "\"x\"", want: "invalid --sandbox-state-json value: invalid type: string \"x\", expected struct SandboxState at line 1 column 3", windowsOnly: false},
		{name: "array-empty", payload: "[]", want: "invalid --sandbox-state-json value: invalid length 0, expected struct SandboxState with 5 elements at line 1 column 2", windowsOnly: false},
		{name: "array-two", payload: "[{},null]", want: "invalid --sandbox-state-json value: invalid length 2, expected struct SandboxState with 5 elements at line 1 column 9", windowsOnly: false},
		{name: "array-extra", payload: "[{},null,\"file:///C:/tmp\",true,false,1]", want: "invalid --sandbox-state-json value: trailing characters at line 1 column 38", windowsOnly: false},
		{name: "missing-cwd", payload: "{\"permissionProfile\":{}}", want: "invalid --sandbox-state-json value: missing field `sandboxCwd` at line 1 column 24", windowsOnly: false},
		{name: "missing-profile", payload: "{\"sandboxCwd\":\"file:///C:/tmp\"}", want: "invalid --sandbox-state-json value: missing field `permissionProfile` at line 1 column 31", windowsOnly: false},
		{name: "profile-int", payload: "{\"permissionProfile\":1}", want: "invalid --sandbox-state-json value: data did not match any variant of untagged enum PermissionProfileDe at line 1 column 23", windowsOnly: false},
		{name: "profile-null", payload: "{\"permissionProfile\":null,\"sandboxCwd\":\"file:///C:/tmp\"}", want: "invalid --sandbox-state-json value: data did not match any variant of untagged enum PermissionProfileDe at line 1 column 25", windowsOnly: false},
		{name: "profile-unknown-tag", payload: "{\"permissionProfile\":{\"type\":\"bogus\"},\"sandboxCwd\":\"file:///C:/tmp\"}", want: "invalid --sandbox-state-json value: data did not match any variant of untagged enum PermissionProfileDe at line 1 column 37", windowsOnly: false},
		{name: "profile-managed-missing-file-system", payload: "{\"permissionProfile\":{\"type\":\"managed\",\"network\":\"enabled\"},\"sandboxCwd\":\"file:///C:/tmp\"}", want: "invalid --sandbox-state-json value: data did not match any variant of untagged enum PermissionProfileDe at line 1 column 59", windowsOnly: false},
		{name: "profile-nested-array", payload: "[[]]", want: "invalid --sandbox-state-json value: data did not match any variant of untagged enum PermissionProfileDe at line 1 column 4", windowsOnly: false},
		{name: "cwd-number", payload: "{\"permissionProfile\":{},\"sandboxCwd\":5}", want: "invalid --sandbox-state-json value: invalid type: integer `5`, expected a string at line 1 column 38", windowsOnly: false},
		{name: "cwd-float", payload: "{\"permissionProfile\":{},\"sandboxCwd\":1.5}", want: "invalid --sandbox-state-json value: invalid type: number, expected a string at line 1 column 40", windowsOnly: false},
		{name: "cwd-bool", payload: "{\"permissionProfile\":{},\"sandboxCwd\":true}", want: "invalid --sandbox-state-json value: invalid type: boolean `true`, expected a string at line 1 column 41", windowsOnly: false},
		{name: "cwd-relative", payload: "{\"permissionProfile\":{},\"sandboxCwd\":\"/home/x\"}", want: "invalid --sandbox-state-json value: invalid URI: relative URL without a base at line 1 column 47", windowsOnly: false},
		{name: "cwd-native-path", payload: "{\"permissionProfile\":{},\"sandboxCwd\":\"C:\\\\tmp\"}", want: "invalid --sandbox-state-json value: unsupported path URI scheme `c` at line 1 column 47", windowsOnly: false},
		{name: "cwd-http", payload: "{\"permissionProfile\":{},\"sandboxCwd\":\"http://x/y\"}", want: "invalid --sandbox-state-json value: unsupported path URI scheme `http` at line 1 column 50", windowsOnly: false},
		{name: "cwd-not-native", payload: "{\"permissionProfile\":{},\"sandboxCwd\":\"file:///home/x\"}", want: "sandbox state cwd is not native to this host\n\nCaused by:\n    'file:///home/x' is invalid on 'windows'", windowsOnly: true},
		{name: "cwd-file-without-slashes", payload: "{\"permissionProfile\":{},\"sandboxCwd\":\"file:tmp\"}", want: "sandbox state cwd is not native to this host\n\nCaused by:\n    'file:///tmp' is invalid on 'windows'", windowsOnly: true},
		{name: "dup-profile", payload: "{\"permissionProfile\":{},\"permissionProfile\":{},\"sandboxCwd\":\"file:///C:/tmp\"}", want: "invalid --sandbox-state-json value: duplicate field `permissionProfile` at line 1 column 43", windowsOnly: false},
		{name: "dup-cwd", payload: "{\"permissionProfile\":{},\"sandboxCwd\":\"file:///C:/tmp\",\"sandboxCwd\":\"file:///C:/tmp\"}", want: "invalid --sandbox-state-json value: duplicate field `sandboxCwd` at line 1 column 66", windowsOnly: false},
		{name: "bool-string", payload: "{\"permissionProfile\":{},\"sandboxCwd\":\"file:///C:/tmp\",\"useLegacyLandlock\":\"x\"}", want: "invalid --sandbox-state-json value: invalid type: string \"x\", expected a boolean at line 1 column 77", windowsOnly: false},
		{name: "bool-number", payload: "{\"permissionProfile\":{},\"sandboxCwd\":\"file:///C:/tmp\",\"useMxc\":1}", want: "invalid --sandbox-state-json value: invalid type: integer `1`, expected a boolean at line 1 column 64", windowsOnly: false},
		{name: "exe-number", payload: "{\"permissionProfile\":{},\"sandboxCwd\":\"file:///C:/tmp\",\"codexLinuxSandboxExe\":5}", want: "invalid --sandbox-state-json value: invalid type: integer `5`, expected path string at line 1 column 78", windowsOnly: false},
		{name: "trailing", payload: "{\"permissionProfile\":{},\"sandboxCwd\":\"file:///C:/tmp\"} x", want: "invalid --sandbox-state-json value: trailing characters at line 1 column 56", windowsOnly: false},
		{name: "open-brace", payload: "{\"permissionProfile\":{}", want: "invalid --sandbox-state-json value: EOF while parsing an object at line 1 column 23", windowsOnly: false},
		{name: "open-string", payload: "{\"permissionProfile\":{},\"sandboxCwd\":\"file:///C:/", want: "invalid --sandbox-state-json value: EOF while parsing a string at line 1 column 49", windowsOnly: false},
		{name: "colon-missing", payload: "{\"permissionProfile\" {}}", want: "invalid --sandbox-state-json value: expected `:` at line 1 column 22", windowsOnly: false},
		{name: "key-unquoted", payload: "{permissionProfile:{}}", want: "invalid --sandbox-state-json value: key must be a string at line 1 column 2", windowsOnly: false},
		{name: "partial-literal", payload: "tru", want: "invalid --sandbox-state-json value: EOF while parsing a value at line 1 column 3", windowsOnly: false},
		{name: "multiline", payload: "{\n  \"permissionProfile\": {},\n  \"sandboxCwd\": 5\n}", want: "invalid --sandbox-state-json value: invalid type: integer `5`, expected a string at line 3 column 17", windowsOnly: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if tt.windowsOnly && runtime.GOOS != "windows" {
				t.Skip("PathUri host check is written for the Windows host this was captured on")
			}
			// A payload can decode and still describe a path this host cannot
			// represent, which Rust reports once the value is resolved.
			value, err := ParseSandboxStateJSONValue(tt.payload)
			if err == nil {
				_, err = value.NativeCWD()
			}
			if err == nil {
				t.Fatalf("ParseSandboxStateJSONValue(%q) succeeded, want a diagnostic", tt.payload)
			}
			if got := err.Error(); got != tt.want {
				t.Fatalf("diagnostic mismatch\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// TestParseSandboxStateJSONValueNormalizesForRunner checks the payload the
// sandbox runner receives: sandboxCwd becomes a native path so the runner never
// has to understand PathUri, and a positional sequence is rewritten as the
// equivalent object.
func TestParseSandboxStateJSONValueNormalizesForRunner(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("file: URI normalisation is asserted against Windows drive paths")
	}
	cases := []struct {
		name     string
		payload  string
		wantCWD  string
		wantJSON string
	}{
		{
			name:     "object keeps every other byte",
			payload:  `{"permissionProfile":{},"sandboxCwd":"file:///c:/tmp","zzz":1}`,
			wantCWD:  `C:\tmp`,
			wantJSON: `{"permissionProfile":{},"sandboxCwd":"C:\\tmp","zzz":1}`,
		},
		{
			name:     "unc authority",
			payload:  `{"permissionProfile":{"type":"disabled"},"sandboxCwd":"file://server/share/x"}`,
			wantCWD:  `\\server\share\x`,
			wantJSON: `{"permissionProfile":{"type":"disabled"},"sandboxCwd":"\\\\server\\share\\x"}`,
		},
		{
			name:     "sequence becomes an object",
			payload:  `[{},null,"file:///C:/tmp"]`,
			wantCWD:  `C:\tmp`,
			wantJSON: `{"permissionProfile":{},"codexLinuxSandboxExe":null,"sandboxCwd":"C:\\tmp","useLegacyLandlock":false,"useMxc":false}`,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			value, err := ParseSandboxStateJSONValue(tt.payload)
			if err != nil {
				t.Fatalf("ParseSandboxStateJSONValue(%q) error = %v", tt.payload, err)
			}
			native, err := value.NativeCWD()
			if err != nil {
				t.Fatalf("NativeCWD() error = %v", err)
			}
			if native != tt.wantCWD {
				t.Fatalf("NativeCWD() = %q, want %q", native, tt.wantCWD)
			}
			if got := value.NormalizedJSON(native); got != tt.wantJSON {
				t.Fatalf("NormalizedJSON() = %s, want %s", got, tt.wantJSON)
			}
		})
	}
}
