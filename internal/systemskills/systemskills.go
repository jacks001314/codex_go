package systemskills

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	markerFilename = ".codex-system-skills.marker"
	markerSalt     = "v1"
	embeddedRoot   = "assets/samples"
)

//go:embed assets/samples
var embeddedSamples embed.FS

func CacheRoot(codexHome string) string {
	return filepath.Join(codexHome, "skills", ".system")
}

func Uninstall(codexHome string) {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return
	}
	_ = os.RemoveAll(CacheRoot(codexHome))
}

func Install(codexHome string) error {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return fmt.Errorf("system skills CODEX_HOME is required")
	}
	skillsRoot := filepath.Join(codexHome, "skills")
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		return fmt.Errorf("io error while create skills root dir: %w", err)
	}
	destination := CacheRoot(codexHome)
	markerPath := filepath.Join(destination, markerFilename)
	fingerprint, err := Fingerprint()
	if err != nil {
		return err
	}
	if info, statErr := os.Stat(destination); statErr == nil && info.IsDir() {
		if marker, readErr := os.ReadFile(markerPath); readErr == nil && strings.TrimSpace(string(marker)) == fingerprint {
			return nil
		}
	}
	if err := os.RemoveAll(destination); err != nil {
		return fmt.Errorf("io error while remove existing system skills dir: %w", err)
	}
	if err := writeEmbeddedDir(destination); err != nil {
		return err
	}
	if err := os.WriteFile(markerPath, []byte(fingerprint+"\n"), 0o644); err != nil {
		return fmt.Errorf("io error while write system skills marker: %w", err)
	}
	return nil
}

func Fingerprint() (string, error) {
	items := []string{}
	err := fs.WalkDir(embeddedSamples, embeddedRoot, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == embeddedRoot {
			return nil
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(name, embeddedRoot), "/")
		if entry.IsDir() {
			items = append(items, "d\x00"+relative)
			return nil
		}
		contents, err := embeddedSamples.ReadFile(name)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(contents)
		items = append(items, "f\x00"+relative+"\x00"+hex.EncodeToString(digest[:]))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("fingerprint embedded system skills: %w", err)
	}
	sort.Strings(items)
	hash := sha256.New()
	_, _ = hash.Write([]byte(markerSalt))
	for _, item := range items {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(item))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeEmbeddedDir(destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return fmt.Errorf("io error while create system skills dir: %w", err)
	}
	return fs.WalkDir(embeddedSamples, embeddedRoot, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == embeddedRoot {
			return nil
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(name, embeddedRoot), "/")
		target := filepath.Join(destination, filepath.FromSlash(path.Clean(relative)))
		if entry.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("io error while create system skills subdir: %w", err)
			}
			return nil
		}
		contents, err := embeddedSamples.ReadFile(name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("io error while create system skills file parent: %w", err)
		}
		if err := os.WriteFile(target, contents, 0o644); err != nil {
			return fmt.Errorf("io error while write system skill file: %w", err)
		}
		return nil
	})
}
