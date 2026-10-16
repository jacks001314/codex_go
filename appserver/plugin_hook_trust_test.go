package appserver

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex_go/plugin"
)

// pluginBundleFingerprintVector is the bundle fingerprint of
// pluginBundleFixture computed by an independent implementation of the Rust
// recipe (hooks/src/engine/plugin_fingerprint.rs, codex-rs #54619): sha256 over
// b"codex-plugin-bundle-v1", per-entry length-prefixed relative paths, a
// directory/file discriminator, little-endian lengths and file contents, with
// __pycache__/.venv/*.pyc/*.egg-info skipped and entries visited in ascending
// name order with a LIFO stack.
const pluginBundleFingerprintVector = "sha256:1b30ece2fc8dd5840ddd43127d3e0931034d3e3679e06fe398bbe82769d2c44b"

const pluginBundleHookJSON = `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"python3 ${PLUGIN_ROOT}/hooks/log_version.py"}]}]}}`

// writePluginBundleFixture materializes the bundle shared by the fingerprint
// vector and the discovery tests.
func writePluginBundleFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		filepath.Join("hooks", "hooks.json"):                       pluginBundleHookJSON,
		filepath.Join("scripts", "log_version.py"):                 "print(1)\n",
		filepath.Join("scripts", "__pycache__", "log_version.pyc"): "BYTECODE",
		filepath.Join(".venv", "lib", "site.py"):                   "# venv\n",
		filepath.Join("pkg", "thing.egg-info"):                     "name = thing\n",
		"notes.txt":                                                "hello",
	}
	for relative, body := range files {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", relative, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", relative, err)
		}
	}
}

