package memories

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const ExtensionResourceRetentionDays = 7

//go:embed templates/ad_hoc_instructions.md
var adHocInstructions string

func ExtensionsRoot(root string) string {
	return filepath.Join(root, ExtensionsSubdir)
}

func SeedExtensionInstructions(root string) error {
	extensionRoot := filepath.Join(ExtensionsRoot(root), "ad_hoc")
	if err := os.MkdirAll(extensionRoot, 0o755); err != nil {
		return err
	}
	path := filepath.Join(extensionRoot, "instructions.md")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := file.WriteString(adHocInstructions); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func PruneOldExtensionResources(root string, now time.Time) {
	cutoff := now.UTC().AddDate(0, 0, -ExtensionResourceRetentionDays)
	extensions, err := os.ReadDir(ExtensionsRoot(root))
	if err != nil {
		return
	}
	for _, extension := range extensions {
		if !extension.IsDir() {
			continue
		}
		extensionPath := filepath.Join(ExtensionsRoot(root), extension.Name())
		if _, err := os.Stat(filepath.Join(extensionPath, "instructions.md")); err != nil {
			continue
		}
		resourcesPath := filepath.Join(extensionPath, "resources")
		resources, err := os.ReadDir(resourcesPath)
		if err != nil {
			continue
		}
		for _, resource := range resources {
			if resource.IsDir() || !strings.HasSuffix(resource.Name(), ".md") {
				continue
			}
			timestamp, ok := extensionResourceTimestamp(resource.Name())
			if !ok || timestamp.After(cutoff) {
				continue
			}
			_ = os.Remove(filepath.Join(resourcesPath, resource.Name()))
		}
	}
}

func extensionResourceTimestamp(name string) (time.Time, bool) {
	if len(name) < 19 {
		return time.Time{}, false
	}
	parsed, err := time.ParseInLocation("2006-01-02T15-04-05", name[:19], time.UTC)
	return parsed, err == nil
}
