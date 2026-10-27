package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Mirrors Rust #57178: ChatGPT backend defaults resolve against the request's
// credentials, while any configuration layer above packaged defaults keeps its
// URL unchanged.
func TestChatGPTBackendURLResolvesCredentialDefaultsLikeRust(t *testing.T) {
	const fedrampKey = "sk-proj-fedramp-synthetic-test-key"
	for name, testCase := range map[string]struct {
		body string
		opts *LoadOptions
		want string
	}{
		"packaged default": {"", nil, FedRAMPChatGPTBaseURL},
		"explicit commercial default": {
			`chatgpt_base_url = "https://chatgpt.com/backend-api/"`,
			nil,
			"https://chatgpt.com/backend-api/",
		},
		"custom URL": {
			`chatgpt_base_url = "https://example.test/backend-api/"`,
			nil,
			"https://example.test/backend-api/",
		},
		"profile URL": {
			"[profiles.gov]\nchatgpt_base_url = \"https://profile.example.test/backend-api/\"\n",
			&LoadOptions{Profile: "gov"},
			"https://profile.example.test/backend-api/",
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if testCase.body != "" {
				writeConfig(t, home, testCase.body)
			}
			cfg, err := LoadWithOptions(home, testCase.opts)
			if err != nil {
				t.Fatalf("LoadWithOptions() error = %v", err)
			}
			backend := cfg.ChatGPTBackendURL()
			if got := backend.Resolve("api-key", fedrampKey); got != testCase.want {
				t.Fatalf("Resolve(fedramp api key) = %q, want %q", got, testCase.want)
			}
			if got := backend.Resolve("chatgpt", ""); got != cfg.ChatGPTBaseURL() {
				t.Fatalf("Resolve(chatgpt) = %q, want configured %q", got, cfg.ChatGPTBaseURL())
			}
		})
	}
}

func TestChatGPTBackendURLKeepsCommercialDefaultForUnmarkedKeysLikeRust(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	backend := cfg.ChatGPTBackendURL()
	for _, key := range []string{"sk-proj-synthetic-test-key", "", "sk-svcacct-synthetic-test-key"} {
		if got := backend.Resolve("api-key", key); got != DefaultChatGPTBaseURL {
			t.Fatalf("Resolve(%q) = %q, want %q", key, got, DefaultChatGPTBaseURL)
		}
	}
	if got := backend.Resolve("api-key", "sk-svcacct-fedramp-synthetic-test-key"); got != FedRAMPChatGPTBaseURL {
		t.Fatalf("service-account Resolve() = %q, want government host", got)
	}
}

// Managed requirements select the URL even when it equals the commercial
// default, so a FedRAMP key cannot silently reroute a managed endpoint.
func TestChatGPTBackendURLTreatsManagedRequirementAsExplicitLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(`chatgpt_base_url = "https://chatgpt.com/backend-api/"`), 0o600); err != nil {
		t.Fatalf("WriteFile requirements error = %v", err)
	}
	cfg, err := Load(home)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.ChatGPTBackendURL().Resolve("api-key", "sk-proj-fedramp-synthetic-test-key"); got != "https://chatgpt.com/backend-api/" {
		t.Fatalf("managed Resolve() = %q, want explicit commercial default", got)
	}
}

// The app-server reads a merged config plus origins; origin metadata is what
// distinguishes an explicit URL from the packaged default.
func TestNewConfigFromReadPreservesExplicitBaseURLLikeRust(t *testing.T) {
	const fedrampKey = "sk-proj-fedramp-synthetic-test-key"
	read := &ConfigReadResponse{
		Config:  map[string]any{"chatgpt_base_url": DefaultChatGPTBaseURL},
		Origins: map[string]LayerMetadata{"chatgpt_base_url": {Name: LayerSource{Type: LayerSourceUser}}},
	}
	if got := NewConfigFromRead(read).ChatGPTBackendURL().Resolve("api-key", fedrampKey); got != DefaultChatGPTBaseURL {
		t.Fatalf("explicit origin Resolve() = %q, want %q", got, DefaultChatGPTBaseURL)
	}
	read.Origins = map[string]LayerMetadata{}
	if got := NewConfigFromRead(read).ChatGPTBackendURL().Resolve("api-key", fedrampKey); got != FedRAMPChatGPTBaseURL {
		t.Fatalf("packaged default Resolve() = %q, want %q", got, FedRAMPChatGPTBaseURL)
	}
}
