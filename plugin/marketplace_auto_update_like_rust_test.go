package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMarketplaceAutoUpdatePreservedForSameSourceLikeRust(t *testing.T) {
	catalog := t.TempDir()
	for _, tc := range []struct {
		name, kind, oldSource, newSource string
		keep                             bool
	}{
		{"same git", "git", "https://example.com/team.git", "https://example.com/team.git", true},
		{"changed git", "git", "https://example.com/team.git", "https://example.com/other.git", false},
		{"equivalent local", "local", catalog + string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(catalog), catalog, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			values := map[string]any{"marketplaces": map[string]any{
				"team":  map[string]any{"source_type": tc.kind, "source": tc.oldSource, "auto_update": true},
				"other": map[string]any{"source_type": "git", "source": "https://example.com/keep.git"},
			}}
			if err := writeMarketplaceConfig(path, values); err != nil {
				t.Fatal(err)
			}
			if err := recordMarketplaceConfig(path, &Marketplace{Name: "team", SourceType: tc.kind, SourceURL: tc.newSource}); err != nil {
				t.Fatal(err)
			}
			got, err := readMarketplaceConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			entries := got["marketplaces"].(map[string]any)
			team := entries["team"].(map[string]any)
			value, present := team["auto_update"]
			if present != tc.keep || (present && value != true) {
				t.Fatalf("auto_update = %v (present %v), want retained %v", value, present, tc.keep)
			}
			if _, ok := entries["other"]; !ok {
				t.Fatal("unrelated marketplace removed")
			}
		})
	}
}

func TestMarketplaceAutoUpdateInlineTableLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	fixture := `marketplaces = { team = { source_type = "git", source = "https://example.com/team.git", auto_update = false }, other = { source_type = "git", source = "https://example.com/other.git" } }`
	if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	if err := recordMarketplaceConfig(path, &Marketplace{Name: "team", SourceType: "git", SourceURL: "https://example.com/team.git"}); err != nil {
		t.Fatal(err)
	}
	values, err := readMarketplaceConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := values["marketplaces"].(map[string]any)
	if entries["team"].(map[string]any)["auto_update"] != false {
		t.Fatal("explicit false lost")
	}
	if _, ok := entries["other"]; !ok {
		t.Fatal("other inline marketplace lost")
	}
}
