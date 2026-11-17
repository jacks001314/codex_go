package appserver

import (
	"encoding/json"
	"reflect"
	"testing"
)

// These cases mirror Rust's committed world-state snapshot fixture
// (codex-rs/core/src/context/world_state/snapshots/
// codex_core__context__world_state__environment__tests__snapshots.snap) so the
// Go section renders and persists exactly what Rust does.
func TestEnvironmentsWorldStateSnapshotMatchesRustLikeRust(t *testing.T) {
	available := func(cwd, shell string) environmentWorldStateEnvironment {
		return environmentWorldStateEnvironment{CWD: cwd, Status: environmentStatusAvailable, Shell: shell}
	}
	tests := []struct {
		name         string
		previous     environmentWorldStateSection
		known        bool
		current      environmentWorldStateSection
		wantFragment string
		wantSnapshot string
	}{
		{
			name: "absent renders the full section",
			current: environmentWorldStateSection{Environments: map[string]environmentWorldStateEnvironment{
				"devbox": available("/workspace", "bash"),
				"laptop": {CWD: "/repo", Status: environmentStatusAvailable, Shell: "zsh", IsPrimary: true},
			}},
			wantFragment: "<environment_context>\n  <environments>\n" +
				"    <environment id=\"devbox\" primary=\"false\">\n      <cwd>/workspace</cwd>\n      <shell>bash</shell>\n    </environment>\n" +
				"    <environment id=\"laptop\" primary=\"true\">\n      <cwd>/repo</cwd>\n      <shell>zsh</shell>\n    </environment>\n" +
				"  </environments>\n</environment_context>",
			wantSnapshot: `{"environments":{"devbox":{"cwd":"/workspace","shell":"bash","status":"available"},"laptop":{"cwd":"/repo","is_primary":true,"shell":"zsh","status":"available"}}}`,
		},
		{
			name: "environment changes restate the changed environments and withdraw the removed one",
			previous: environmentWorldStateSection{Environments: map[string]environmentWorldStateEnvironment{
				"laptop": {CWD: "/repo", Status: environmentStatusAvailable, Shell: "bash", IsPrimary: true},
				"devbox": {CWD: "/workspace", Status: environmentStatusStarting},
				"old":    available("/old", "sh"),
			}},
			known: true,
			current: environmentWorldStateSection{Environments: map[string]environmentWorldStateEnvironment{
				"devbox": available("/workspace", "powershell"),
				"laptop": {CWD: "/repo", Status: environmentStatusAvailable, Shell: "zsh", IsPrimary: true},
				"remote": {CWD: "/remote", Status: environmentStatusStarting},
			}},
			wantFragment: "<environment_context>\n  <environments>\n" +
				"    <environment id=\"devbox\" primary=\"false\">\n      <cwd>/workspace</cwd>\n      <shell>powershell</shell>\n    </environment>\n" +
				"    <environment id=\"laptop\" primary=\"true\">\n      <cwd>/repo</cwd>\n      <shell>zsh</shell>\n    </environment>\n" +
				"    <environment id=\"old\" status=\"unavailable\" />\n" +
				"    <environment id=\"remote\" primary=\"false\">\n      <cwd>/remote</cwd>\n      <status>starting</status>\n    </environment>\n" +
				"  </environments>\n</environment_context>",
			wantSnapshot: `{"environments":{"devbox":{"cwd":"/workspace","shell":"powershell","status":"available"},"laptop":{"cwd":"/repo","is_primary":true,"shell":"zsh","status":"available"},"remote":{"cwd":"/remote","status":"starting"}}}`,
		},
		{
			name: "turn context values change without restating environments",
			previous: environmentWorldStateSection{
				Environments: map[string]environmentWorldStateEnvironment{"local": available("/repo", "zsh")},
				CurrentDate:  "2026-06-19",
				Timezone:     "UTC",
				Network:      `<network enabled="true"><allowed>old.example.com</allowed></network>`,
				Filesystem:   `<filesystem><permission_profile type="disabled"><file_system type="unrestricted" /></permission_profile></filesystem>`,
			},
			known: true,
			current: environmentWorldStateSection{
				Environments: map[string]environmentWorldStateEnvironment{"local": available("/repo", "zsh")},
				CurrentDate:  "2026-06-20",
				Timezone:     "America/Los_Angeles",
				Network:      `<network enabled="true"><allowed>new.example.com</allowed><denied>blocked.example.com</denied></network>`,
				Filesystem:   `<filesystem><permission_profile type="external"><file_system type="external" /></permission_profile></filesystem>`,
			},
			wantFragment: "<environment_context>\n  <current_date>2026-06-20</current_date>\n" +
				"  <timezone>America/Los_Angeles</timezone>\n" +
				"  <network enabled=\"true\"><allowed>new.example.com</allowed><denied>blocked.example.com</denied></network>\n" +
				"  <filesystem><permission_profile type=\"external\"><file_system type=\"external\" /></permission_profile></filesystem>\n" +
				"</environment_context>",
			wantSnapshot: `{"current_date":"2026-06-20","environments":{"local":{"cwd":"/repo","shell":"zsh","status":"available"}},"filesystem":"<filesystem><permission_profile type=\"external\"><file_system type=\"external\" /></permission_profile></filesystem>","network":"<network enabled=\"true\"><allowed>new.example.com</allowed><denied>blocked.example.com</denied></network>","timezone":"America/Los_Angeles"}`,
		},
		{
			name: "single environment renders the legacy layout with its filesystem",
			current: environmentWorldStateSection{
				Environments: map[string]environmentWorldStateEnvironment{"remote": available(`C:\windows`, "powershell")},
				Filesystem:   `<filesystem><permission_profile type="disabled"><file_system type="unrestricted" /></permission_profile></filesystem>`,
			},
			wantFragment: "<environment_context>\n  <cwd>C:\\windows</cwd>\n  <shell>powershell</shell>\n" +
				"  <filesystem><permission_profile type=\"disabled\"><file_system type=\"unrestricted\" /></permission_profile></filesystem>\n" +
				"</environment_context>",
			wantSnapshot: `{"environments":{"remote":{"cwd":"C:\\windows","shell":"powershell","status":"available"}},"filesystem":"<filesystem><permission_profile type=\"disabled\"><file_system type=\"unrestricted\" /></permission_profile></filesystem>"}`,
		},
		{
			name: "a shell that appears without a shell version restates nothing",
			previous: environmentWorldStateSection{Environments: map[string]environmentWorldStateEnvironment{
				"local": {CWD: "/repo", Status: environmentStatusAvailable},
			}},
			known: true,
			current: environmentWorldStateSection{Environments: map[string]environmentWorldStateEnvironment{
				"local": available("/repo", "zsh"),
			}},
			wantFragment: "",
			wantSnapshot: `{"environments":{"local":{"cwd":"/repo","shell":"zsh","status":"available"}}}`,
		},
		{
			name: "removing the only environment withdraws it",
			previous: environmentWorldStateSection{Environments: map[string]environmentWorldStateEnvironment{
				"local": available("/repo", "bash"),
			}},
			known:   true,
			current: environmentWorldStateSection{Environments: map[string]environmentWorldStateEnvironment{}},
			wantFragment: "<environment_context>\n  <environments>\n" +
				"    <environment id=\"local\" status=\"unavailable\" />\n" +
				"  </environments>\n</environment_context>",
			wantSnapshot: `{"environments":{}}`,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			current, fragment := testCase.current.renderDiff(testCase.previous, testCase.known, nil)
			if fragment != "" {
				fragment = wrapEnvironmentContext(fragment)
			}
			if fragment != testCase.wantFragment {
				t.Fatalf("fragment = %q, want %q", fragment, testCase.wantFragment)
			}
			encoded, err := json.Marshal(current)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			var got, want any
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatalf("Unmarshal(got) error = %v", err)
			}
			if err := json.Unmarshal([]byte(testCase.wantSnapshot), &want); err != nil {
				t.Fatalf("Unmarshal(want) error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("snapshot = %s, want %s", encoded, testCase.wantSnapshot)
			}
		})
	}
}