// TestPluginContentHashMatchesRustBundleFingerprintLikeRust feeds the Rust
// plugin_fingerprint.rs recipe to the Go walker: the vector, the deterministic
// ordering, the skip rules and the failure modes must all agree.
func TestPluginContentHashMatchesRustBundleFingerprintLikeRust(t *testing.T) {
	root := t.TempDir()
	writePluginBundleFixture(t, root)

	got, err := pluginContentHash(root)
	if err != nil {
		t.Fatalf("pluginContentHash() error = %v", err)
	}
	if got != pluginBundleFingerprintVector {
		t.Fatalf("pluginContentHash() = %q, want %q", got, pluginBundleFingerprintVector)
	}
	again, err := pluginContentHash(root)
	if err != nil || again != got {
		t.Fatalf("pluginContentHash() second = %q, %v; want %q, nil", again, err, got)
	}

	// Generated Python artifacts are skipped, so adding an ignored file keeps
	// the approval valid (Rust plugin_fingerprint.rs skip predicate).
	ignored := filepath.Join(root, "scripts", "__pycache__", "extra.pyc")
	if err := os.WriteFile(ignored, []byte("MORE BYTECODE"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if skipped, err := pluginContentHash(root); err != nil || skipped != got {
		t.Fatalf("pluginContentHash() after ignored file = %q, %v; want %q, nil", skipped, err, got)
	}

	// Real content changes invalidate the fingerprint.
	notes := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(notes, []byte("hello!"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	changed, err := pluginContentHash(root)
	if err != nil {
		t.Fatalf("pluginContentHash() after change error = %v", err)
	}
	if changed == got {
		t.Fatalf("pluginContentHash() after content change = %q, want a different fingerprint", changed)
	}
	if err := os.WriteFile(notes, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if restored, err := pluginContentHash(root); err != nil || restored != got {
		t.Fatalf("pluginContentHash() after restore = %q, %v; want %q, nil", restored, err, got)
	}

	// A file cannot be a plugin root.
	if _, err := pluginContentHash(notes); err == nil || !strings.Contains(err.Error(), "plugin root is not a directory") {
		t.Fatalf("pluginContentHash(file) error = %v, want plugin root is not a directory", err)
	}
	if _, err := pluginContentHash(filepath.Join(root, "missing")); err == nil {
		t.Fatalf("pluginContentHash(missing) error = nil, want a filesystem error")
	}

	// Symlinks are neither directories nor regular files, so they are rejected
	// exactly like the Rust `unsupported plugin entry type` branch.
	target := filepath.Join(root, "notes.txt")
	link := filepath.Join(root, "notes-link.txt")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Logf("skipping symlink case: %v", err)
		} else {
			t.Fatalf("Symlink() error = %v", err)
		}
	} else {
		if _, err := pluginContentHash(root); err == nil || !strings.Contains(err.Error(), "unsupported plugin entry type") {
			t.Fatalf("pluginContentHash(symlink) error = %v, want unsupported plugin entry type", err)
		}
	}
}

func TestPluginHookTrustMigratesLegacyBaselineLikeRust(t *testing.T) {
	home := t.TempDir()
	configFile := filepath.Join(home, "config.toml")
	root := t.TempDir()
	writePluginBundleFixture(t, root)
	content, err := pluginContentHash(root)
	if err != nil {
		t.Fatalf("pluginContentHash() error = %v", err)
	}

	legacy := pluginLegacyTrustPath(configFile, "demo@market")
	if legacy == "" {
		t.Fatal("pluginLegacyTrustPath() = \"\", want a baseline path")
	}
	if want := filepath.Join(home, ".hook-trust"); filepath.Dir(legacy) != want {
		t.Fatalf("legacy baseline directory = %q, want %q", filepath.Dir(legacy), want)
	}
	if other := pluginLegacyTrustPath(configFile, "other@market"); other == legacy {
		t.Fatalf("pluginLegacyTrustPath() collides across plugin ids: %q", other)
	}
	if none := pluginLegacyTrustPath("", "demo@market"); none != "" {
		t.Fatalf("pluginLegacyTrustPath(no user layer) = %q, want \"\"", none)
	}

	// A declaration-only approval adopts the current contents as the one-time
	// immutable baseline (Rust migrate_legacy_trust).
	baseline, err := migrateLegacyTrust(legacy, content)
	if err != nil {
		t.Fatalf("migrateLegacyTrust() error = %v", err)
	}
	if baseline != content {
		t.Fatalf("migrateLegacyTrust() = %q, want %q", baseline, content)
	}
	if stored, err := os.ReadFile(legacy); err != nil || string(stored) != content {
		t.Fatalf("baseline file = %q, %v; want %q", string(stored), err, content)
	}

	trust := &pluginHookTrust{
		Root:            root,
		DeclarationHash: "sha256:declaration",
		TrustedHash:     "sha256:declaration",
		LegacyPath:      legacy,
	}
	if matched, err := trust.matchesHash(content); err != nil || !matched {
		t.Fatalf("matchesHash(same contents) = %v, %v; want true, nil", matched, err)
	}
	if matched, err := trust.matchesHash("sha256:other"); err != nil || matched {
		t.Fatalf("matchesHash(changed contents) = %v, %v; want false, nil", matched, err)
	}

	// A recorded content approval wins over the legacy baseline.
	approved := content
	trust.TrustedContentHash = &approved
	if matched, err := trust.matchesHash(content); err != nil || !matched {
		t.Fatalf("matchesHash(approved contents) = %v, %v; want true, nil", matched, err)
	}
	if matched, err := trust.matchesHash("sha256:other"); err != nil || matched {
		t.Fatalf("matchesHash(approved contents, changed) = %v, %v; want false, nil", matched, err)
	}

	// A different declaration never matches, even with the right contents.
	trust.TrustedHash = "sha256:other-declaration"
	if matched, err := trust.matchesHash(content); err != nil || matched {
		t.Fatalf("matchesHash(declaration mismatch) = %v, %v; want false, nil", matched, err)
	}

	// Without a user config layer there is no legacy baseline, so a
	// declaration-only approval cannot be verified (Rust returns false).
	noLayer := &pluginHookTrust{
		Root:            root,
		DeclarationHash: "sha256:declaration",
		TrustedHash:     "sha256:declaration",
	}
	if matched, err := noLayer.matchesHash(content); err != nil || matched {
		t.Fatalf("matchesHash(no legacy layer) = %v, %v; want false, nil", matched, err)
	}
}

func newPluginHookDiscovery(t *testing.T, home string, root string) *HookDiscoveryService {
	t.Helper()
	service := NewHookDiscoveryService(home)
	service.PluginHookSources = []plugin.HookSource{{
		PluginID:           "demo@marketplace",
		PluginRoot:         root,
		SourcePath:         filepath.Join(root, "hooks", "hooks.json"),
		SourceRelativePath: "hooks/hooks.json",
	}}
	return service
}

// TestPluginHookTrustBindsApprovalToPluginContentsLikeRust is the behavior probe
// for discovery.rs (#54619): a plugin approval is bound to the plugin contents,
// the reported current hash carries both approvals, and a bundle change returns
// the hook to "needs approval" instead of staying trusted.
func TestPluginHookTrustBindsApprovalToPluginContentsLikeRust(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	writePluginBundleFixture(t, root)
	content, err := pluginContentHash(root)
	if err != nil {
		t.Fatalf("pluginContentHash() error = %v", err)
	}
	service := newPluginHookDiscovery(t, home, root)
	cwd := t.TempDir()

	first := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(first.Data) != 1 || len(first.Data[0].Hooks) != 1 {
		t.Fatalf("Discover() = %+v, want one plugin hook", first.Data)
	}
	hook := first.Data[0].Hooks[0]
	declaration, reported, found := strings.Cut(hook.CurrentHash, "/")
	if !found {
		t.Fatalf("CurrentHash = %q, want declaration/content", hook.CurrentHash)
	}
	if declaration == "" || reported != content {
		t.Fatalf("CurrentHash = %q, want %q/%q", hook.CurrentHash, declaration, content)
	}
	if hook.TrustStatus != HookTrustUntrusted {
		t.Fatalf("TrustStatus = %q, want %q", hook.TrustStatus, HookTrustUntrusted)
	}

	// A declaration-only approval adopts the current bundle as the legacy
	// baseline, so the hook is trusted on first use.
	service.States = map[string]*HookState{hook.Key: {TrustedHash: &declaration}}
	approved := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(approved.Data) != 1 || len(approved.Data[0].Hooks) != 1 {
		t.Fatalf("Discover() = %+v, want one plugin hook", approved.Data)
	}
	approvedHook := approved.Data[0].Hooks[0]
	if approvedHook.TrustStatus != HookTrustTrusted {
		t.Fatalf("TrustStatus = %q, want %q (legacy approval adopts current contents)", approvedHook.TrustStatus, HookTrustTrusted)
	}
	if !hookTrustAllowsExecution(&approvedHook) {
		t.Fatal("hookTrustAllowsExecution() = false, want true for a trusted plugin hook")
	}
	baseline := pluginLegacyTrustPath(filepath.Join(home, "config.toml"), "demo@marketplace")
	if stored, err := os.ReadFile(baseline); err != nil || string(stored) != content {
		t.Fatalf("legacy baseline = %q, %v; want %q", string(stored), err, content)
	}

	// Changing the plugin contents invalidates the approval: the hook is
	// reported Modified and no longer executes.
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("tampered"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	changed := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if len(changed.Data) != 1 || len(changed.Data[0].Hooks) != 1 {
		t.Fatalf("Discover() = %+v, want one plugin hook", changed.Data)
	}
	changedHook := changed.Data[0].Hooks[0]
	if changedHook.TrustStatus != HookTrustModified {
		t.Fatalf("TrustStatus = %q, want %q after a plugin content change", changedHook.TrustStatus, HookTrustModified)
	}
	if hookTrustAllowsExecution(&changedHook) {
		t.Fatal("hookTrustAllowsExecution() = true, want false after a plugin content change")
	}

	// Recording the old contents as the approval still reports Modified...
	service.States = map[string]*HookState{hook.Key: {TrustedHash: &declaration, TrustedContentHash: &content}}
	stale := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if stale.Data[0].Hooks[0].TrustStatus != HookTrustModified {
		t.Fatalf("TrustStatus = %q, want %q for a stale content approval", stale.Data[0].Hooks[0].TrustStatus, HookTrustModified)
	}

	// ...and approving the new contents restores trust.
	changedContent, err := pluginContentHash(root)
	if err != nil {
		t.Fatalf("pluginContentHash() error = %v", err)
	}
	service.States = map[string]*HookState{hook.Key: {TrustedHash: &declaration, TrustedContentHash: &changedContent}}
	restored := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if restored.Data[0].Hooks[0].TrustStatus != HookTrustTrusted {
		t.Fatalf("TrustStatus = %q, want %q after approving the new contents", restored.Data[0].Hooks[0].TrustStatus, HookTrustTrusted)
	}

	// The approval a Go client persists is the combined declaration/content
	// value (tui/hooks_rpc.go BuildHookTrustValue writes hooks.state.trusted_hash
	// from hook.CurrentHash). Reading it back has to split it so the approve
	// flow keeps working while still binding the approval to the contents.
	combined := declaration + "/" + changedContent
	service.States = map[string]*HookState{hook.Key: {TrustedHash: &combined}}
	fromClient := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if fromClient.Data[0].Hooks[0].TrustStatus != HookTrustTrusted {
		t.Fatalf("TrustStatus = %q, want %q for a client-written combined approval", fromClient.Data[0].Hooks[0].TrustStatus, HookTrustTrusted)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("tampered again"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	afterClientChange := service.Discover(&HookListParams{CWDs: []string{cwd}}, "")
	if afterClientChange.Data[0].Hooks[0].TrustStatus != HookTrustModified {
		t.Fatalf("TrustStatus = %q, want %q after the approved contents changed", afterClientChange.Data[0].Hooks[0].TrustStatus, HookTrustModified)
	}
}

// TestPluginHookRunReverifiesPluginContentsLikeRust covers the execution half of
// #54619 (command_runner.rs/mcp_runner.rs verify): a bundle that changes between
// discovery and execution must not run.
func TestPluginHookRunReverifiesPluginContentsLikeRust(t *testing.T) {
	root := t.TempDir()
	writePluginBundleFixture(t, root)
	content, err := pluginContentHash(root)
	if err != nil {
		t.Fatalf("pluginContentHash() error = %v", err)
	}
	command := "echo plugin hook"
	metadata := HookMetadata{
		Key:           "plugin:demo",
		EventName:     HookEventUserPromptSubmit,
		HandlerType:   HookHandlerCommand,
		ExecutionMode: HookExecutionSync,
		Command:       &command,
		TimeoutSec:    30,
		Source:        HookSourcePlugin,
		PluginID:      stringPointer("demo@marketplace"),
		Enabled:       true,
		CurrentHash:   "sha256:declaration/" + content,
		TrustStatus:   HookTrustTrusted,
		pluginTrust: &pluginHookTrust{
			Root:               root,
			DeclarationHash:    "sha256:declaration",
			TrustedHash:        "sha256:declaration",
			TrustedContentHash: &content,
		},
	}
	request := &HookRunRequest{
		ThreadID:  "thread-1",
		CWD:       t.TempDir(),
		EventName: HookEventUserPromptSubmit,
		Hooks:     []HookMetadata{metadata},
	}
	runner := NewHookRunner()

	ok, err := runner.Run(nil, request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(ok.Runs) != 1 || ok.Runs[0].Status != HookRunCompleted {
		t.Fatalf("Run() = %+v, want one completed run", ok.Runs)
	}

	// The bundle changes after discovery: the approval no longer covers it.
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("tampered"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	blocked, err := runner.Run(nil, request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(blocked.Runs) != 1 {
		t.Fatalf("Run() = %+v, want one run", blocked.Runs)
	}
	if blocked.Runs[0].Status != HookRunFailed {
		t.Fatalf("Status = %q, want %q", blocked.Runs[0].Status, HookRunFailed)
	}
	texts := make([]string, 0, len(blocked.Runs[0].Entries))
	for _, entry := range blocked.Runs[0].Entries {
		texts = append(texts, entry.Text)
	}
	if !strings.Contains(strings.Join(texts, "\n"), "plugin contents changed; hook approval required") {
		t.Fatalf("Entries = %#v, want plugin contents changed; hook approval required", texts)
	}
}
