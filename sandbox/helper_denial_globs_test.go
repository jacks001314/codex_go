package sandbox

import (
	"encoding/json"
	"testing"
)

func TestWindowsHelperDenialGlobsLikeRust(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"~", "~"}, {"~/", "~/"}, {`~\`, `~\`},
		{"~/private/*.env", "~/private/*.env"},
		{`~\private\*.env`, `~\private\*.env`},
		{"hidden/*.env", `C:\policy\checkout\hidden\*.env`},
		{"/hidden/*.env", `C:\hidden\*.env`},
		{`\hidden\*.env`, `C:\hidden\*.env`},
		{"~someone/private", `C:\policy\checkout\~someone\private`},
		{"D:/absolute/*.env", "D:/absolute/*.env"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			wire := rustPermissionProfileWire{Type: "managed", Network: "restricted", FileSys: &rustPermissionFilesystem{Type: "restricted", Entries: []rustPermissionFilesystemEntry{
				{Path: rustPermissionFilesystemPath{Type: "glob_pattern", Pattern: tc.input}, Access: "deny"},
			}}}
			raw, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := ParseRuntimePermissionProfileJSON(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			before, _ := RuntimePermissionProfileJSON(*profile)
			bound, err := BindWindowsHelperDenialGlobs(profile, `C:\policy\checkout`)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := RuntimePermissionProfileJSON(*bound)
			if err := json.Unmarshal([]byte(after), &wire); err != nil {
				t.Fatal(err)
			}
			if got := wire.FileSys.Entries[0].Path.Pattern; got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			unchanged, _ := RuntimePermissionProfileJSON(*profile)
			if unchanged != before {
				t.Error("input mutated")
			}
		})
	}
}
