package tui

import "testing"

// Mirrors Rust update_versions_tests::stable_clients_only_warn_for_older_releases
// (#46673): a stable client orders against released servers, including
// prereleases, and ignores a server that is newer, equal, local, or carries
// build metadata.
func TestStableClientsOnlyWarnForOlderReleasesLikeRust(t *testing.T) {
	for _, tc := range []struct {
		client string
		server string
		want   ServerVersionNoticeKind
	}{
		{client: "0.152.1", server: "0.152.0", want: ServerVersionNoticeOlder},
		{client: "0.153.0", server: "0.152.1", want: ServerVersionNoticeOlder},
		{client: "0.153.0", server: "0.153.0-alpha.10.1", want: ServerVersionNoticeOlder},
		{client: "0.156.0", server: "0.155.0-alpha.12", want: ServerVersionNoticeOlder},
		{client: "0.153.0", server: "0.153.0"},
		{client: "0.153.0", server: "0.154.0"},
		{client: "0.153.0", server: "0.154.0-alpha.1"},
		{client: "0.153.0", server: "0.0.0"},
		{client: "0.153.0", server: "0.0.0-alpha.1"},
		{client: "0.153.0", server: "0.152.0+dev"},
	} {
		got, ok := ServerVersionNoticeKindFor(tc.client, tc.server)
		if tc.want == "" {
			if ok {
				t.Fatalf("notice(%q, %q) = %q, want none", tc.client, tc.server, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Fatalf("notice(%q, %q) = %q ok=%v, want %q", tc.client, tc.server, got, ok, tc.want)
		}
	}
}

// Mirrors Rust update_versions_tests::prerelease_clients_compare_within_the_same_release_line.
func TestPrereleaseClientsCompareWithinTheSameReleaseLineLikeRust(t *testing.T) {
	for _, tc := range [][2]string{
		{"0.155.0-alpha.23", "0.155.0-alpha.22"},
		{"0.155.0-alpha.24", "0.155.0-alpha.23"},
		{"0.153.0-alpha.10", "0.153.0-alpha.9"},
		{"0.153.0-alpha.10.1", "0.153.0-alpha.9.2"},
		{"0.153.0-alpha.10.10", "0.153.0-alpha.10.9"},
		{"0.153.0-alpha.10.1", "0.153.0-alpha.10"},
		{"0.155.0", "0.155.0-alpha.23"},
	} {
		newer, older := tc[0], tc[1]
		if kind, ok := ServerVersionNoticeKindFor(newer, older); !ok || kind != ServerVersionNoticeOlder {
			t.Fatalf("notice(%q, %q) = %q ok=%v, want older", newer, older, kind, ok)
		}
		if kind, ok := ServerVersionNoticeKindFor(older, newer); ok {
			t.Fatalf("notice(%q, %q) = %q, want none", older, newer, kind)
		}
		if kind, ok := ServerVersionNoticeKindFor(newer, newer); ok {
			t.Fatalf("notice(%q, %q) = %q, want none", newer, newer, kind)
		}
	}
}

// Mirrors Rust update_versions_tests::prerelease_clients_on_other_release_lines_and_local_clients_warn_for_mismatches.
func TestPrereleaseAndLocalClientsWarnForMismatchesLikeRust(t *testing.T) {
	for _, tc := range [][2]string{
		{"0.155.0-alpha.23", "0.156.0"},
		{"0.155.0-alpha.12", "0.154.0"},
		{"0.0.0", "0.153.0"},
		{"0.0.0", "0.153.0-alpha.10"},
		{"0.153.0+dev", "0.153.0"},
		{"0.153.0-alpha.10", "0.0.0"},
	} {
		client, server := tc[0], tc[1]
		if kind, ok := ServerVersionNoticeKindFor(client, server); !ok || kind != ServerVersionNoticeDifferent {
			t.Fatalf("notice(%q, %q) = %q ok=%v, want different", client, server, kind, ok)
		}
		if kind, ok := ServerVersionNoticeKindFor(client, client); ok {
			t.Fatalf("notice(%q, %q) = %q, want none", client, client, kind)
		}
	}
}

// Mirrors Rust update_versions_tests::unknown_or_malformed_versions_do_not_produce_notices.
func TestUnknownOrMalformedVersionsDoNotProduceNoticesLikeRust(t *testing.T) {
	for _, version := range []string{
		"unknown",
		"dev",
		"0.0.0.0",
		"0.153",
		"0.153.0.1",
		" 0.153.0",
		"+0.153.0",
		"0.0153.0",
		"0.153.0-alpha.01",
		"0.153.0-alpha..1",
	} {
		for _, release := range []string{"0.153.0", "0.153.0-alpha.10", "0.0.0"} {
			if kind, ok := ServerVersionNoticeKindFor(version, release); ok {
				t.Fatalf("notice(%q, %q) = %q, want none", version, release, kind)
			}
			if kind, ok := ServerVersionNoticeKindFor(release, version); ok {
				t.Fatalf("notice(%q, %q) = %q, want none", release, version, kind)
			}
		}
	}
}
