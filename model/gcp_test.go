package model

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func gcpStringPtr(value string) *string { return &value }

func TestGCPBuiltInProviderLikeRust(t *testing.T) {
	providers := BuiltInProviders("")
	provider, ok := providers[GCPProviderID]
	if !ok {
		t.Fatalf("BuiltInProviders() missing %q", GCPProviderID)
	}
	if provider.Name != "Google Cloud Vertex AI" {
		t.Fatalf("GCP provider name = %q, want %q", provider.Name, "Google Cloud Vertex AI")
	}
	if !(&provider).IsGCP() {
		t.Fatal("built-in GCP provider should report IsGCP()")
	}
	if provider.RequiresOpenAIAuth || provider.SupportsWebsockets {
		t.Fatalf("GCP provider auth flags = %+v", provider)
	}
}

func TestGCPRuntimeBaseURLLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		project     *string
		location    *string
		envProject  string
		envLocation string
		want        string
		wantErr     string
	}{
		{
			name:       "defaults_to_global_location",
			envProject: "example-project",
			want:       "https://aiplatform.googleapis.com/v1/projects/example-project/locations/global/publishers/openai/v1",
		},
		{
			name:        "uses_explicit_location",
			envProject:  "example-project",
			envLocation: "us-central1",
			want:        "https://aiplatform.googleapis.com/v1/projects/example-project/locations/us-central1/publishers/openai/v1",
		},
		{
			name:        "configured_fields_win_over_environment",
			project:     gcpStringPtr("configured-project"),
			location:    gcpStringPtr("us-east4"),
			envProject:  "environment-project",
			envLocation: "us-central1",
			want:        "https://aiplatform.googleapis.com/v1/projects/configured-project/locations/us-east4/publishers/openai/v1",
		},
		{
			name:        "empty_configured_project_does_not_fall_back",
			project:     gcpStringPtr(""),
			envProject:  "environment-project",
			envLocation: "us-central1",
			wantErr:     "model_providers.gcp.project_id must be non-empty and contain only ASCII letters, digits, and hyphens",
		},
		{
			name:    "missing_project_explains_environment_variable",
			wantErr: "Missing environment variable: `GOOGLE_CLOUD_PROJECT`. Set model_providers.gcp.project_id or GOOGLE_CLOUD_PROJECT to your Google Cloud project ID, or configure model_providers.gcp.base_url.",
		},
		{
			name:       "rejects_path_components",
			envProject: "example-project/other",
			wantErr:    "GOOGLE_CLOUD_PROJECT must be non-empty and contain only ASCII letters, digits, and hyphens",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := &ProviderInfo{ProjectID: tc.project, Location: tc.location}
			got, err := GCPRuntimeBaseURL(info, tc.envProject, tc.envLocation)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("GCPRuntimeBaseURL() error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("GCPRuntimeBaseURL() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("GCPRuntimeBaseURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGCPConfiguredBaseURLBypassesProjectDiscoveryLikeRust(t *testing.T) {
	t.Setenv(GCPProjectEnvVar, "")
	t.Setenv(GCPLocationEnvVar, "")
	provider := CreateGCPProvider()
	provider.BaseURL = "https://vertex.example.com/v1"
	provider.Auth = &ProviderAuthInfo{Command: "print-token", TimeoutMS: 5000}
	// An explicit base URL must not require project/location discovery.
	got, err := (&GCPProvider{info: provider}).RuntimeBaseURL()
	if err != nil {
		t.Fatalf("RuntimeBaseURL() error = %v", err)
	}
	if got != "https://vertex.example.com/v1" {
		t.Fatalf("RuntimeBaseURL() = %q", got)
	}
}

func TestMergeGCPProviderOverridesLikeRust(t *testing.T) {
	builtIn := CreateGCPProvider()
	override := ProviderInfo{
		BaseURL:        "https://vertex.example.com/v1",
		Auth:           &ProviderAuthInfo{Command: "print-token"},
		HTTPHeaders:    map[string]string{"X-Existing": "preserved"},
		ProjectID:      gcpStringPtr("selected-project"),
		Location:       gcpStringPtr("us-central1"),
		CredentialFile: gcpStringPtr(filepath.Join(t.TempDir(), "adc.json")),
	}
	if err := MergeGCPProviderOverrides(&builtIn, override); err != nil {
		t.Fatalf("MergeGCPProviderOverrides() error = %v", err)
	}
	if builtIn.ProjectID == nil || *builtIn.ProjectID != "selected-project" {
		t.Fatalf("project id = %v", builtIn.ProjectID)
	}
	if builtIn.Location == nil || *builtIn.Location != "us-central1" {
		t.Fatalf("location = %v", builtIn.Location)
	}
	if builtIn.CredentialFile == nil || *builtIn.CredentialFile != *override.CredentialFile {
		t.Fatalf("credential file = %v", builtIn.CredentialFile)
	}
	if builtIn.BaseURL != override.BaseURL {
		t.Fatalf("base url = %q", builtIn.BaseURL)
	}
	if builtIn.HTTPHeaders["X-Existing"] != "preserved" {
		t.Fatalf("http headers = %v", builtIn.HTTPHeaders)
	}
	if builtIn.Auth == nil || builtIn.Auth.Command != "print-token" {
		t.Fatalf("auth = %#v", builtIn.Auth)
	}

	for _, relative := range []string{"", "adc.json", "../adc.json"} {
		bad := ProviderInfo{CredentialFile: gcpStringPtr(relative)}
		err := MergeGCPProviderOverrides(nil, bad)
		if err == nil || !strings.Contains(err.Error(), "model_providers.gcp provider credential_file must be an absolute path") {
			t.Fatalf("relative credential %q error = %v", relative, err)
		}
	}

	forbidden := ProviderInfo{Name: "Custom"}
	err := MergeGCPProviderOverrides(nil, forbidden)
	if err == nil || !strings.Contains(err.Error(), "only supports changing") {
		t.Fatalf("forbidden field error = %v", err)
	}
}

func TestGCPConfigOverridePreservesExplicitEmptyValuesLikeRust(t *testing.T) {
	// ConfigToml deserialization keeps Some("") so the GCP runtime rejects it
	// instead of falling back to GOOGLE_CLOUD_PROJECT.
	values := map[string]any{
		"model_providers": map[string]any{
			"gcp": map[string]any{"project_id": ""},
		},
	}
	providers, err := ProvidersFromConfig(values, "")
	if err != nil {
		t.Fatalf("ProvidersFromConfig() error = %v", err)
	}
	gcp := providers[GCPProviderID]
	if gcp.ProjectID == nil || *gcp.ProjectID != "" {
		t.Fatalf("explicit empty project id was lost: %v", gcp.ProjectID)
	}
	if _, err := GCPRuntimeBaseURL(&gcp, "environment-project", ""); err == nil ||
		!strings.Contains(err.Error(), "model_providers.gcp.project_id must be non-empty") {
		t.Fatalf("GCPRuntimeBaseURL() error = %v, want explicit-config rejection", err)
	}
}

func TestGCPProviderDefaultAuthBoundToStartupCWDLikeRust(t *testing.T) {
	startup := t.TempDir()
	providers, err := ProvidersFromConfigForCWD(map[string]any{}, "", startup)
	if err != nil {
		t.Fatalf("ProvidersFromConfigForCWD() error = %v", err)
	}
	authInfo := providers[GCPProviderID].Auth
	if authInfo == nil {
		t.Fatal("GCP default auth was not installed")
	}
	wantCommand := "gcloud"
	if runtime.GOOS == "windows" {
		wantCommand = "gcloud.cmd"
	}
	if authInfo.Command != wantCommand {
		t.Fatalf("command = %q, want %q", authInfo.Command, wantCommand)
	}
	if strings.Join(authInfo.Args, " ") != "auth application-default print-access-token" {
		t.Fatalf("args = %v", authInfo.Args)
	}
	if authInfo.TimeoutMS != 30000 || authInfo.RefreshIntervalMS != 3000000 {
		t.Fatalf("timeouts = %d/%d", authInfo.TimeoutMS, authInfo.RefreshIntervalMS)
	}
	if authInfo.CWD != filepath.Clean(startup) {
		t.Fatalf("cwd = %q, want %q", authInfo.CWD, startup)
	}
}

func TestGCPProviderAccountStateLikeRust(t *testing.T) {
	state, err := (&GCPProvider{info: CreateGCPProvider()}).AccountState()
	if err != nil {
		t.Fatalf("AccountState() error = %v", err)
	}
	if state.RequiresOpenAIAuth {
		t.Fatal("GCP must not require OpenAI auth")
	}
	if state.Account == nil || state.Account.Type != "gcp" {
		t.Fatalf("account = %#v, want type gcp", state.Account)
	}
}

func TestGCPModelsManagerFiltersAndNormalizesLikeRust(t *testing.T) {
	catalog := ModelsResponse{Models: []ModelInfo{
		{Slug: "gpt-6-sol", UseResponsesLite: true, ToolMode: ToolModeCodeModeOnly, ApplyPatchToolType: "freeform"},
		{Slug: "gpt-6.1-sol", UseResponsesLite: true, ToolMode: ToolModeCodeModeOnly, ApplyPatchToolType: "freeform"},
		{Slug: "gpt-6.1-sol-extra"},
		{Slug: "gpt-6.1-terra", UseResponsesLite: true, ToolMode: ToolModeCodeModeOnly, ApplyPatchToolType: "freeform"},
		{Slug: "gpt-6-luna", UseResponsesLite: true, ToolMode: ToolModeCodeModeOnly, ApplyPatchToolType: "freeform"},
		{Slug: "gpt-6-astra", UseResponsesLite: true, ToolMode: ToolModeCodeModeOnly, ApplyPatchToolType: "freeform"},
	}}
	manager := (&GCPProvider{info: CreateGCPProvider()}).ModelsManager(&catalog)
	got := manager.GetRemoteModels()
	want := []string{"gpt-6.1-sol", "gpt-6.1-terra", "gpt-6-luna", "gpt-6-astra"}
	if len(got) != len(want) {
		t.Fatalf("models = %v, want %v", got, want)
	}
	for i, item := range got {
		if item.Slug != want[i] {
			t.Fatalf("models[%d] = %q, want %q", i, item.Slug, want[i])
		}
		if item.UseResponsesLite {
			t.Fatalf("%s kept use_responses_lite", item.Slug)
		}
		if item.ToolMode != ToolModeDirect {
			t.Fatalf("%s tool mode = %q, want direct", item.Slug, item.ToolMode)
		}
		if item.ApplyPatchToolType != "" {
			t.Fatalf("%s apply_patch_tool_type = %q", item.Slug, item.ApplyPatchToolType)
		}
	}
}

func TestGCPProviderResolveProviderAuthInjectsCredentialFileOnlyForCommandLikeRust(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell command fixture")
	}
	startup := t.TempDir()
	credentialFile := filepath.Join(startup, "credentials with space.json")
	provider := CreateGCPProvider()
	provider.Auth = &ProviderAuthInfo{
		Command: "sh",
		Args:    []string{"-c", `printf '%s' "$GOOGLE_APPLICATION_CREDENTIALS"`},
		CWD:     startup,
	}
	provider.CredentialFile = &credentialFile
	t.Setenv(GCPCredentialFileEnvVar, "")
	headers, err := ResolveProviderAuth(nil, provider)
	if err != nil {
		t.Fatalf("ResolveProviderAuth() error = %v", err)
	}
	if got := headers.Headers.Get("Authorization"); got != "Bearer "+credentialFile {
		t.Fatalf("authorization = %q, want credential file path", got)
	}
	if os.Getenv(GCPCredentialFileEnvVar) != "" {
		t.Fatalf("process %s was modified", GCPCredentialFileEnvVar)
	}
}

func TestGCPProviderMissingCredentialsExplainReauthenticationLikeRust(t *testing.T) {
	provider := CreateGCPProvider()
	if runtime.GOOS == "windows" {
		provider.Auth = &ProviderAuthInfo{Command: "cmd", Args: []string{"/d", "/c", "echo."}, TimeoutMS: 5000}
	} else {
		provider.Auth = &ProviderAuthInfo{Command: "sh", Args: []string{"-c", "printf ''"}, TimeoutMS: 5000}
	}
	_, err := (&GCPProvider{info: provider}).APIAuth()
	want := "Google Cloud credentials are unavailable or expired. Run `gcloud auth application-default login` to reauthenticate"
	if err == nil || err.Error() != want {
		t.Fatalf("APIAuth() error = %v, want %q", err, want)
	}
}

func TestGCPRequiredModelProviderDefinitionMergesBuiltInLikeRust(t *testing.T) {
	provider, err := RequiredModelProviderDefinition(GCPProviderID, map[string]any{"project_id": "configured-project"})
	if err != nil {
		t.Fatalf("RequiredModelProviderDefinition() error = %v", err)
	}
	if provider == nil || provider.Name != GCPProviderName {
		t.Fatalf("provider = %#v, want merged built-in GCP provider", provider)
	}
	if provider.ProjectID == nil || *provider.ProjectID != "configured-project" {
		t.Fatalf("project id = %v", provider.ProjectID)
	}
}

func TestGCPReservedProviderIDAllowsOverrideLikeRust(t *testing.T) {
	values := map[string]any{
		"model_providers": map[string]any{
			"gcp": map[string]any{"project_id": "configured-project"},
		},
	}
	providers, err := ProvidersFromConfig(values, "")
	if err != nil {
		t.Fatalf("ProvidersFromConfig() rejected GCP override: %v", err)
	}
	provider := providers[GCPProviderID]
	if provider.Name != GCPProviderName {
		t.Fatalf("merged provider name = %q", provider.Name)
	}
	if provider.ProjectID == nil || *provider.ProjectID != "configured-project" {
		t.Fatalf("project id = %v", provider.ProjectID)
	}

	relative := map[string]any{
		"model_providers": map[string]any{
			"gcp": map[string]any{"credential_file": "adc.json"},
		},
	}
	if _, err := ProvidersFromConfig(relative, ""); err == nil ||
		!strings.Contains(err.Error(), "model_providers.gcp: provider credential_file must be an absolute path") {
		t.Fatalf("relative GCP credential error = %v", err)
	}
}
