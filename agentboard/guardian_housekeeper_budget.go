package agentboard

import (
	"encoding/json"
	"errors"
	"fmt"
)

// GuardianRoutingMaxSourceJSONBytes bounds one original's serialized bytes in a
// routing request, matching Rust's 64 KiB per-original encoding limit
// (guardian-v3 housekeeper).
const GuardianRoutingMaxSourceJSONBytes = 64 * 1024

// guardianRoutingInput is the serialized request a decider receives, mirroring
// Rust's RoutingInput: the board's channel catalog plus the originals in source
// order.
type guardianRoutingInput struct {
	Catalog map[string]*string      `json:"catalog"`
	Sources []GuardianRoutingSource `json:"sources"`
}

// GuardianRoutingBatchPrefix returns the largest prefix of sources whose
// serialized routing input fits in byteBudget, mirroring Rust #62372's batching:
// the catalog and the array delimiters count first, then each original's
// serialized bytes plus the separating comma. A prefix stops before the first
// original that does not fit; when even one original does not fit the caller
// must stop without advancing its cursor, so the function reports Rust's
// budget error.
func GuardianRoutingBatchPrefix(catalog map[string]*string, sources []GuardianRoutingSource, byteBudget int) (int, error) {
	if len(sources) == 0 {
		return 0, nil
	}
	if byteBudget <= 0 {
		return 0, errors.New("housekeeper catalog and original exceed the model's tool-output budget")
	}
	empty, err := json.Marshal(guardianRoutingInput{Catalog: catalog, Sources: []GuardianRoutingSource{}})
	if err != nil {
		return 0, err
	}
	inputBytes := len(empty)
	count := 0
	for _, source := range sources {
		encoded, err := json.Marshal(source)
		if err != nil {
			return 0, err
		}
		if len(encoded) > GuardianRoutingMaxSourceJSONBytes {
			return 0, fmt.Errorf("guardian routing original exceeds %d serialized bytes", GuardianRoutingMaxSourceJSONBytes)
		}
		added := len(encoded)
		if count > 0 {
			// Every original after the first adds its separating comma.
			added++
		}
		if added > byteBudget-inputBytes {
			break
		}
		inputBytes += added
		count++
	}
	if count == 0 {
		return 0, errors.New("housekeeper catalog and original exceed the model's tool-output budget")
	}
	return count, nil
}
