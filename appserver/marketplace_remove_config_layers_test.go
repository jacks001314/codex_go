package appserver

import (
	"testing"

	"codex_go/config"
)

func TestMarketplaceRemoveConflictSourceDetectsOtherLayer(t *testing.T) {
	home := t.TempDir()
	svc := config.NewConfigService(home)
	svc.SetManagedLayers([]config.Layer{
		{
			Name: config.LayerSource{Type: config.LayerSourceEnterpriseManaged, File: "managed.toml"},
			Config: map[string]any{
				"marketplaces": map[string]any{
					"debug": map[string]any{"source_type": "git"},
				},
			},
		},
	})
	router := NewRuntimeRouter(RuntimeServices{Config: svc})

	// A marketplace defined by an enabled non-user layer is a conflict.
	source, err := router.marketplaceRemoveConflictSource("debug")
	if err != nil {
		t.Fatalf("marketplaceRemoveConflictSource() error = %v", err)
	}
	if source != "enterpriseManaged" {
		t.Fatalf("conflict source = %q, want enterpriseManaged", source)
	}

	// A marketplace defined only by the user override is not a conflict.
	source, err = router.marketplaceRemoveConflictSource("user-only")
	if err != nil {
		t.Fatalf("marketplaceRemoveConflictSource() error = %v", err)
	}
	if source != "" {
		t.Fatalf("user-only conflict source = %q, want empty", source)
	}
}
