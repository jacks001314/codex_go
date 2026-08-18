//go:build windows

package doctor

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// desktopAppInstallations probes the Windows Uninstall registry keys for
// OpenAI/ChatGPT/Codex desktop applications (Rust desktop.rs platform probe,
// #39060).
func desktopAppInstallations() []desktopAppInstallation {
	var out []desktopAppInstallation
	roots := []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE}
	for _, root := range roots {
		for _, uninstallPath := range []string{
			`Software\Microsoft\Windows\CurrentVersion\Uninstall`,
			`Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`,
		} {
			key, err := registry.OpenKey(root, uninstallPath, registry.ENUMERATE_SUB_KEYS)
			if err != nil {
				continue
			}
			names, err := key.ReadSubKeyNames(0)
			_ = key.Close()
			if err != nil {
				continue
			}
			for _, name := range names {
				sub, err := registry.OpenKey(root, uninstallPath+`\`+name, registry.QUERY_VALUE)
				if err != nil {
					continue
				}
				displayName, _, _ := sub.GetStringValue("DisplayName")
				installLocation, _, _ := sub.GetStringValue("InstallLocation")
				displayVersion, _, _ := sub.GetStringValue("DisplayVersion")
				_ = sub.Close()
				if !desktopProductName(displayName) {
					continue
				}
				out = append(out, desktopAppInstallation{
					Name:     strings.TrimSpace(displayName),
					Path:     strings.TrimSpace(installLocation),
					Version:  strings.TrimSpace(displayVersion),
					Platform: "windows",
				})
			}
		}
	}
	return out
}

func desktopProductName(displayName string) bool {
	lower := strings.ToLower(strings.TrimSpace(displayName))
	return strings.Contains(lower, "openai") || strings.Contains(lower, "chatgpt") || strings.Contains(lower, "codex")
}
