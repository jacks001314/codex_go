package tea

import (
	"context"
	"errors"
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/config"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// Rust #61979 statusline_reader_tests.rs:
// statusline_resource_requires_one_matching_json_text_item.
func TestStatuslineProviderDecodeRequiresOneMatchingJSONTextItem(t *testing.T) {
	const uri = "ui://provider/footer"
	text := func(mime string, body string) StatuslineProviderContent {
		return StatuslineProviderContent{URI: uri, MIMEType: mime, Text: body, IsText: true}
	}
	if _, err := decodeStatuslineProviderTree(uri, []StatuslineProviderContent{
		text("APPLICATION/JSON; charset=utf-8", "null"),
	}); err != nil {
		t.Fatalf("valid media type rejected: %v", err)
	}
	invalid := map[string][]StatuslineProviderContent{
		"empty": {},
		"two items": {
			text("application/json", "null"),
			text("application/json", "null"),
		},
		"wrong uri": {{
			URI: "ui://wrong", MIMEType: "application/json", Text: "null", IsText: true,
		}},
		"missing mime": {{
			URI: uri, Text: "null", IsText: true,
		}},
		"blob": {{
			URI: uri, MIMEType: "application/json", IsText: false,
		}},
		"wrong media type":  {text("text/plain", "null")},
		"invalid json tree": {text("application/json", "{")},
	}
	for name, contents := range invalid {
		if _, err := decodeStatuslineProviderTree(uri, contents); err == nil {
			t.Fatalf("%s: decode succeeded, want failure", name)
		}
	}
}

func statuslineProviderFooterModel(
	t *testing.T,
	resolve func(context.Context, StatuslineProviderReadRequest) (StatuslineProvider, error),
	read func(context.Context, StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error),
) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-provider")
	state.SetStatus("idle")
	state.CWD = t.TempDir()
	model := NewModel(state, Options{
		Width:           120,
		Height:          30,
		FeatureSettings: map[string]bool{"tui_extensions_statusline": true},
		StatusLineProvider: &config.StatusLineProviderSelection{
			Server: "fixture",
			Tool:   "line",
		},
		OnResolveStatuslineProvider: resolve,
		OnReadStatuslineProvider:    read,
	})
	model.width, model.height = 120, 30
	return model
}

// runProviderCmd executes one command and feeds a single resulting message back
// through Update, returning Update's follow-up command.
func runProviderCmd(model *Model, cmd bubbletea.Cmd) bubbletea.Cmd {
	if cmd == nil {
		return nil
	}
	_, next := model.Update(cmd())
	return next
}

// Rust #61979: the read starts with the initialization copy, resolves the saved
// provider, sends the displayed thread's snapshot, and installs the UI tree.
func TestStatuslineProviderFooterLifecycleLikeRust(t *testing.T) {
	var resolves, reads int
	var snapshot map[string]any
	model := statuslineProviderFooterModel(t,
		func(ctx context.Context, request StatuslineProviderReadRequest) (StatuslineProvider, error) {
			resolves++
			if request.ThreadID != "thread-provider" || request.Server != "fixture" || request.Tool != "line" {
				t.Fatalf("unexpected resolve request: %#v", request)
			}
			return StatuslineProvider{Found: true, Title: "Fixture Line", URI: "ui://fixture/line"}, nil
		},
		func(ctx context.Context, request StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			reads++
			if request.ThreadID != "thread-provider" || request.Server != "fixture" || request.URI != "ui://fixture/line" {
				t.Fatalf("unexpected read request: %#v", request)
			}
			snapshot = request.Snapshot
			return []StatuslineProviderContent{{
				URI:      request.URI,
				MIMEType: "application/json",
				Text:     `"Ready"`,
				IsText:   true,
			}}, nil
		})

	model.refreshStatusControls()
	if got := model.statusLineContent(); got != statuslineProviderInitializing {
		t.Fatalf("initializing footer = %q", got)
	}
	next := runProviderCmd(model, model.ensureProviderFooterCmd())
	next = runProviderCmd(model, next)
	if next != nil {
		_ = next()
	}
	if resolves != 1 || reads != 1 {
		t.Fatalf("resolves/reads = %d/%d", resolves, reads)
	}
	if got := model.statusLineContent(); got != "Ready" {
		t.Fatalf("provider footer = %q", got)
	}
	thread, _ := snapshot["thread"].(map[string]any)
	if thread["id"] != "thread-provider" {
		t.Fatalf("snapshot thread id = %#v", thread["id"])
	}
	app, _ := snapshot["app"].(map[string]any)
	if app["display_mode"] != "standard" {
		t.Fatalf("snapshot display mode = %#v", app["display_mode"])
	}
	configState, _ := snapshot["config"].(map[string]any)
	items, _ := configState["statusline_items"].([]string)
	if strings.Join(items, ",") != "model-with-reasoning,current-dir,thread-name" {
		t.Fatalf("snapshot items = %#v", configState["statusline_items"])
	}

	// A refresh keeps the provider content instead of rebuilding the native line.
	model.refreshStatusControls()
	if got := model.statusLineContent(); got != "Ready" {
		t.Fatalf("refresh dropped provider content: %q", got)
	}
	if resolves != 1 || reads != 1 {
		t.Fatalf("refresh restarted the read: %d/%d", resolves, reads)
	}
}

