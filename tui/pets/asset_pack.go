package pets

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp" // decode CDN WebP spritesheets
)

const (
	PackVersion      = "v1"
	PackDir          = "cache/tui-pets"
	PetCDNBaseURL    = "https://persistent.oaistatic.com/codex/pets/v1"
	MaxDownloadBytes = 4 * 1024 * 1024
	DownloadTimeout  = 60 * time.Second
)

type AssetPack struct {
	ID      string
	Version string
}

// AssetFetchFunc downloads at most maxBytes bytes from url. It is injectable
// so tests can avoid the network and callers can reuse the app HTTP stack.
type AssetFetchFunc func(url string, maxBytes int64) ([]byte, error)

// HTTPAssetFetch returns an AssetFetchFunc backed by net/http with a bounded
// body read and the DownloadTimeout deadline.
func HTTPAssetFetch(client *http.Client) AssetFetchFunc {
	if client == nil {
		client = &http.Client{Timeout: DownloadTimeout}
	}
	return func(url string, maxBytes int64) ([]byte, error) {
		request, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("pet asset download from %s: HTTP %d", url, response.StatusCode)
		}
		if response.ContentLength > maxBytes {
			return nil, fmt.Errorf("pet asset download from %s exceeded %d bytes", url, maxBytes)
		}
		limited := io.LimitReader(response.Body, maxBytes+1)
		payload, err := io.ReadAll(limited)
		if err != nil {
			return nil, err
		}
		if int64(len(payload)) > maxBytes {
			return nil, fmt.Errorf("pet asset download from %s exceeded %d bytes", url, maxBytes)
		}
		return payload, nil
	}
}

// EnsureBuiltinPet makes sure the built-in pet's spritesheet is present under
// codexHome with the expected geometry. A valid cached file short-circuits the
// download; otherwise the asset is fetched from the CDN, validated, and
// installed atomically. It returns the spritesheet path.
func EnsureBuiltinPet(codexHome string, pet CatalogPet, fetch AssetFetchFunc) (string, error) {
	if strings.TrimSpace(pet.SpritesheetFile) == "" {
		return "", errors.New("pet has no spritesheet file")
	}
	destination := BuiltinSpritesheetPath(codexHome, pet.SpritesheetFile)
	if err := validateSpritesheet(destination); err == nil {
		return destination, nil
	}
	url := BuiltinPetURL(pet)
	if url == "" {
		return "", errors.New("pet has no CDN URL")
	}
	if fetch == nil {
		fetch = HTTPAssetFetch(nil)
	}
	payload, err := fetch(url, MaxDownloadBytes)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	staging := filepath.Join(parent, fmt.Sprintf(".%s.download-%d.webp", pet.SpritesheetFile, time.Now().UnixNano()))
	if err := os.WriteFile(staging, payload, 0o644); err != nil {
		return "", err
	}
	defer os.Remove(staging)
	if err := validateSpritesheet(staging); err != nil {
		return "", err
	}
	if err := os.Rename(staging, destination); err != nil {
		// Another process may have installed a valid copy concurrently.
		if validateSpritesheet(destination) == nil {
			return destination, nil
		}
		return "", err
	}
	return destination, nil
}

// FrameCacheKey derives a stable cache directory key from the spritesheet
// contents and the frame grid, mirroring the Rust frame-cache layout.
func FrameCacheKey(spritesheetPath string, frameWidth int, frameHeight int, columns int, rows int) (string, error) {
	payload, err := os.ReadFile(spritesheetPath)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256-%x-%dx%d-%dx%d", digest, frameWidth, frameHeight, columns, rows), nil
}

func BuiltinSpritesheetPath(codexHome string, file string) string {
	return filepath.Join(codexHome, PackDir, PackVersion, "assets", file)
}

func BuiltinPetURL(pet CatalogPet) string {
	if pet.SpritesheetFile == "" {
		return ""
	}
	return PetCDNBaseURL + "/" + pet.SpritesheetFile
}

func validateSpritesheet(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if config.Width != SpritesheetWidth || config.Height != SpritesheetHeight {
		return fmt.Errorf(
			"invalid pet spritesheet dimensions for %s: expected %dx%d, got %dx%d",
			path, SpritesheetWidth, SpritesheetHeight, config.Width, config.Height,
		)
	}
	return nil
}
