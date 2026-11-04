package protocol

import (
	"github.com/google/uuid"
)

// ItemIDGenerator mints a response item id for a type prefix (Rust #59312
// `ItemIdGenerator`). Sessions own one generator and share it with every
// consumer that needs a new item identity.
type ItemIDGenerator func(prefix string) string

// ResponseItemIDWithSuffix builds `<prefix>_<suffix>`, the ResponseItemId
// shape Rust uses for generated and host-admitted item ids.
func ResponseItemIDWithSuffix(prefix string, suffix string) string {
	return prefix + "_" + suffix
}

// NewUUIDv7ItemIDGenerator returns the default generator: `<prefix>_<uuidv7>`
// for every item family.
func NewUUIDv7ItemIDGenerator() ItemIDGenerator {
	return func(prefix string) string {
		if id, err := uuid.NewV7(); err == nil {
			return ResponseItemIDWithSuffix(prefix, id.String())
		}
		return ResponseItemIDWithSuffix(prefix, uuid.NewString())
	}
}
