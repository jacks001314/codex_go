package tea

import (
	"context"
	"testing"

	"codex_go/config"
)

// Rust #61988/#61985: choosing a picker row hands the full provider
// advertisement to the reader (provider_from_picker), so the resolve request
// carries it instead of re-querying the global inventory by saved identity.
func TestStatuslineProviderPickerHandsAdvertisementToReaderLikeRust(t *testing.T) {
	writes := []SettingsEdit{}
	discover := statuslineProviderDiscovered(t, StatuslineProviderCandidate{
		Server:          "fixture",
		Tool:            "zeta",
		Title:           "Zeta",
		URI:             "ui://fixture/zeta",
		PluginName:      "statusline",
		MarketplaceName: "openai-curated",
	})
	model := statuslineProviderPickerModel(t, nil, discover, &writes)
	var resolved []*StatuslineProvider
	model.onResolveStatuslineProvider = func(_ context.Context, request StatuslineProviderReadRequest) (StatuslineProvider, error) {
		resolved = append(resolved, request.Advertised)
		if request.Advertised != nil {
			return *request.Advertised, nil
		}
		return StatuslineProvider{Found: true, Title: "Fixture Line", URI: "ui://fixture/line"}, nil
	}
	model.onReadStatuslineProvider = func(_ context.Context, request StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
		return []StatuslineProviderContent{{
			URI:      request.URI,
			MIMEType: "application/json",
			Text:     `"ready"`,
			IsText:   true,
		}}, nil
	}

	model.openStatusLineSetup()
	discovery := model.toggleStatusSetupSelection()
	model.Update(discovery())
	model.modal.selected = 1
	save := model.activateModalOption(1)
	if save == nil {
		t.Fatal("provider choice did not save the selection")
	}
	model.Update(save())

	read := model.statuslineProviderRead
	if read == nil || read.advertised == nil {
		t.Fatalf("reader did not retain the picker advertisement: %#v", read)
	}
	if read.advertised.Server != "fixture" || read.advertised.Tool != "zeta" ||
		read.advertised.URI != "ui://fixture/zeta" ||
		read.advertised.PluginName != "statusline" || read.advertised.MarketplaceName != "openai-curated" {
		t.Fatalf("advertisement = %#v", read.advertised)
	}
	resolveMsg := model.statuslineProviderResolveCmd(read)()
	_, next := model.Update(resolveMsg)
	if next != nil {
		runProviderCmd(model, next)
	}
	if len(resolved) != 1 || resolved[0] == nil {
		t.Fatalf("resolve requests = %#v, want one carrying the advertisement", resolved)
	}
	if got := model.statusLineContent(); got != "ready" {
		t.Fatalf("provider footer = %q", got)
	}
}

// Rust #61988: the advertisement survives a reader rebuild (for example when the
// thread id arrives) and is dropped once it no longer matches the selection.
func TestStatuslineProviderAdvertisementSurvivesRefreshLikeRust(t *testing.T) {
	model := statuslineProviderPickerModel(t, &config.StatusLineProviderSelection{Server: "fixture", Tool: "line"}, nil, &[]SettingsEdit{})
	model.statuslinePickedProvider = &StatuslineProvider{
		Found:  true,
		Server: "fixture",
		Tool:   "line",
		Title:  "Fixture Line",
		URI:    "ui://fixture/line",
	}
	model.retireStatuslineProviderRead()
	model.refreshStatusControls()
	if read := model.statuslineProviderRead; read == nil || read.advertised == nil {
		t.Fatalf("refreshed reader dropped the advertisement: %#v", read)
	}

	model.statuslinePickedProvider = &StatuslineProvider{Found: true, Server: "other", Tool: "line"}
	model.retireStatuslineProviderRead()
	model.refreshStatusControls()
	if read := model.statuslineProviderRead; read == nil || read.advertised != nil {
		t.Fatalf("mismatched advertisement was kept: %#v", read)
	}
}
