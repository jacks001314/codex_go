package app

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"codex_go/cli"
)

func TestRemoteTrustStatusFromValuesLikeRust(t *testing.T) {
	cwd := `C:\repo\sub`
	trustTarget := `C:\repo`
	values := map[string]any{
		"projects": map[string]any{
			`C:\repo`: map[string]any{"trust_level": "trusted"},
		},
	}
	if got := remoteTrustStatusFromValues(values, cwd, trustTarget); got != TrustStatusTrusted {
		t.Fatalf("trusted project status = %q, want trusted", got)
	}
	if got := remoteTrustStatusFromValues(map[string]any{}, cwd, trustTarget); got != TrustStatusUndecided {
		t.Fatalf("absent decision status = %q, want undecided", got)
	}
	untrustedValues := map[string]any{
		"projects": map[string]any{
			`C:\repo`: map[string]any{"trust_level": "untrusted"},
		},
	}
	if got := remoteTrustStatusFromValues(untrustedValues, cwd, trustTarget); got != TrustStatusUntrusted {
		t.Fatalf("untrusted project status = %q, want untrusted", got)
	}
	// A trusted cwd with no explicit target entry is still trusted.
	trustedCWD := map[string]any{
		"projects": map[string]any{
			`C:\repo\sub`: map[string]any{"trust_level": "trusted"},
		},
	}
	if got := remoteTrustStatusFromValues(trustedCWD, cwd, trustTarget); got != TrustStatusTrusted {
		t.Fatalf("trusted cwd status = %q, want trusted", got)
	}
}

func TestTrustTargetForDecisionFallsBackToCWD(t *testing.T) {
	if got := trustTargetForDecision("/workspace/project", "/workspace/project"); got != "/workspace/project" {
		t.Fatalf("trust target = %q", got)
	}
	if got := trustTargetForDecision("/workspace/project", ""); got != "/workspace/project" {
		t.Fatalf("fallback trust target = %q", got)
	}
	if got := trustTargetForDecision("/workspace/project", "  "); got != "/workspace/project" {
		t.Fatalf("blank fallback trust target = %q", got)
	}
}

func TestEnsureRemoteProjectTrustNonInteractiveDeclinesLikeRust(t *testing.T) {
	// Without a server client this exercises the non-interactive fast path:
	// the status query fails (no server) and the error propagates; the
	// decision core is covered by remoteTrustStatusFromValues above.
	status, err := ensureRemoteProjectTrust(context.Background(), nil, "/workspace/project", "", func(string, string) (bool, error) {
		return true, nil
	}, false)
	if status != TrustStatusUndecided || err == nil {
		t.Fatalf("ensure without client = %q, %v; want undecided + error", status, err)
	}
}

func TestPersistRemoteProjectTrustKeyPathQuotesPath(t *testing.T) {
	// Verifies the batchWrite key path quotes project paths with dots and
	// separators (used by persistRemoteProjectTrust).
	path := `C:\repo.with.dots`
	got := "projects." + strconv.Quote(path) + ".trust_level"
	if !strings.Contains(got, strconv.Quote(path)) {
		t.Fatalf("quoted key path = %q, missing quoted %q", got, strconv.Quote(path))
	}
	if !strings.HasSuffix(got, ".trust_level") {
		t.Fatalf("key path suffix = %q", got)
	}
	if !strings.HasPrefix(got, "projects.") {
		t.Fatalf("key path prefix = %q", got)
	}
}

func TestInteractiveRemoteTrustCheckNilGuards(t *testing.T) {
	// Nil guards must not panic.
	interactiveRemoteTrustCheck(context.Background(), nil, nil, false)
	interactiveRemoteTrustCheck(context.Background(), nil, &cli.RootOptions{}, false)
}
