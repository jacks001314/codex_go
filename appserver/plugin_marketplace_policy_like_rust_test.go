package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/session"
)

func TestRuntimeRouterInstallsMarketplacePolicyBeforePluginStartupLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(`
[marketplaces]
restrict_to_allowed_sources = true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\nplugins = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewDefaultRuntimeRouter(session.NewStore(filepath.Join(home, "sessions")), home)
	t.Cleanup(func() { _ = router.Close() })

	plugins := router.requirePlugins()
	if plugins == nil {
		t.Fatal("runtime router did not create the plugin service")
	}
	if plugins.StartCuratedRepoSync(nil) {
		t.Fatal("startup policy did not block the curated repository sync")
	}
}
