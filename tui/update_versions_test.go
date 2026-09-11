package tui

import "testing"

// Mirrors Rust update_versions_tests::official_server_version_comparison
// (#43619).
func TestIsOfficialServerOlderMatchesRust(t *testing.T) {
	if !IsOfficialServerOlder("0.152.1", "0.152.0") {
		t.Fatal("0.152.1 should be newer than 0.152.0")
	}
	if !IsOfficialServerOlder("0.153.0", "0.152.1") {
		t.Fatal("0.153.0 should be newer than 0.152.1")
	}
	if IsOfficialServerOlder("0.152.0", "0.152.0") {
		t.Fatal("equal versions are not older")
	}
	if IsOfficialServerOlder("0.152.0", "0.153.0") {
		t.Fatal("a newer server is not older")
	}
	for _, version := range []string{
		"0.0.0",
		"0.0.0.0",
		"0.153.0-alpha.1",
		"unknown",
		"0.153",
		"0.153.0.1",
		" 0.153.0",
		"+0.153.0",
		"0.0153.0",
	} {
		if IsOfficialServerOlder(version, "0.152.0") {
			t.Fatalf("invalid client version %q must not compare", version)
		}
		if IsOfficialServerOlder("0.153.0", version) {
			t.Fatalf("invalid server version %q must not compare", version)
		}
	}
}
