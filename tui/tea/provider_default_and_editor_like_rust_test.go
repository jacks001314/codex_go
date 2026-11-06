package tea

import (
	"errors"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

func providerPageWithDefaultControl() model.ProviderListResponse {
	page := providerBrowserPage()
	page.DefaultProviderID = "openai"
	page.CanChangeDefault = true
	page.Data[1].CanEdit = true
	return page
}

func TestConfigPanelProviderDefaultSelectionLikeRust(t *testing.T) {
	var wrote []config.ConfigBatchWriteParams
	m := NewModel(codextui.NewState(nil), Options{
		Width:                100,
		Height:               30,
		OnListModelProviders: func() (model.ProviderListResponse, error) { return providerPageWithDefaultControl(), nil },
		OnWriteHookConfig: func(params config.ConfigBatchWriteParams) error {
			wrote = append(wrote, params)
			return nil
		},
	})
	m.State.ThreadID = "thread-providers"
	m.State.Provider = "openai"
	cmd := openProviderBrowser(t, m)
	m = applyCmd(t, m, cmd)
	view := utils.StripANSI(m.viewString())
	for _, want := range []string{"● OpenAI", "○ Gateway", "space set default"} {
		if !strings.Contains(view, want) {
			t.Fatalf("provider list missing %q:\n%s", want, view)
		}
	}
	m.Update(key(keymsg.KeyDown))
	updated, writeCmd := m.Update(key(keymsg.KeySpace))
	m = updated.(*Model)
	if writeCmd == nil {
		t.Fatal("space/default action produced no write")
	}
	m = applyCmd(t, m, writeCmd)
	if len(wrote) != 1 || len(wrote[0].Edits) != 1 || wrote[0].Edits[0].KeyPath != "model_provider" || wrote[0].Edits[0].Value != "gateway" {
		t.Fatalf("default provider write = %#v", wrote)
	}
	reload := m.modal.configPanel.providers
	if reload == nil || reload.defaultID != "gateway" || !strings.Contains(reload.notice, "/new now uses gateway") {
		t.Fatalf("saved default state = %#v", reload)
	}
}

func TestConfigPanelProviderClientOverrideLocksDefaultLikeRust(t *testing.T) {
	m := NewModel(codextui.NewState(nil), Options{
		Width:                       100,
		Height:                      30,
		ModelProviderClientOverride: "gateway",
		OnListModelProviders:        func() (model.ProviderListResponse, error) { return providerPageWithDefaultControl(), nil },
	})
	cmd := openProviderBrowser(t, m)
	m = applyCmd(t, m, cmd)
	view := utils.StripANSI(m.viewString())
	// Rust keeps the subtitle's default id from the server page and names the
	// client's pinned provider only in the footer note.
	if !strings.Contains(view, "Server default: openai") || !strings.Contains(view, "This client's launch settings select gateway") {
		t.Fatalf("client override view = %q", view)
	}
	if strings.Contains(view, "space set default") {
		t.Fatalf("client override still offered default selection: %q", view)
	}
}

func TestConfigPanelProviderEditorWritesSelectedFieldsLikeRust(t *testing.T) {
	var patch model.ProviderConfigWriteParams
	m := NewModel(codextui.NewState(nil), Options{
		Width:                100,
		Height:               30,
		OnListModelProviders: func() (model.ProviderListResponse, error) { return providerPageWithDefaultControl(), nil },
		OnWriteModelProviderConfig: func(params model.ProviderConfigWriteParams) (model.ProviderConfigWriteResponse, error) {
			patch = params
			return model.ProviderConfigWriteResponse{}, nil
		},
	})
	m.State.ThreadID = "thread-providers"
	m.State.Provider = "openai"
	cmd := openProviderBrowser(t, m)
	m = applyCmd(t, m, cmd)
	m.Update(key(keymsg.KeyDown))
	m.Update(key(keymsg.KeyEnter))
	if view := utils.StripANSI(m.viewString()); !strings.Contains(view, "enter Edit configuration") {
		t.Fatalf("details missing edit action:\n%s", view)
	}
	m.Update(key(keymsg.KeyEnter))
	editor := m.modal.configPanel.providers.editor
	if editor == nil {
		t.Fatal("provider editor did not open")
	}
	editor.fields[0].value = "Gateway Two"
	editor.fields[1].value = "https://new.example.com/v1"
	editor.fields[2].value = "NEW_KEY"
	editor.selected = len(editor.fields)
	writeCmd := m.updateConfigProviderEditorKey(key(keymsg.KeyEnter), m.modal.configPanel.providers)
	if writeCmd == nil {
		t.Fatalf("editor save produced no command; error=%q", editor.err)
	}
	m = applyCmd(t, m, writeCmd)
	if patch.Settings.Type != "customGateway" || patch.Settings.CustomGateway == nil {
		t.Fatalf("provider patch = %#v", patch)
	}
	custom := patch.Settings.CustomGateway
	if custom.ProviderID == nil || *custom.ProviderID != "gateway" || custom.Name == nil || *custom.Name != "Gateway Two" {
		t.Fatalf("custom provider identity = %#v", custom)
	}
	if !custom.BaseURL.Set || custom.BaseURL.Value == nil || *custom.BaseURL.Value != "https://new.example.com/v1" {
		t.Fatalf("base URL patch = %#v", custom.BaseURL)
	}
	if !custom.EnvKey.Set || custom.EnvKey.Value == nil || *custom.EnvKey.Value != "NEW_KEY" {
		t.Fatalf("env key patch = %#v", custom.EnvKey)
	}
	if m.modal.configPanel.providers.editor != nil {
		t.Fatal("editor stayed open after a successful save")
	}
}

func TestConfigPanelProviderEditorPreservesDraftOnFailureLikeRust(t *testing.T) {
	m := NewModel(codextui.NewState(nil), Options{
		Width:                100,
		Height:               30,
		OnListModelProviders: func() (model.ProviderListResponse, error) { return providerPageWithDefaultControl(), nil },
		OnWriteModelProviderConfig: func(model.ProviderConfigWriteParams) (model.ProviderConfigWriteResponse, error) {
			return model.ProviderConfigWriteResponse{}, errors.New("denied by requirements")
		},
	})
	cmd := openProviderBrowser(t, m)
	m = applyCmd(t, m, cmd)
	m.Update(key(keymsg.KeyDown))
	m.Update(key(keymsg.KeyEnter))
	m.Update(key(keymsg.KeyEnter))
	m.modal.configPanel.providers.editor.fields[0].value = "Draft Name"
	m.modal.configPanel.providers.editor.selected = len(m.modal.configPanel.providers.editor.fields)
	writeCmd := m.updateConfigProviderEditorKey(key(keymsg.KeyEnter), m.modal.configPanel.providers)
	m = applyCmd(t, m, writeCmd)
	editor := m.modal.configPanel.providers.editor
	if editor == nil || editor.fields[0].value != "Draft Name" || !strings.Contains(editor.err, "denied by requirements") {
		t.Fatalf("failed editor state = %#v", editor)
	}
}
