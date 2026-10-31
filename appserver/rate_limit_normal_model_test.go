package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/chatgptapi"
)

// Mirrors Rust #42372: the app-server rate-limit snapshot exposes the alias
// model as normalModelSlug so clients can render the associated model's display
// metadata without changing the request model.
func TestAccountRateLimitFromBackendCarriesNormalModelSlug(t *testing.T) {
	slug := "gpt-6-sol"
	limitID := "codex_luna_reserve"
	limitName := "Luna Reserve"
	converted := accountRateLimitFromBackend(&chatgptapi.RateLimitSnapshot{
		LimitID:         &limitID,
		LimitName:       &limitName,
		NormalModelSlug: &slug,
	})
	if converted.NormalModelSlug == nil || *converted.NormalModelSlug != slug {
		t.Fatalf("NormalModelSlug = %v, want %q", converted.NormalModelSlug, slug)
	}
	payload, err := json.Marshal(converted)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if !strings.Contains(string(payload), `"normalModelSlug":"gpt-6-sol"`) {
		t.Fatalf("snapshot JSON = %s, want normalModelSlug", payload)
	}

	// The primary quota keeps the field null.
	primary := accountRateLimitFromBackend(&chatgptapi.RateLimitSnapshot{LimitID: &limitID})
	payload, err = json.Marshal(primary)
	if err != nil {
		t.Fatalf("marshal primary snapshot: %v", err)
	}
	if !strings.Contains(string(payload), `"normalModelSlug":null`) {
		t.Fatalf("primary snapshot JSON = %s, want normalModelSlug null", payload)
	}
}
