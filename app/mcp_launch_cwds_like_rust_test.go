package app

import (
	"path/filepath"
	"strings"
	"testing"

	"codex_go/cli"
)

func TestAppServerMCPLaunchCwdsValidatesAndNormalizesLikeRust(t *testing.T) {
	pluginDir := t.TempDir()
	configuredDir := t.TempDir()
	executable := filepath.Join(t.TempDir(), "node")
	launch, err := appServerMCPLaunchCwds(cli.AppServerOptions{
		PluginMCPLaunchCwds: []cli.PluginMCPLaunchCwd{{PluginID: "sample@market", Server: "docs", Directory: pluginDir}},
		MCPLaunchCwds:       []cli.ConfiguredMCPLaunchCwd{{Server: "configured", Executable: executable, Directory: configuredDir}},
	})
	if err != nil {
		t.Fatalf("appServerMCPLaunchCwds() error = %v", err)
	}
	if got, ok := launch.PluginDirectory("sample@market", "docs"); !ok || got != pluginDir {
		t.Fatalf("plugin directory = %q, %v", got, ok)
	}
	if got, ok := launch.ConfiguredDirectory("configured", executable); !ok || got != configuredDir {
		t.Fatalf("configured directory = %q, %v", got, ok)
	}
	if _, ok := launch.ConfiguredDirectory("configured", "different"); ok {
		t.Fatal("an executable mismatch matched the launch directory")
	}
	// Rust validates the executable as an absolute path but keeps the original
	// string for the configured-command comparison instead of normalizing it.
	rawExecutable := strings.ReplaceAll(executable, string(filepath.Separator), string(filepath.Separator)+string(filepath.Separator))
	verbatim, err := appServerMCPLaunchCwds(cli.AppServerOptions{
		MCPLaunchCwds: []cli.ConfiguredMCPLaunchCwd{{Server: "configured", Executable: rawExecutable, Directory: configuredDir}},
	})
	if err != nil {
		t.Fatalf("appServerMCPLaunchCwds() error = %v", err)
	}
	if _, ok := verbatim.ConfiguredDirectory("configured", rawExecutable); !ok {
		t.Fatal("the raw executable spelling did not match")
	}
	if _, ok := verbatim.ConfiguredDirectory("configured", executable); ok {
		t.Fatal("the executable was normalized before matching")
	}
}

func TestAppServerMCPLaunchCwdsRejectsRelativePathsLikeRust(t *testing.T) {
	if _, err := appServerMCPLaunchCwds(cli.AppServerOptions{
		PluginMCPLaunchCwds: []cli.PluginMCPLaunchCwd{{PluginID: "sample@market", Server: "docs", Directory: "relative"}},
	}); err == nil {
		t.Fatal("a relative plugin launch directory was accepted")
	}
	if _, err := appServerMCPLaunchCwds(cli.AppServerOptions{
		MCPLaunchCwds: []cli.ConfiguredMCPLaunchCwd{{Server: "docs", Executable: "node", Directory: t.TempDir()}},
	}); err == nil {
		t.Fatal("a relative configured executable was accepted")
	}
}
