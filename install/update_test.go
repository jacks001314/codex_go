package install

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

type recordingRunner struct {
	Command string
	Args    []string
	Err     error
}

func (r *recordingRunner) Run(ctx context.Context, command string, args []string) error {
	_ = ctx
	r.Command = command
	r.Args = append([]string(nil), args...)
	return r.Err
}

func TestActionFromContext(t *testing.T) {
	platform := StandaloneWindows
	tests := []struct {
		name    string
		context *InstallContext
		want    UpdateActionKind
		has     bool
	}{
		{name: "npm", context: &InstallContext{Method: InstallMethod{Kind: InstallNPM}}, want: UpdateActionNPMGlobalLatest, has: true},
		{name: "bun", context: &InstallContext{Method: InstallMethod{Kind: InstallBun}}, want: UpdateActionBunGlobalLatest, has: true},
		{name: "brew", context: &InstallContext{Method: InstallMethod{Kind: InstallBrew}}, want: UpdateActionBrewUpgrade, has: true},
		{name: "standalone-windows", context: &InstallContext{Method: InstallMethod{Kind: InstallStandalone, Platform: &platform}}, want: UpdateActionStandaloneWin, has: true},
		{name: "other", context: &InstallContext{Method: InstallMethod{Kind: InstallOther}}, has: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := ActionFromContext(test.context)
			if !test.has {
				if action != nil {
					t.Fatalf("ActionFromContext() = %#v, want nil", action)
				}
				return
			}
			if action == nil || action.Kind != test.want {
				t.Fatalf("ActionFromContext() = %#v, want %s", action, test.want)
			}
		})
	}
}

func TestUpdateActionCommandArgs(t *testing.T) {
	action := &UpdateAction{Kind: UpdateActionStandaloneUnix}
	command, args := action.CommandArgs()
	if command != "sh" || len(args) != 2 || args[1] != "curl -fsSL https://chatgpt.com/codex/install.sh | CODEX_NON_INTERACTIVE=1 sh" {
		t.Fatalf("CommandArgs() = %q %#v", command, args)
	}
	action = &UpdateAction{Kind: UpdateActionStandaloneWin}
	command, args = action.CommandArgs()
	if command != "powershell" || len(args) != 4 || args[3] != "$env:CODEX_NON_INTERACTIVE=1; irm https://chatgpt.com/codex/install.ps1 | iex" {
		t.Fatalf("CommandArgs() = %q %#v", command, args)
	}
}

func TestVersionParsingAndComparison(t *testing.T) {
	version, err := ExtractVersionFromLatestTag("rust-v1.5.0")
	if err != nil {
		t.Fatalf("ExtractVersionFromLatestTag() error = %v", err)
	}
	if version != "1.5.0" {
		t.Fatalf("version = %q", version)
	}
	if _, err := ExtractVersionFromLatestTag("v1.5.0"); err == nil {
		t.Fatal("latest tag without rust-v prefix returned nil error")
	}
	assertBoolPtr(t, IsNewerVersion("0.11.1", "0.11.0"), true)
	assertBoolPtr(t, IsNewerVersion("0.11.0", "0.11.1"), false)
	if got := IsNewerVersion("1.0.0-rc.1", "1.0.0"); got != nil {
		t.Fatalf("pre-release comparison = %v, want nil", *got)
	}
	if !IsSourceBuildVersion("0.0.0") || IsSourceBuildVersion("0.0.1") {
		t.Fatal("IsSourceBuildVersion mismatch")
	}
}

func TestFetchLatestVersionUsesHomebrewMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"2.3.4"}`))
	}))
	defer server.Close()

	latest, err := FetchLatestVersion(context.Background(), &UpdateCheckOptions{
		Context:     &InstallContext{Method: InstallMethod{Kind: InstallBrew}},
		HomebrewURL: server.URL,
	})
	if err != nil {
		t.Fatalf("FetchLatestVersion() error = %v", err)
	}
	if latest != "2.3.4" {
		t.Fatalf("latest = %q", latest)
	}
}

func TestFetchLatestVersionChecksNPMReadiness(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/github", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"rust-v1.2.3"}`))
	})
	mux.HandleFunc("/npm", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"dist-tags": {"latest": "1.2.3"},
			"versions": {
				"1.2.3": {"dist": {"tarball": "https://example.test/codex.tgz", "integrity": "sha512-test"}}
			}
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	latest, err := FetchLatestVersion(context.Background(), &UpdateCheckOptions{
		Context:        &InstallContext{Method: InstallMethod{Kind: InstallNPM}},
		GitHubURL:      server.URL + "/github",
		NPMRegistryURL: server.URL + "/npm",
	})
	if err != nil {
		t.Fatalf("FetchLatestVersion() error = %v", err)
	}
	if latest != "1.2.3" {
		t.Fatalf("latest = %q", latest)
	}
}

func TestCheckForUpdateBuildsResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"rust-v1.2.4"}`))
	}))
	defer server.Close()

	result, err := CheckForUpdate(context.Background(), &UpdateCheckOptions{
		Context:        &InstallContext{Method: InstallMethod{Kind: InstallOther}},
		CurrentVersion: "1.2.3",
		GitHubURL:      server.URL,
	})
	if err != nil {
		t.Fatalf("CheckForUpdate() error = %v", err)
	}
	if result.Status != UpdateStatusUpdateAvailable || result.LatestVersion != "1.2.4" || !result.CheckedOnline {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunUpdateUsesDetectedCommand(t *testing.T) {
	runner := &recordingRunner{}
	result, err := RunUpdate(context.Background(), &RunUpdateOptions{
		Context:       &InstallContext{Method: InstallMethod{Kind: InstallBun}},
		CommandRunner: runner,
	})
	if err != nil {
		t.Fatalf("RunUpdate() error = %v", err)
	}
	if result.Status != UpdateStatusUpdated {
		t.Fatalf("status = %s", result.Status)
	}
	if runner.Command != "bun" || len(runner.Args) != 3 || runner.Args[2] != "@openai/codex" {
		t.Fatalf("runner = %#v", runner)
	}
}

func TestExecCommandRunnerWindowsSpecialCaseDocumented(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows command wrapping only applies on Windows")
	}
	line := (&UpdateAction{Kind: UpdateActionNPMGlobalLatest}).CommandLine()
	if line == "" {
		t.Fatal("CommandLine() returned empty string")
	}
}

func assertBoolPtr(t *testing.T, got *bool, want bool) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("bool pointer = %v, want %v", got, want)
	}
}
