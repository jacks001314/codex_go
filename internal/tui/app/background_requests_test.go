package app

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/internal/mcp"
	"codex_go/internal/plugin"
)

func TestBackgroundRequestTimeoutConstantsMatchRust(t *testing.T) {
	if TokenActivityFetchTimeout != 15*time.Second {
		t.Fatalf("TokenActivityFetchTimeout = %s", TokenActivityFetchTimeout)
	}
	if RateLimitResetRequestTimeout != 15*time.Second {
		t.Fatalf("RateLimitResetRequestTimeout = %s", RateLimitResetRequestTimeout)
	}
	if WorkspaceHeadlineFetchTimeout != 2*time.Second {
		t.Fatalf("WorkspaceHeadlineFetchTimeout = %s", WorkspaceHeadlineFetchTimeout)
	}
}

func TestBackgroundRequestRegistryTracksActiveRequests(t *testing.T) {
	registry := NewBackgroundRequestRegistry()
	if !registry.Start("plugins") {
		t.Fatal("first Start() = false, want true")
	}
	if registry.Start("plugins") {
		t.Fatal("duplicate Start() = true, want false")
	}
	if !registry.IsActive("plugins") || registry.ActiveCount() != 1 {
		t.Fatalf("active registry = %v/%d", registry.IsActive("plugins"), registry.ActiveCount())
	}
	if !registry.Finish("plugins") {
		t.Fatal("Finish() = false, want true")
	}
	if registry.Finish("plugins") || registry.IsActive("plugins") || registry.ActiveCount() != 0 {
		t.Fatalf("registry after finish active=%v count=%d", registry.IsActive("plugins"), registry.ActiveCount())
	}
}

func TestMCPInventoryRequestThreadIDMatchRust(t *testing.T) {
	nav := NewAgentNavigationState()
	nav.Upsert("thread-1", "", "", false)

	if got, ok := MCPInventoryRequestThreadID("thread-1", "thread-1", nav); !ok || got != "thread-1" {
		t.Fatalf("active open thread id = %q/%v", got, ok)
	}
	if got, ok := MCPInventoryRequestThreadID("thread-1", "thread-2", nav); ok || got != "" {
		t.Fatalf("inactive thread id = %q/%v, want none", got, ok)
	}
	nav.MarkClosed("thread-1")
	if got, ok := MCPInventoryRequestThreadID("thread-1", "thread-1", nav); ok || got != "" {
		t.Fatalf("closed thread id = %q/%v, want none", got, ok)
	}
	if got, ok := MCPInventoryRequestThreadID("thread-1", "thread-1", nil); !ok || got != "thread-1" {
		t.Fatalf("nil nav thread id = %q/%v", got, ok)
	}
}

func TestPluginRemoteSectionErrorMessagesMatchRust(t *testing.T) {
	cases := []struct {
		label string
		err   string
		want  string
	}{
		{
			label: "OpenAI Curated",
			err:   "api key auth is not supported",
			want:  "Sign in with ChatGPT auth; API key auth cannot load remote plugin catalogs.",
		},
		{
			label: "OpenAI Curated",
			err:   "authentication required",
			want:  "Sign in to ChatGPT, then try loading this section again.",
		},
		{
			label: "Workspace",
			err:   "workspace access mismatch",
			want:  "Switch to the matching workspace or ask the sharer for access.",
		},
		{
			label: "OpenAI Curated",
			err:   "status 503",
			want:  "Try again later; local plugin functionality is still available.",
		},
		{
			label: "Shared with me",
			err:   "plugin disabled",
			want:  "Ask the sharer or a workspace admin to confirm plugin access.",
		},
	}
	for _, tc := range cases {
		got := PluginRemoteSectionErrorMessage(tc.label, tc.err)
		if !strings.Contains(got, tc.want) {
			t.Fatalf("PluginRemoteSectionErrorMessage(%q, %q) = %q, want suffix %q", tc.label, tc.err, got, tc.want)
		}
	}

	if got := PluginRemoteSectionErrorMessage("Workspace", "plain failure"); got != "plain failure" {
		t.Fatalf("PluginRemoteSectionErrorMessage(no next step) = %q", got)
	}
	disabled := PluginSharingDisabledRemoteSectionError()
	if disabled.SectionID != "shared-with-me" || disabled.Label != "Shared with me" || !strings.Contains(disabled.Message, "Plugin sharing is disabled") {
		t.Fatalf("PluginSharingDisabledRemoteSectionError() = %#v", disabled)
	}
}

