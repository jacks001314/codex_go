package appserver

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func fixedTime() time.Time {
	return time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}
