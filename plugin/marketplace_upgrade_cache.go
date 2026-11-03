package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// marketplaceCheckoutInputs identify the exact checkout whose manifest was
// rejected. They contain no source URL, only its digest (Rust #58957).
type marketplaceCheckoutInputs struct {
	ConfiguredName string   `json:"configured_name"`
	SourceSHA256   string   `json:"source_sha256"`
	RefName        *string  `json:"ref_name"`
	SparsePaths    []string `json:"sparse_paths"`
	Revision       string   `json:"revision"`
}

type marketplaceNameMismatch struct {
	Inputs    marketplaceCheckoutInputs `json:"inputs"`
	FoundName string                    `json:"found_name"`
}

type marketplaceNameMismatchError struct {
	ConfiguredName string
	FoundName      string
	Cacheable      bool
}

func (e *marketplaceNameMismatchError) Error() string {
	return fmt.Sprintf("upgraded marketplace name `%s` does not match configured marketplace `%s`", e.FoundName, e.ConfiguredName)
}

func newMarketplaceCheckoutInputs(name, source string, refName *string, sparsePaths []string, revision string) marketplaceCheckoutInputs {
	digest := sha256.Sum256([]byte(source))
	return marketplaceCheckoutInputs{
		ConfiguredName: name,
		SourceSHA256:   hex.EncodeToString(digest[:]),
		RefName:        cloneStringPtr(refName),
		SparsePaths:    append([]string(nil), sparsePaths...),
		Revision:       revision,
	}
}

func marketplaceNameMismatchCachePath(codexHome string, name string) string {
	return filepath.Join(codexHome, "cache", "marketplaces", name+".json")
}

func loadMarketplaceNameMismatch(codexHome string, inputs marketplaceCheckoutInputs) (string, bool) {
	body, err := os.ReadFile(marketplaceNameMismatchCachePath(codexHome, inputs.ConfiguredName))
	if err != nil {
		return "", false
	}
	var cached marketplaceNameMismatch
	if err := json.Unmarshal(body, &cached); err != nil || !marketplaceCheckoutInputsEqual(cached.Inputs, inputs) {
		return "", false
	}
	return cached.FoundName, true
}

func storeMarketplaceNameMismatch(codexHome string, inputs marketplaceCheckoutInputs, found string) {
	if strings.TrimSpace(codexHome) == "" || strings.TrimSpace(found) == "" {
		return
	}
	path := marketplaceNameMismatchCachePath(codexHome, inputs.ConfiguredName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	body, err := json.Marshal(marketplaceNameMismatch{Inputs: inputs, FoundName: found})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, body, 0o600)
}

func marketplaceCheckoutInputsEqual(left, right marketplaceCheckoutInputs) bool {
	if left.ConfiguredName != right.ConfiguredName || left.SourceSHA256 != right.SourceSHA256 || left.Revision != right.Revision {
		return false
	}
	if (left.RefName == nil) != (right.RefName == nil) {
		return false
	}
	if left.RefName != nil && *left.RefName != *right.RefName {
		return false
	}
	if len(left.SparsePaths) != len(right.SparsePaths) {
		return false
	}
	for i := range left.SparsePaths {
		if left.SparsePaths[i] != right.SparsePaths[i] {
			return false
		}
	}
	return true
}

// marketplaceManifestName reads the name declared by the staged manifest.
func marketplaceManifestName(root string) (string, error) {
	manifestPath := findMarketplaceManifestPath(root)
	if manifestPath == "" {
		return "", fmt.Errorf("marketplace manifest is missing")
	}
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", err
	}
	var manifest marketplaceManifestFile
	if err := json.Unmarshal(body, &manifest); err != nil {
		return "", err
	}
	return strings.TrimSpace(manifest.Name), nil
}

// marketplaceManifestHasSymlink reports whether any manifest path component is
// a symlink, which lets a checkout change without a new revision.
func marketplaceManifestHasSymlink(root string) bool {
	for _, relative := range marketplaceManifestRelativePaths {
		current := strings.TrimSpace(root)
		for _, part := range strings.Split(filepath.Clean(relative), string(os.PathSeparator)) {
			if part == "" || part == "." {
				continue
			}
			current = filepath.Join(current, part)
			info, err := os.Lstat(current)
			if err != nil {
				break
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return true
			}
		}
	}
	return false
}
