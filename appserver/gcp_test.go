package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

const gcpManagedConfigPathEnv = "CODEX_APP_SERVER_MANAGED_CONFIG_PATH"

// clearGCPTestEnv mirrors the env clearing in Rust's gcp_app_server helper.
// The ADC variable must be unset rather than empty because verify_credentials
// treats a present empty value as a relative credential path.
func clearGCPTestEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{model.GCPCredentialFileEnvVar, model.GCPProjectEnvVar, model.GCPLocationEnvVar} {
		name := name
		old, ok := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
		t.Cleanup(func() {
			if ok {
				_ = os.Setenv(name, old)
				return
			}
			_ = os.Unsetenv(name)
		})
	}
	clearAuthEnvAppserver(t)
}

func prependGCPTestPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeFakeGCloud(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "gcloud.cmd")
		body := "@echo off\r\nif exist fail exit /b 1\r\nsetlocal EnableDelayedExpansion\r\necho(!GOOGLE_APPLICATION_CREDENTIALS!>>credential-log\r\nendlocal\r\necho setup-token\r\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write fake gcloud: %v", err)
		}
		return path
	}
	path := filepath.Join(dir, "gcloud")
	body := "#!/bin/sh\nif [ -f fail ]; then exit 1; fi\nprintf '%s\\n' \"$GOOGLE_APPLICATION_CREDENTIALS\" >> credential-log\nprintf '%s\\n' setup-token\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake gcloud: %v", err)
	}
	return path
}

func writeGCPCredentialFile(t *testing.T, dir string, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(`{"type":"external_account"}`), 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	return path
}

func writeMutatingGCPAuthCommand(t *testing.T, dir string, configPath string) (string, []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "mutate.cmd")
		body := "@echo off\r\necho model = \"gpt-b\"> \"%~1\"\r\necho setup-token\r\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write mutating auth command: %v", err)
		}
		return path, []string{configPath}
	}
	path := filepath.Join(dir, "mutate.sh")
	body := "#!/bin/sh\nprintf 'model = \"gpt-b\"\\n' > \"$1\"\nprintf '%s\\n' setup-token\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write mutating auth command: %v", err)
	}
	return path, []string{configPath}
}

func tomlString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func newGCPTestRouter(t *testing.T, home string, startup string, models *model.ModelService) *RuntimeRouter {
	t.Helper()
	service := config.NewConfigService(home)
	if models == nil {
		models = model.NewModelService(nil)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Config:     service,
		Account:    auth.NewAccountManager(),
		Models:     models,
		DefaultCWD: startup,
	})
	t.Cleanup(func() { _ = router.Close() })
	return router
}

func writeGCPModelCatalog(t *testing.T, home string) (string, model.ModelsResponse) {
	t.Helper()
	catalog := model.ModelsResponse{Models: []model.ModelInfo{
		{Slug: "gpt-6.1-sol", DisplayName: "GPT-6.1 Sol", Visibility: model.VisibilityList, SupportedInAPI: true, Priority: 1},
		{Slug: "test-non-vertex-model", DisplayName: "Unsupported", Visibility: model.VisibilityList, SupportedInAPI: true, Priority: 2},
	}}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("marshal model catalog: %v", err)
	}
	path := filepath.Join(home, "models.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write model catalog: %v", err)
	}
	return path, catalog
}

func gcpConfigTable(t *testing.T, home string) map[string]any {
	t.Helper()
	values := readConfigValues(t, home)
	providers, ok := values["model_providers"].(map[string]any)
	if !ok {
		t.Fatalf("model_providers = %#v", values["model_providers"])
	}
	provider, ok := providers[model.GCPProviderID].(map[string]any)
	if !ok {
		t.Fatalf("model_providers.gcp = %#v", providers[model.GCPProviderID])
	}
	return provider
}

