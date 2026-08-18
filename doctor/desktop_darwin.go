//go:build darwin

package doctor

import (
	"os"
	"path/filepath"
	"strings"
)

// desktopAppInstallations scans /Applications for OpenAI/ChatGPT/Codex desktop
// applications (Rust desktop.rs platform probe, #39060).
func desktopAppInstallations() []desktopAppInstallation {
	matches, err := filepath.Glob("/Applications/*.app")
	if err != nil {
		return nil
	}
	var out []desktopAppInstallation
	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".app")
		if !desktopProductName(name) {
			continue
		}
		version := desktopBundleVersion(path)
		out = append(out, desktopAppInstallation{
			Name:     name,
			Path:     path,
			Version:  version,
			Platform: "darwin",
		})
	}
	return out
}

func desktopBundleVersion(appPath string) string {
	infoPath := filepath.Join(appPath, "Contents", "Info.plist")
	data, err := os.ReadFile(infoPath)
	if err != nil {
		return ""
	}
	// Minimal CFBundleShortVersionString extraction (plist is XML on disk).
	const marker = "<key>CFBundleShortVersionString</key>"
	index := strings.Index(string(data), marker)
	if index < 0 {
		return ""
	}
	rest := string(data)[index+len(marker):]
	open := strings.Index(rest, "<string>")
	close := strings.Index(rest, "</string>")
	if open < 0 || close <= open {
		return ""
	}
	return strings.TrimSpace(rest[open+len("<string>") : close])
}
