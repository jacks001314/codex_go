package app

import (
	"testing"

	"codex_go/cli"
)

// Rust #62127 picks each resume-picker source's initial page mode per backend:
// a local workspace starts from the state index (StateDbOnly) and a remote
// workspace uses the store default. Go lists one source per picker, so the
// shared listing mode that made local backends fall back in Rust cannot occur;
// this test pins the remote half (the local half is the state-index-first page
// in interactiveSessionPickerItemsForScope).
func TestSessionPickerSourceModesMatchRustPerBackendDefaults(t *testing.T) {
	params := remoteTUIThreadListParams(&cli.RootOptions{}, false)
	if params.UseStateDBOnly {
		t.Fatal("remote picker source must use the store default, not the state index")
	}
}
