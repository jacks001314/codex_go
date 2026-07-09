package pets

import "path/filepath"

const (
	PackVersion      = "v1"
	PackDir          = "cache/tui-pets"
	PetCDNBaseURL    = "https://persistent.oaistatic.com/codex/pets/v1"
	MaxDownloadBytes = 4 * 1024 * 1024
)

type AssetPack struct {
	ID      string
	Version string
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
