package exec

import (
	"bytes"
	"strings"
	"testing"

	"codex_go/model"
)

func TestExecRendersAuthRecoveryProgressLikeRust(t *testing.T) {
	var stderr bytes.Buffer
	sink := newExecEventSink(nil /*encodeJSON*/, false)
	sink.human = newExecHumanRenderer(&stderr, "never")

	sink.emitAuthRecovery(model.ProviderAuthRecoveryEvent{
		Phase:   model.ProviderAuthRecoveryPhaseStarted,
		Message: "AWS session has expired. Reauthenticating...",
	})
	sink.emitAuthRecovery(model.ProviderAuthRecoveryEvent{
		Phase:   model.ProviderAuthRecoveryPhaseCompleted,
		Message: "Signed in with AWS.",
	})

	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) != 2 ||
		lines[0] != "AWS session has expired. Reauthenticating..." ||
		lines[1] != "Signed in with AWS." {
		t.Fatalf("auth recovery output = %q", stderr.String())
	}
}
