package pets

type CatalogPet struct {
	ID              string
	Name            string
	DisplayName     string
	Description     string
	SpritesheetFile string
}

const (
	DefaultFrameWidth   = 192
	DefaultFrameHeight  = 208
	DefaultFrameColumns = 8
	DefaultFrameRows    = 9
	SpritesheetWidth    = DefaultFrameWidth * DefaultFrameColumns
	SpritesheetHeight   = DefaultFrameHeight * DefaultFrameRows
)

var builtinPets = []CatalogPet{
	{ID: "codex", Name: "Codex", DisplayName: "Codex", Description: "The original Codex companion", SpritesheetFile: "codex-spritesheet-v4.webp"},
	{ID: "dewey", Name: "Dewey", DisplayName: "Dewey", Description: "A tidy duck for calm workspace days", SpritesheetFile: "dewey-spritesheet-v4.webp"},
	{ID: "fireball", Name: "Fireball", DisplayName: "Fireball", Description: "Hot path energy for fast iteration", SpritesheetFile: "fireball-spritesheet-v4.webp"},
	{ID: "rocky", Name: "Rocky", DisplayName: "Rocky", Description: "A steady rock when the diff gets large", SpritesheetFile: "rocky-spritesheet-v4.webp"},
	{ID: "seedy", Name: "Seedy", DisplayName: "Seedy", Description: "Small green shoots for new ideas", SpritesheetFile: "seedy-spritesheet-v4.webp"},
	{ID: "stacky", Name: "Stacky", DisplayName: "Stacky", Description: "A balanced stack for deep work", SpritesheetFile: "stacky-spritesheet-v4.webp"},
	{ID: "bsod", Name: "BSOD", DisplayName: "BSOD", Description: "A tiny blue-screen gremlin", SpritesheetFile: "bsod-spritesheet-v4.webp"},
	{ID: "null-signal", Name: "Null Signal", DisplayName: "Null Signal", Description: "Quiet signal from the void", SpritesheetFile: "null-signal-spritesheet-v4.webp"},
}

func BuiltinCatalog() []CatalogPet {
	out := make([]CatalogPet, len(builtinPets))
	copy(out, builtinPets)
	return out
}

func BuiltinPet(id string) (CatalogPet, bool) {
	for _, pet := range builtinPets {
		if pet.ID == id {
			return pet, true
		}
	}
	return CatalogPet{}, false
}
