package windowssandbox

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// CleanUninstallEnabled reads the owner's current home-deletion preference.
// A missing config keeps the OEM preinstall default; unreadable or invalid
// configuration disables cleanup (Rust clean_uninstall_enabled).
func CleanUninstallEnabled(home string, preinstalled bool) bool {
	contents, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return preinstalled
		}
		return false
	}
	var parsed struct {
		Desktop map[string]any `toml:"desktop"`
	}
	if err := toml.Unmarshal(contents, &parsed); err != nil {
		return false
	}
	value, ok := parsed.Desktop["uninstall_codex_home"]
	if !ok {
		return preinstalled
	}
	enabled, ok := value.(bool)
	if !ok {
		return false
	}
	return enabled
}
