package config

import "testing"

func TestMarketplaceAutoUpdateTypeLikeRust(t *testing.T) {
	for _, value := range []any{true, false, "true", 1, []any{true}} {
		values := map[string]any{"marketplaces": map[string]any{"team": map[string]any{"auto_update": value}}}
		_, boolean := value.(bool)
		if err := ValidateConfigValues(values); (err == nil) != boolean {
			t.Errorf("auto_update=%#v error=%v, want valid=%v", value, err, boolean)
		}
		if boolean {
			if err := validateKnownTopLevelConfigFields(values); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := ValidateConfigValues(map[string]any{"marketplaces": map[string]any{"team": map[string]any{}}}); err != nil {
		t.Fatalf("omitted auto_update: %v", err)
	}
}
