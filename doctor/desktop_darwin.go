//go:build darwin

package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
			app, err := desktopInspectBundle(bundle)
			if err != nil {
				return nil, err
			}
			if app != nil {
				return app, nil
			}
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

// desktopInspectBundle mirrors Rust 3f65e9f1 inspect_macos_bundle, including
// property-list conversion and string/u64 validation.
func desktopInspectBundle(bundle string) (*desktopApp, error) {
	metadata, err := desktopBundleMetadata(bundle)
	if err != nil {
		return nil, err
	}
	if metadata["CFBundleIdentifier"] != desktopMacBundleIdentifier {
		return nil, nil
	}
	versionValue, present := metadata["CFBundleShortVersionString"]
	if !present {
		versionValue = metadata["CFBundleVersion"]
	}
	version, ok := versionValue.(string)
	if !ok {
		return nil, errDesktopBundle
	}
	build, ok := metadata["CFBundleVersion"].(string)
	if !ok {
		return nil, errDesktopBundle
	}
	if _, err := strconv.ParseUint(strings.TrimPrefix(build, "+"), 10, 64); err != nil {
		return nil, errDesktopBundle
	}
	return &desktopApp{identity: desktopMacBundleIdentifier, version: version, bundle: bundle}, nil
}

func desktopBundleMetadata(bundle string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", filepath.Join(bundle, "Contents", "Info.plist"))
	output := &boundedOutput{limit: 64 * 1024}
	command.Stdout = output
	if err := command.Run(); err != nil || output.truncated {
		return nil, errDesktopBundle
	}
	var decoded any
	if err := json.Unmarshal(output.buffer.Bytes(), &decoded); err != nil {
		return nil, errDesktopBundle
	}
	metadata, _ := decoded.(map[string]any)
	return metadata, nil
}
