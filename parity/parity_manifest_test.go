package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const parityRustBaseline = "fa1d4c40d0"

type parityManifest struct {
	RustBaseline     string               `json:"rustBaseline"`
	RustTag          string               `json:"rustTag"`
	RustUpstreamHead string               `json:"rustUpstreamHead"`
	RustUpstreamTag  string               `json:"rustUpstreamTag"`
	Items            []parityManifestItem `json:"items"`
}

type parityManifestItem struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	UserImpact string `json:"userImpact"`
	Test       string `json:"test"`
}

func TestParityManifestIsMachineReadableAndComplete(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "parity.json"))
	if err != nil {
		t.Fatalf("ReadFile(parity.json): %v", err)
	}
	var manifest parityManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal(parity.json): %v", err)
	}
	if manifest.RustBaseline != parityRustBaseline || manifest.RustTag == "" || manifest.RustUpstreamHead == "" || manifest.RustUpstreamTag == "" || len(manifest.Items) == 0 {
		t.Fatalf("manifest header = %#v", manifest)
	}
	allowed := map[string]bool{"done": true, "partial": true, "missing": true, "intentional_difference": true}
	seen := map[string]bool{}
	for _, item := range manifest.Items {
		if item.ID == "" || seen[item.ID] || !allowed[item.Status] || item.Test == "" {
			t.Fatalf("invalid parity item = %#v", item)
		}
		seen[item.ID] = true
		if item.Status == "intentional_difference" && (item.Reason == "" || item.UserImpact == "") {
			t.Fatalf("intentional difference lacks reason or user impact = %#v", item)
		}
	}
}