// Rust #61979: results for a retired thread, provider, or working directory
// never paint the footer.
func TestStatuslineProviderReadsDropStaleResultsLikeRust(t *testing.T) {
	model := statuslineProviderFooterModel(t,
		func(ctx context.Context, request StatuslineProviderReadRequest) (StatuslineProvider, error) {
			return StatuslineProvider{Found: true, Title: "Fixture Line", URI: "ui://fixture/line"}, nil
		},
		func(ctx context.Context, request StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			return []StatuslineProviderContent{{
				URI:      request.URI,
				MIMEType: "application/json",
				Text:     `"Old"`,
				IsText:   true,
			}}, nil
		})

	model.refreshStatusControls()
	read := model.statuslineProviderRead
	if read == nil {
		t.Fatal("provider read was not staged")
	}
	next := runProviderCmd(model, model.ensureProviderFooterCmd())
	resolveCmd := next
	if resolveCmd == nil {
		t.Fatal("resolve command was not produced")
	}

	// Another thread retires the in-flight read.
	model.State.SetThreadID("thread-other")
	model.refreshStatusControls()
	if model.statuslineProviderRead == nil || model.statuslineProviderRead.threadID != "thread-other" {
		t.Fatalf("new thread did not stage its own read: %#v", model.statuslineProviderRead)
	}
	model.applyStatuslineProviderRead(statuslineProviderReadMsg{
		token:    read.token,
		threadID: read.threadID,
		server:   read.server,
		contents: []StatuslineProviderContent{{URI: "ui://fixture/line", MIMEType: "application/json", Text: `"Old"`, IsText: true}},
	})
	if got := model.statusLineContent(); got != statuslineProviderInitializing {
		t.Fatalf("stale result painted the footer: %q", got)
	}

	// A failed or malformed response shows the Rust failure copy.
	model.applyStatuslineProviderRead(statuslineProviderReadMsg{
		token:    model.statuslineProviderRead.token,
		threadID: model.statuslineProviderRead.threadID,
		server:   model.statuslineProviderRead.server,
		err:      errors.New("boom"),
	})
	want := "Error loading statusline contents from line. Use /statusline to (re)select your provider and try again."
	if got := model.statusLineContent(); got != want {
		t.Fatalf("failure copy = %q", got)
	}
}