func gcpCredentialLogLines(t *testing.T, path string) []string {
	t.Helper()
	text := readFileString(t, path)
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func gcpSetupResponse(t *testing.T, router *RuntimeRouter, id int64, params map[string]any) *Response {
	t.Helper()
	return router.Handle(requestWithParams(t, IntID(id), MethodGcpSetup, params))
}

// Rust #57394, setup_gcp_saves_overrides_restores_adc_and_refreshes_catalog.
func TestGCPSetupSavesOverridesRestoresADCRefreshesCatalogLikeRust(t *testing.T) {
	clearGCPTestEnv(t)
	home := t.TempDir()
	startup := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	writeFakeGCloud(t, bin)
	prependGCPTestPath(t, bin)

	selected := writeGCPCredentialFile(t, home, "selected.json")
	ambient := writeGCPCredentialFile(t, home, "ambient.json")
	t.Setenv(model.GCPCredentialFileEnvVar, ambient)
	t.Setenv(model.GCPProjectEnvVar, "ambient-project")
	t.Setenv(model.GCPLocationEnvVar, "ambient-location")

	catalogPath, catalog := writeGCPModelCatalog(t, home)
	configBody := "model_catalog_json = " + tomlString(catalogPath) + "\n" +
		"[model_providers.gcp.http_headers]\nX-Existing = \"preserved\"\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	initialModels := model.NewModelService(model.NewStaticModelsManager(catalog))
	router := newGCPTestRouter(t, home, startup, initialModels)

	before := router.Handle(requestWithParams(t, IntID(1), MethodModelList, model.ModelListParams{}))
	if before.Error != nil {
		t.Fatalf("model/list before setup = %+v", before.Error)
	}
	if got := len(before.Result.(*model.ModelListResponse).Data); got != 2 {
		t.Fatalf("models before setup = %d, want 2", got)
	}

	response := gcpSetupResponse(t, router, 2, map[string]any{
		"projectId":      " selected-project ",
		"location":       " us-central1 ",
		"credentialFile": selected,
	})
	if response.Error != nil {
		t.Fatalf("account/gcp/setup = %+v", response.Error)
	}
	if _, ok := response.Result.(*GcpSetupResponse); !ok {
		t.Fatalf("result = %#v", response.Result)
	}

	values := readConfigValues(t, home)
	if values["model_provider"] != model.GCPProviderID {
		t.Fatalf("model_provider = %#v", values["model_provider"])
	}
	provider := gcpConfigTable(t, home)
	if provider["project_id"] != "selected-project" || provider["location"] != "us-central1" || provider["credential_file"] != selected {
		t.Fatalf("gcp provider = %#v", provider)
	}
	headers, _ := provider["http_headers"].(map[string]any)
	if headers["X-Existing"] != "preserved" {
		t.Fatalf("http_headers = %#v", provider["http_headers"])
	}
	if _, err := os.Stat(filepath.Join(startup, "credential-log")); err != nil {
		t.Fatalf("credential log in startup cwd: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "credential-log")); !os.IsNotExist(err) {
		t.Fatalf("credential log must not be written in CODEX_HOME, stat err = %v", err)
	}

	after := router.Handle(requestWithParams(t, IntID(3), MethodModelList, model.ModelListParams{}))
	if after.Error != nil {
		t.Fatalf("model/list after setup = %+v", after.Error)
	}
	afterModels := after.Result.(*model.ModelListResponse).Data
	if len(afterModels) != 1 || afterModels[0].Model != "gpt-6.1-sol" {
		t.Fatalf("models after setup = %+v, want only gpt-6.1-sol", afterModels)
	}

	response = gcpSetupResponse(t, router, 4, map[string]any{"projectId": "other-project"})
	if response.Error != nil {
		t.Fatalf("second account/gcp/setup = %+v", response.Error)
	}
	provider = gcpConfigTable(t, home)
	if provider["project_id"] != "other-project" || provider["location"] != model.GCPDefaultLocation {
		t.Fatalf("gcp provider after ADC setup = %#v", provider)
	}
	if _, ok := provider["credential_file"]; ok {
		t.Fatalf("credential_file should be cleared for ADC: %#v", provider)
	}
	if got := gcpCredentialLogLines(t, filepath.Join(startup, "credential-log")); len(got) != 2 || got[0] != selected || got[1] != ambient {
		t.Fatalf("credential log = %#v, want selected then ambient", got)
	}
}

// Rust #57394, setup_gcp_rejects_invalid_inputs_and_credentials_before_saving.
func TestGCPSetupRejectsInvalidInputsAndCredentialsBeforeSavingLikeRust(t *testing.T) {
	clearGCPTestEnv(t)
	home := t.TempDir()
	configPath := config.ConfigPath(home)
	if err := os.WriteFile(configPath, nil, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv(model.GCPProjectEnvVar, "ambient-project")
	t.Setenv(model.GCPLocationEnvVar, "ambient-location")
	emptyPath := t.TempDir()
	t.Setenv("PATH", emptyPath)
	router := newGCPTestRouter(t, home, home, nil)

	response := gcpSetupResponse(t, router, 1, map[string]any{"projectId": " "})
	if response.Error == nil || response.Error.Code != JSONRPCInvalidRequestErrorCode || response.Error.Data != nil {
		t.Fatalf("blank project response = %+v", response.Error)
	}

	response = gcpSetupResponse(t, router, 2, map[string]any{"projectId": "project"})
	if response.Error == nil || response.Error.Data["reason"] != gcpAuthReasonGcloudNotInstalled {
		t.Fatalf("missing gcloud response = %+v", response.Error)
	}

	invalid := filepath.Join(home, "invalid.json")
	if err := os.WriteFile(invalid, []byte(`["authorized_user"]`), 0o600); err != nil {
		t.Fatalf("write invalid credential: %v", err)
	}
	for index, credentialFile := range []string{invalid, filepath.Join(home, "missing.json")} {
		response = gcpSetupResponse(t, router, int64(3+index), map[string]any{"projectId": "project", "credentialFile": credentialFile})
		if response.Error == nil || response.Error.Data["reason"] != gcpAuthReasonCredentialFileInvalid {
			t.Fatalf("credential %s response = %+v", credentialFile, response.Error)
		}
	}

	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	writeFakeGCloud(t, bin)
	prependGCPTestPath(t, bin)
	if err := os.WriteFile(filepath.Join(home, "fail"), nil, 0o600); err != nil {
		t.Fatalf("write fail marker: %v", err)
	}
	response = gcpSetupResponse(t, router, 5, map[string]any{"projectId": "project"})
	if response.Error == nil || response.Error.Data["reason"] != gcpAuthReasonAuthCommandFailed {
		t.Fatalf("failed auth response = %+v", response.Error)
	}
	if got := readFileString(t, configPath); got != "" {
		t.Fatalf("config after rejected setup = %q", got)
	}
}

// Rust #57394, setup_gcp_rejects_policy_and_endpoint_conflicts_before_saving.
func TestGCPSetupRejectsPolicyAndEndpointConflictsBeforeSavingLikeRust(t *testing.T) {
	cases := []struct {
		name               string
		configBody         string
		requirementsBody   string
		managedBody        string
		wantMessage        string
		wantWriteErrorCode string
	}{
		{
			name:        "forced chatgpt login",
			configBody:  "forced_login_method = \"chatgpt\"\n",
			wantMessage: "Google Cloud setup is disabled. Use ChatGPT login instead.",
		},
		{
			name:               "managed provider override",
			managedBody:        "[model_providers.gcp]\nproject_id = \"managed-project\"\n",
			wantMessage:        "Cannot configure `model_providers.gcp.project_id`: Overridden by legacy managed_config.toml",
			wantWriteErrorCode: string(config.ConfigWriteLayerReadonly),
		},
		{
			name:        "custom base url",
			configBody:  "[model_providers.gcp]\nbase_url = \"https://vertex.example.com/v1\"\n",
			wantMessage: "Google Cloud has a custom `base_url`",
		},
		{
			name:               "required provider selection",
			requirementsBody:   "model_provider = \"openai\"\n",
			wantMessage:        "requirements set `model_provider` to `openai`",
			wantWriteErrorCode: string(config.ConfigWriteRequirementReadonly),
		},
		{
			name:               "required provider definition",
			requirementsBody:   "[model_providers.gcp]\nproject_id = \"managed-project\"\n",
			wantMessage:        "model_providers.gcp",
			wantWriteErrorCode: string(config.ConfigWriteRequirementReadonly),
		},
		{
			name:             "application network restriction",
			requirementsBody: "[application.network.domains]\n\"oauth2.googleapis.com\" = \"allow\"\n",
			wantMessage:      "disabled by application network restrictions",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			clearGCPTestEnv(t)
			home := t.TempDir()
			bin := filepath.Join(home, "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatalf("mkdir bin: %v", err)
			}
			writeFakeGCloud(t, bin)
			prependGCPTestPath(t, bin)
			configPath := config.ConfigPath(home)
			if err := os.WriteFile(configPath, []byte(testCase.configBody), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if testCase.managedBody != "" {
				managedPath := filepath.Join(home, "managed_config.toml")
				if err := os.WriteFile(managedPath, []byte(testCase.managedBody), 0o600); err != nil {
					t.Fatalf("write managed config: %v", err)
				}
				t.Setenv(gcpManagedConfigPathEnv, managedPath)
			}
			if testCase.requirementsBody != "" {
				if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(testCase.requirementsBody), 0o600); err != nil {
					t.Fatalf("write requirements: %v", err)
				}
			}
			router := newGCPTestRouter(t, home, home, nil)
			response := gcpSetupResponse(t, router, 1, map[string]any{
				"projectId":      "project",
				"credentialFile": filepath.Join(home, "missing.json"),
			})
			if response.Error == nil || response.Error.Code != JSONRPCInvalidRequestErrorCode {
				t.Fatalf("setup response = %+v", response.Error)
			}
			if !strings.Contains(response.Error.Message, testCase.wantMessage) {
				t.Fatalf("message = %q, want substring %q", response.Error.Message, testCase.wantMessage)
			}
			if testCase.wantWriteErrorCode != "" {
				if got := response.Error.Data["config_write_error_code"]; got != testCase.wantWriteErrorCode {
					t.Fatalf("error data = %#v, want config_write_error_code=%s", response.Error.Data, testCase.wantWriteErrorCode)
				}
			}
			if got := readFileString(t, configPath); got != testCase.configBody {
				t.Fatalf("config changed: %q, want %q", got, testCase.configBody)
			}
			if _, err := os.Stat(filepath.Join(home, "credential-log")); !os.IsNotExist(err) {
				t.Fatalf("credential command must not run, stat err = %v", err)
			}
		})
	}
}

// Rust #57394: the default gcloud command runs relative to the app-server's
// startup cwd, never CODEX_HOME.
func TestGCPSetupRunsDefaultAuthInStartupCWDLikeRust(t *testing.T) {
	clearGCPTestEnv(t)
	home := t.TempDir()
	startup := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	writeFakeGCloud(t, bin)
	prependGCPTestPath(t, bin)
	if err := os.WriteFile(config.ConfigPath(home), nil, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	router := newGCPTestRouter(t, home, startup, nil)
	response := gcpSetupResponse(t, router, 1, map[string]any{"projectId": "project"})
	if response.Error != nil {
		t.Fatalf("account/gcp/setup = %+v", response.Error)
	}
	if _, err := os.Stat(filepath.Join(startup, "credential-log")); err != nil {
		t.Fatalf("startup credential log: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "credential-log")); !os.IsNotExist(err) {
		t.Fatalf("CODEX_HOME credential log must not exist, stat err = %v", err)
	}
}

// Rust #57394: production routers use the configured app-server startup cwd
// (--cd or the process cwd) for the default gcloud command, not CODEX_HOME.
func TestGCPSetupUsesConfiguredStartupCWDLikeRust(t *testing.T) {
	clearGCPTestEnv(t)
	home := t.TempDir()
	startup := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	writeFakeGCloud(t, bin)
	prependGCPTestPath(t, bin)
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(filepath.Join(home, "sessions")), home, &RuntimeRouterOptions{StartupCWD: startup})
	t.Cleanup(func() { _ = router.Close() })
	response := gcpSetupResponse(t, router, 1, map[string]any{"projectId": "project"})
	if response.Error != nil {
		t.Fatalf("account/gcp/setup = %+v", response.Error)
	}
	if _, err := os.Stat(filepath.Join(startup, "credential-log")); err != nil {
		t.Fatalf("startup credential log: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "credential-log")); !os.IsNotExist(err) {
		t.Fatalf("CODEX_HOME credential log must not exist, stat err = %v", err)
	}
}

// Rust #57394: an app-server without a --cd override records the process cwd
// as its startup cwd instead of CODEX_HOME.
func TestGCPSetupStartupCWDDefaultsToProcessCWDLikeRust(t *testing.T) {
	home := t.TempDir()
	router := NewDefaultRuntimeRouterWithOptions(nil, home, nil)
	t.Cleanup(func() { _ = router.Close() })
	if got := router.services.StartupCWD; got == "" || got == home {
		t.Fatalf("StartupCWD = %q, want the process cwd %q rather than CODEX_HOME %q", got, processCWD(), home)
	}
}

// Rust #57394: a config write that races the credential command is rejected
// with configVersionConflict instead of overwriting the newer user config.
func TestGCPSetupRejectsConfigVersionConflictLikeRust(t *testing.T) {
	clearGCPTestEnv(t)
	home := t.TempDir()
	startup := t.TempDir()
	configPath := config.ConfigPath(home)
	script, args := writeMutatingGCPAuthCommand(t, startup, configPath)
	authTable := "[model_providers.gcp.auth]\n" +
		"command = " + tomlString(script) + "\n" +
		"args = [" + tomlString(args[0]) + "]\n" +
		"timeout_ms = 30000\n" +
		"cwd = " + tomlString(startup) + "\n"
	initial := "model = \"gpt-a\"\n" + authTable
	if err := os.WriteFile(configPath, []byte(initial), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	router := newGCPTestRouter(t, home, startup, nil)
	response := gcpSetupResponse(t, router, 1, map[string]any{"projectId": "project"})
	if response.Error == nil || response.Error.Code != JSONRPCInvalidRequestErrorCode {
		t.Fatalf("setup response = %+v", response.Error)
	}
	if got := response.Error.Data["config_write_error_code"]; got != string(config.ConfigWriteVersionConflict) {
		t.Fatalf("error data = %#v, want version conflict", response.Error.Data)
	}
	if got := readFileString(t, configPath); !strings.Contains(got, "gpt-b") {
		t.Fatalf("config = %q, want the concurrent write preserved", got)
	}
}

// Rust #57394: revoking the application network permit cancels the in-flight
// credential command and leaves the configuration untouched.
func TestGCPSetupStopsCredentialCommandWhenNetworkPermissionRevokedLikeRust(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell for the long-running auth command")
	}
	clearGCPTestEnv(t)
	home := t.TempDir()
	startup := t.TempDir()
	configPath := config.ConfigPath(home)
	initial := "model_providers.gcp.auth.command = \"sh\"\n" +
		"model_providers.gcp.auth.args = [\"-c\", " + tomlString("echo $$ > refresh.pid; exec sleep 120") + "]\n" +
		"model_providers.gcp.auth.cwd = " + tomlString(startup) + "\n"
	if err := os.WriteFile(configPath, []byte(initial), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	router := newGCPTestRouter(t, home, startup, nil)
	responses := make(chan *Response, 1)
	request := requestWithParams(t, IntID(1), MethodGcpSetup, map[string]any{"projectId": "project"})
	go func() {
		responses <- router.Handle(request)
	}()

	pidPath := filepath.Join(startup, "refresh.pid")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(pidPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("credential command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("[application.network]\n"), 0o600); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	// Rust's test triggers the reload through config/read, which republishes
	// the application network policy and revokes outstanding permits.
	if read := router.Handle(requestWithParams(t, IntID(2), MethodConfigRead, map[string]any{})); read.Error != nil {
		t.Fatalf("config/read = %+v", read.Error)
	}

	select {
	case response := <-responses:
		if response.Error == nil || response.Error.Message != "application network permission was revoked" {
			t.Fatalf("setup response = %+v", response.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("setup did not stop after network permission revocation")
	}
	if got := readFileString(t, configPath); got != initial {
		t.Fatalf("config changed after revocation: %q", got)
	}
}

// Rust #57394, account/read reports a GCP account without requiring OpenAI auth.
func TestGCPAccountReadLikeRust(t *testing.T) {
	clearGCPTestEnv(t)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_provider = \"gcp\"\n[model_providers.gcp]\nproject_id = \"project\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	router := newGCPTestRouter(t, home, home, nil)
	response := router.Handle(requestWithParams(t, IntID(1), MethodGetAccount, auth.GetAccountParams{}))
	if response.Error != nil {
		t.Fatalf("account/read = %+v", response.Error)
	}
	encoded, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatalf("marshal account/read: %v", err)
	}
	want := `{"account":{"type":"gcp"},"requiresOpenaiAuth":false,"workspaceRouting":null}`
	if string(encoded) != want {
		t.Fatalf("account/read = %s, want %s", encoded, want)
	}
}

// Rust #57394 registers account/gcp/setup as an experimental client request only.
func TestGCPMethodsRequireExperimentalAPILikeRust(t *testing.T) {
	if !experimentalAPIMethod(MethodGcpSetup) {
		t.Fatal("account/gcp/setup must be experimental")
	}
	for _, entry := range BuildProtocolSchema(false, false).ClientRequests {
		if Method(entry.Method) == MethodGcpSetup {
			t.Fatal("account/gcp/setup must not be part of the stable client request surface")
		}
	}
	found := false
	for _, entry := range BuildProtocolSchema(true, false).ClientRequests {
		if Method(entry.Method) != MethodGcpSetup {
			continue
		}
		found = true
		if entry.Params != "GcpSetupParams" || entry.Result != "GcpSetupResponse" {
			t.Fatalf("account/gcp/setup signature = %#v", entry)
		}
	}
	if !found {
		t.Fatal("experimental client requests missing account/gcp/setup")
	}
}
