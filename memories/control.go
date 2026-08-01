package memories

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ClearRootsContents(codexHome string) error {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return nil
	}
	for _, root := range []string{
		filepath.Join(codexHome, "memories"),
		filepath.Join(codexHome, "memories_extensions"),
	} {
		if err := clearRootContents(root); err != nil {
			return err
		}
	}
	return nil
}

func clearRootContents(root string) error {
	metadata, err := os.Lstat(root)
	if err == nil && metadata.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to clear symlinked memory root %s", root)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if entry.IsDir() {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		} else if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