// Rust #61979: selecting a different provider or leaving read-only history
// restarts the read, and read-only history hides the provider footer.
func TestStatuslineProviderSelectionChangesLikeRust(t *testing.T) {
	model := statuslineProviderFooterModel(t,
		func(ctx context.Context, request StatuslineProviderReadRequest) (StatuslineProvider, error) {
			return StatuslineProvider{Found: true, Title: request.Tool, URI: "ui://fixture/" + request.Tool}, nil
		},
		func(ctx context.Context, request StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			return []StatuslineProviderContent{{
				URI:      request.URI,
				MIMEType: "application/json",
				Text:     `"Ready"`,
				IsText:   true,
			}}, nil
		})
	model.refreshStatusControls()
	runProviderCmd(model, runProviderCmd(model, model.ensureProviderFooterCmd()))
	if got := model.statusLineContent(); got != "Ready" {
		t.Fatalf("provider footer = %q", got)
	}
	firstToken := model.statuslineProviderRead.token

	model.statusLineProvider = &config.StatusLineProviderSelection{Server: "fixture", Tool: "other"}
	model.refreshStatusControls()
	if model.statuslineProviderRead == nil || model.statuslineProviderRead.tool != "other" {
		t.Fatalf("selection change did not restage the read: %#v", model.statuslineProviderRead)
	}
	if model.statuslineProviderRead.token == firstToken {
		t.Fatal("selection change reused the retired token")
	}
	if got := model.statusLineContent(); got != statuslineProviderInitializing {
		t.Fatalf("selection change kept old content: %q", got)
	}

	model.readOnlyThread = true
	model.refreshStatusControls()
	if model.statuslineProviderRead != nil {
		t.Fatal("read-only history kept a provider read")
	}
	if got := model.statusLineContent(); got != "" {
		t.Fatalf("read-only history painted a footer: %q", got)
	}

	model.readOnlyThread = false
	model.refreshStatusControls()
	if model.statuslineProviderRead == nil || model.statusLineContent() != statuslineProviderInitializing {
		t.Fatalf("leaving read-only history did not restart the read: %#v", model.statuslineProviderRead)
	}
}

// A host that cannot serve providers keeps the built-in status line.
func TestStatuslineProviderWithoutHostHooksKeepsBuiltinLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-provider")
	model := NewModel(state, Options{
		Width:           120,
		Height:          30,
		FeatureSettings: map[string]bool{"tui_extensions_statusline": true},
		StatusLineProvider: &config.StatusLineProviderSelection{
			Server: "fixture",
			Tool:   "line",
		},
	})
	model.refreshStatusControls()
	if model.statuslineProviderRead != nil || model.statusLineExtension != nil {
		t.Fatal("provider footer started without host hooks")
	}
}

// Rust #61979 selected_thread_usage: a provider footer always selects credits
// (except on ENT26) and the estimated cost, and read-only history stops the
// built-in item scan.
func TestStatuslineProviderThreadUsageSelectionLikeRust(t *testing.T) {
	model := statuslineProviderFooterModel(t,
		func(ctx context.Context, request StatuslineProviderReadRequest) (StatuslineProvider, error) {
			return StatuslineProvider{Found: true, Title: "line", URI: "ui://fixture/line"}, nil
		},
		func(ctx context.Context, request StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			return nil, errors.New("unused")
		})
	model.ensureStatusControls()
	model.statusControls.StatusLineConfigured = true
	model.statusControls.StatusLineIDs = []string{"model"}
	model.statusControls.TerminalTitleConfigured = true
	model.statusControls.TerminalTitleIDs = []string{}

	credits, cost := model.threadUsageSelection()
	if !credits || !cost {
		t.Fatalf("provider selection = credits %v cost %v", credits, cost)
	}
	model.chatGPTPlanType = "ent26"
	credits, cost = model.threadUsageSelection()
	if credits || !cost {
		t.Fatalf("ENT26 provider selection = credits %v cost %v", credits, cost)
	}

	model.statusLineProvider = nil
	model.chatGPTPlanType = ""
	model.statusControls.StatusLineIDs = []string{"thread-credits", "estimated-thread-cost"}
	model.readOnlyThread = true
	if credits, cost := model.threadUsageSelection(); credits || cost {
		t.Fatalf("read-only history kept built-in usage = credits %v cost %v", credits, cost)
	}
	model.readOnlyThread = false
	if credits, cost := model.threadUsageSelection(); !credits || !cost {
		t.Fatalf("built-in usage selection = credits %v cost %v", credits, cost)
	}

	// A provider keeps polling usage even with no built-in item selected.
	model.statusLineProvider = &config.StatusLineProviderSelection{Server: "fixture", Tool: "line"}
	model.statusControls.StatusLineIDs = []string{}
	model.ensureThreadUsageForSurfaces(chatwidget.NewStatusSurfaceSelections([]string{}, []string{}))
	if !model.threadUsageSurfaceSelected {
		t.Fatal("provider footer did not select thread usage")
	}
}
