package execpolicy

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type LegacyAllowRules struct {
	Commands [][]string `json:"commands"`
}

func MigrateLegacyAllowRules(legacyPath, policyPath string) (int, error) {
	data, err := os.ReadFile(legacyPath)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var legacy LegacyAllowRules
	if err := json.Unmarshal(data, &legacy); err != nil {
		return 0, fmt.Errorf("parse legacy allow rules: %w", err)
	}
	added := 0
	for _, command := range legacy.Commands {
		pattern := make([]string, 0, len(command))
		for _, token := range command {
			if strings.TrimSpace(token) != "" {
				pattern = append(pattern, strings.TrimSpace(token))
			}
		}
		if len(pattern) == 0 {
			continue
		}
		encoded, _ := json.Marshal(pattern)
		line := "prefix_rule(pattern=" + string(encoded) + ", decision=\"allow\", justification=\"migrated legacy allow rule\")"
		before, _ := os.ReadFile(policyPath)
		if err := appendRuleLine(policyPath, line); err != nil {
			return added, err
		}
		after, _ := os.ReadFile(policyPath)
		if len(after) > len(before) {
			added++
		}
	}
	return added, nil
}