// Rust's full render (the compaction window) explicitly states every value, so
// a missing date never becomes an unavailable marker.
func TestEnvironmentsWorldStateFullRenderSkipsRemovalsLikeRust(t *testing.T) {
	section := environmentWorldStateSection{
		Environments: map[string]environmentWorldStateEnvironment{
			"local": {CWD: "/repo", Status: environmentStatusAvailable, Shell: "zsh"},
		},
		Timezone: "UTC",
	}
	got := wrapEnvironmentContext(section.renderFull(environmentWorldStateRemovals{}, section.Subagents))
	want := "<environment_context>\n  <cwd>/repo</cwd>\n  <shell>zsh</shell>\n  <timezone>UTC</timezone>\n</environment_context>"
	if got != want {
		t.Fatalf("full render = %q, want %q", got, want)
	}
}

// The legacy Go shape only recorded the removal-sensitive values; it must still
// decode so an older thread reports the removal exactly once.
func TestDecodeEnvironmentWorldStateLegacyGoShapeLikeRust(t *testing.T) {
	section, known := decodeEnvironmentWorldStateSnapshot(json.RawMessage(`{"currentDate":"2026-06-17","shellVersion":"7.4.1"}`))
	if !known || section.CurrentDate != "2026-06-17" || section.ShellVersion != "7.4.1" {
		t.Fatalf("legacy decode = %#v known=%t", section, known)
	}
	if section.Environments == nil {
		t.Fatal("legacy decode must produce an empty environments map")
	}
	if _, known := decodeEnvironmentWorldStateSnapshot(nil); known {
		t.Fatal("absent section must be unknown")
	}
}
