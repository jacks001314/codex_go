package appserver

import "testing"

func TestExternalAgentImportAnalyticsDefaultsSourceToCLI(t *testing.T) {
	if got := externalAgentImportAnalyticsSource(nil); got != "cli" {
		t.Fatalf("source = %q, want cli", got)
	}
}