func TestHideCLIOnlyPluginMarketplacesMatchRust(t *testing.T) {
	response := &plugin.PluginListResponse{
		Marketplaces: []plugin.PluginMarketplaceEntry{
			{Name: "local"},
			{Name: "openai-bundled"},
			{Name: "workspace"},
		},
	}
	HideCLIOnlyPluginMarketplaces(response)
	var names []string
	for _, marketplace := range response.Marketplaces {
		names = append(names, marketplace.Name)
	}
	if !reflect.DeepEqual(names, []string{"local", "workspace"}) {
		t.Fatalf("marketplaces = %v, want local/workspace", names)
	}
}

func TestMarketplaceAddSourceForRequestResolvesRelativeLikeRust(t *testing.T) {
	cwd := t.TempDir()
	cases := []struct {
		source string
		want   string
	}{
		{source: ".", want: cwd},
		{source: "./plugins#main", want: filepath.Join(cwd, "plugins") + "#main"},
		{source: "../plugins@dev", want: filepath.Join(filepath.Dir(cwd), "plugins") + "@dev"},
		{source: ".\\plugins", want: filepath.Join(cwd, "plugins")},
		{source: "owner/repo#main", want: "owner/repo#main"},
		{source: "https://github.com/owner/repo.git@main", want: "https://github.com/owner/repo.git@main"},
	}
	for _, tc := range cases {
		if got := MarketplaceAddSourceForRequest(cwd, tc.source); got != tc.want {
			t.Fatalf("MarketplaceAddSourceForRequest(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}
}

func TestBuildFeedbackUploadParamsMatchRust(t *testing.T) {
	reason := "broken"
	params := BuildFeedbackUploadParams("thread-1", "rollout.jsonl", "bug", &reason, "turn-1", true)
	if params.Classification != "bug" || params.Reason == nil || *params.Reason != "broken" {
		t.Fatalf("feedback core params = %#v", params)
	}
	if params.ThreadID == nil || *params.ThreadID != "thread-1" {
		t.Fatalf("thread id = %#v", params.ThreadID)
	}
	if !reflect.DeepEqual(params.ExtraLogFiles, []string{"rollout.jsonl"}) {
		t.Fatalf("extra logs = %#v", params.ExtraLogFiles)
	}
	if !reflect.DeepEqual(params.Tags, map[string]string{"turn_id": "turn-1"}) {
		t.Fatalf("tags = %#v", params.Tags)
	}

	withoutLogs := BuildFeedbackUploadParams("", "rollout.jsonl", "unknown", nil, "", false)
	if withoutLogs.Classification != "other" || withoutLogs.ThreadID != nil || withoutLogs.ExtraLogFiles != nil || withoutLogs.Tags != nil {
		t.Fatalf("feedback fallback params = %#v", withoutLogs)
	}
}

func TestMCPInventoryMapsFromStatusesMatchRust(t *testing.T) {
	statuses := []mcp.MCPServerStatus{{
		Name: "docs",
		Tools: []mcp.MCPToolInfo{{
			Name:        "search",
			Description: "Search docs",
		}},
		Resources: []mcp.MCPResource{{
			Name: "guide",
			URI:  "file://guide",
		}},
		ResourceTemplates: []mcp.MCPResourceTemplate{{
			Name:        "doc",
			URITemplate: "file://{name}",
		}},
		AuthStatus: mcp.MCPAuthOAuth,
	}}
	maps := MCPInventoryMapsFromStatuses(statuses)
	if maps.Tools["mcp__docs__search"].Description != "Search docs" {
		t.Fatalf("tool map = %#v", maps.Tools)
	}
	if maps.AuthStatuses["docs"] != mcp.MCPAuthOAuth {
		t.Fatalf("auth map = %#v", maps.AuthStatuses)
	}
	if len(maps.Resources["docs"]) != 1 || maps.Resources["docs"][0].URI != "file://guide" {
		t.Fatalf("resources = %#v", maps.Resources)
	}
	if len(maps.ResourceTemplates["docs"]) != 1 || maps.ResourceTemplates["docs"][0].URITemplate != "file://{name}" {
		t.Fatalf("templates = %#v", maps.ResourceTemplates)
	}
}
