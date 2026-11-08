//go:build darwin

package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var errDesktopBundle = errors.New("desktop bundle metadata is unreadable")

// desktopInstalledApp searches the bundles Rust looks at, /Applications then
// $HOME/Applications, for ChatGPT.app and Codex.app, and accepts the first one
// whose Info.plist carries the Codex bundle identifier.
func desktopInstalledApp() (*desktopApp, error) {
	directories := []string{"/Applications"}
	if home := os.Getenv("HOME"); home != "" {
		directories = append(directories, home+"/Applications")
	}
	for _, directory := range directories {
		for _, name := range []string{"ChatGPT.app", "Codex.app"} {
			bundle := directory + "/" + name
			info, err := os.Stat(bundle)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, errDesktopBundle
			}
			if !info.IsDir() {
				continue
			}
			metadata, err := desktopBundleMetadata(bundle)
			if err != nil {
				return nil, err
			}
			if metadata["CFBundleIdentifier"] != desktopMacBundleIdentifier {
				continue
			}
			version := metadata["CFBundleShortVersionString"]
			if version == "" {
				version = metadata["CFBundleVersion"]
			}
			if version == "" || metadata["CFBundleVersion"] == "" {
				return nil, errDesktopBundle
			}
			return &desktopApp{identity: desktopMacBundleIdentifier, version: version, bundle: bundle}, nil
		}
	}
	return nil, nil
}

// desktopApplicationRunning mirrors Rust's platform::application_running: the
// candidate process is the desktop application only when proc_pidpath resolves
// its executable inside <bundle>/Contents/MacOS.
func desktopApplicationRunning(app *desktopApp, pid int) bool {
	if app == nil || app.bundle == "" || pid <= 0 {
		return false
	}
	executable, ok := darwinProcessExecutablePath(pid)
	if !ok {
		return false
	}
	macOSDirectory := filepath.Join(app.bundle, "Contents", "MacOS")
	if executable == macOSDirectory {
		return true
	}
	return strings.HasPrefix(executable, macOSDirectory+string(os.PathSeparator))
}

func desktopBundleMetadata(bundle string) (map[string]string, error) {
	contents, err := os.ReadFile(bundle + "/Contents/Info.plist")
	if err != nil {
		return nil, errDesktopBundle
	}
	metadata := map[string]string{}
	for _, key := range []string{"CFBundleIdentifier", "CFBundleShortVersionString", "CFBundleVersion"} {
		if value, ok := desktopPlistString(string(contents), key); ok {
			metadata[key] = value
		}
	}
	return metadata, nil
}

func desktopPlistString(plist string, key string) (string, bool) {
	marker := "<key>" + key + "</key>"
	index := strings.Index(plist, marker)
	if index < 0 {
		return "", false
	}
	rest := plist[index+len(marker):]
	open := strings.Index(rest, "<string>")
	if open < 0 {
		return "", false
	}
	rest = rest[open+len("<string>"):]
	close := strings.Index(rest, "</string>")
	if close < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[:close]), true
}
